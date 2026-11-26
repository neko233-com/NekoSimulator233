package avd

import (
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type PlayImageInstallResult struct {
	API              int  `json:"api"`
	AlreadyInstalled bool `json:"already_installed"`
}

var sdkProgressPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]{1,3})%`)

// sdkProgress deliberately publishes only bounded numeric progress; SDK tool
// output can contain untrusted URLs or local paths and is not persisted or displayed.
type sdkProgress struct {
	mu           sync.Mutex
	tail         string
	last         int
	licenseIssue bool
	notify       func(int)
}

func (p *sdkProgress) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tail += string(data)
	if len(p.tail) > 4096 {
		p.tail = p.tail[len(p.tail)-4096:]
	}
	lower := strings.ToLower(p.tail)
	if strings.Contains(lower, "license") && (strings.Contains(lower, "not accepted") || strings.Contains(lower, "not been accepted") || strings.Contains(lower, "accept?")) {
		p.licenseIssue = true
	}
	matches := sdkProgressPattern.FindAllStringSubmatch(p.tail, -1)
	if len(matches) != 0 {
		n, _ := strconv.Atoi(matches[len(matches)-1][1])
		if n <= 100 && n > p.last {
			p.last = n
			if p.notify != nil {
				p.notify(n)
			}
		}
	}
	return len(data), nil
}

func playImagePath(sdk string, api int) (string, string, error) {
	if sdk == "" || (api != 30 && api != 34 && api != 35) {
		return "", "", errors.New("不支持的 Android 版本或 SDK 路径")
	}
	root, err := filepath.Abs(sdk)
	if err != nil {
		return "", "", err
	}
	return root, filepath.Join(root, "system-images", fmt.Sprintf("android-%d", api), "google_apis_playstore", "x86_64"), nil
}

func noReparse(path string) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(name)
	return err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

func checkSDKImageParents(root string, api int) error {
	for _, path := range []string{
		filepath.Join(root, "system-images"),
		filepath.Join(root, "system-images", fmt.Sprintf("android-%d", api)),
		filepath.Join(root, "system-images", fmt.Sprintf("android-%d", api), "google_apis_playstore"),
	} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || !noReparse(path) {
			return errors.New("SDK 镜像上级目录包含链接或不是目录，未修改")
		}
	}
	return nil
}

func completePlayImage(dir string, api int) bool {
	if !noReparse(dir) {
		return false
	}
	for _, name := range []string{"system.img", "source.properties", "package.xml", "build.prop"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || (name != "system.img" && info.Size() > 4<<20) || !noReparse(path) {
			return false
		}
	}
	properties, err := parseINI(filepath.Join(dir, "source.properties"))
	if err != nil || strings.TrimSpace(properties["AndroidVersion.ApiLevel"]) != strconv.Itoa(api) || properties["SystemImage.Abi"] != "x86_64" || properties["SystemImage.TagId"] != "google_apis_playstore" || strings.TrimSpace(properties["Pkg.Revision"]) == "" {
		return false
	}
	build, err := parseINI(filepath.Join(dir, "build.prop"))
	if err != nil {
		return false
	}
	version, err := strconv.ParseInt(strings.TrimSpace(build["ro.build.version.incremental"]), 10, 32)
	return err == nil && version > 0
}

func sdkManagerJava() (string, error) {
	// An app-specific selection takes precedence over stale machine-wide
	// environment variables and is shared by the GUI and MCP processes.
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		path := filepath.Join(local, "NekoSimulator233-data", "jdk-home.txt")
		f, err := os.Open(path)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(f, 4097))
			closeErr := f.Close()
			home := strings.TrimSpace(string(data))
			if readErr != nil || closeErr != nil || len(data) > 4096 || !filepath.IsAbs(home) || strings.ContainsAny(home, "\x00\r\n") {
				return "", errors.New("Neko JDK 配置无效，请检查 jdk-home.txt")
			}
			candidate := filepath.Join(home, "bin", "java.exe")
			if !nativeJava(candidate) {
				return "", errors.New("Neko JDK 不存在或架构不匹配，请重新配置 jdk-home.txt")
			}
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("无法读取 Neko JDK 配置：%w", err)
		}
	}
	for _, key := range []string{"JAVA_HOME", "JDK_HOME"} {
		if base := os.Getenv(key); base != "" {
			candidate := filepath.Join(base, "bin", "java.exe")
			if nativeJava(candidate) {
				return candidate, nil
			}
		}
	}
	if studio := os.Getenv("ANDROID_STUDIO_HOME"); studio != "" {
		candidate := filepath.Join(studio, "jbr", "bin", "java.exe")
		if nativeJava(candidate) {
			return candidate, nil
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		managed := filepath.Join(local, "NekoSimulator233-data", "runtime", "jdk", "bin", "java.exe")
		if nativeJava(managed) {
			return managed, nil
		}
	}
	if java, err := exec.LookPath("java.exe"); err == nil && nativeJava(java) {
		return java, nil
	}
	for _, pat := range []string{
		`C:\Program Files\Eclipse Adoptium\jdk-*\bin\java.exe`,
		`C:\Program Files\Microsoft\jdk-*\bin\java.exe`,
		`C:\Program Files\Java\jdk-*\bin\java.exe`,
		`C:\Program Files\Android\Android Studio\jbr\bin\java.exe`,
		`D:\Tools\jdk-*\**\bin\java.exe`,
	} {
		matches, _ := filepath.Glob(pat)
		for _, cand := range matches {
			if nativeJava(cand) {
				return cand, nil
			}
		}
	}
	return "", errors.New("未找到当前程序架构的 Java；请配置 Neko JDK 或将 JAVA_HOME 设置为对应架构的 JDK 17 或更高版本")
}

func nativeJava(path string) bool {
	f, err := pe.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	switch runtime.GOARCH {
	case "amd64":
		return f.Machine == pe.IMAGE_FILE_MACHINE_AMD64
	case "arm64":
		return f.Machine == pe.IMAGE_FILE_MACHINE_ARM64
	default:
		return false
	}
}

func sdkManagerTools(root string) (string, string, error) {
	base := filepath.Join(root, "cmdline-tools")
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", "", errors.New("缺少 Android SDK Command-line Tools")
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name() == "latest" {
			return true
		}
		if entries[j].Name() == "latest" {
			return false
		}
		return entries[i].Name() > entries[j].Name()
	})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tools := filepath.Join(base, entry.Name())
		classpath := filepath.Join(tools, "lib", "sdkmanager-classpath.jar")
		if exists(classpath) {
			return tools, classpath, nil
		}
	}
	return "", "", errors.New("缺少 SDK Manager 的 classpath 文件，请安装 Command-line Tools")
}

// lockSDKInstall serializes installations initiated by Neko across processes.
// It does not assert that external SDK managers are idle.
func lockSDKInstall(ctx context.Context, root string) (func(), error) {
	name, err := windows.UTF16PtrFromString(filepath.Join(root, ".neko-sdk-install.lock"))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("SDK 安装锁不可用或包含链接")
	}
	var overlap windows.Overlapped
	for {
		if err = ctx.Err(); err != nil {
			windows.CloseHandle(h)
			return nil, err
		}
		err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
		if err == nil {
			return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, &overlap); _ = windows.CloseHandle(h) }, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			windows.CloseHandle(h)
			return nil, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			windows.CloseHandle(h)
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// InstallPlayImage installs only the selected Google Play system image and
// never changes an AVD. Existing or incomplete image directories are not
// updated or repaired. License prompts receive EOF instead of acceptance.
func (m Manager) InstallPlayImage(ctx context.Context, api int, notify func(int)) (PlayImageInstallResult, error) {
	root, dir, err := playImagePath(m.SDK, api)
	if err != nil {
		return PlayImageInstallResult{}, err
	}
	unlock, err := lockSDKInstall(ctx, root)
	if err != nil {
		return PlayImageInstallResult{}, fmt.Errorf("无法锁定 SDK 安装：%w", err)
	}
	defer unlock()
	if err := checkSDKImageParents(root, api); err != nil {
		return PlayImageInstallResult{}, err
	}
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !noReparse(dir) {
			return PlayImageInstallResult{}, errors.New("镜像目录不是普通目录，未修改")
		}
		if completePlayImage(dir, api) {
			return PlayImageInstallResult{API: api, AlreadyInstalled: true}, nil
		}
		return PlayImageInstallResult{}, errors.New("目标镜像目录已存在但不完整；请在 SDK Manager 检查，未自动覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		return PlayImageInstallResult{}, err
	}
	java, err := sdkManagerJava()
	if err != nil {
		return PlayImageInstallResult{}, err
	}
	tools, classpath, err := sdkManagerTools(root)
	if err != nil {
		return PlayImageInstallResult{}, err
	}
	packageID := fmt.Sprintf("system-images;android-%d;google_apis_playstore;x86_64", api)
	cmd := exec.CommandContext(ctx, java, "-Dcom.android.sdklib.toolsdir="+tools, "-classpath", classpath, "com.android.sdklib.tool.sdkmanager.SdkManagerCli", "--sdk_root="+root, "--install", packageID, "--channel=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader("")
	progress := &sdkProgress{last: -1, notify: notify}
	cmd.Stdout, cmd.Stderr = progress, progress
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return PlayImageInstallResult{}, ctx.Err()
		}
		if progress.licenseIssue {
			return PlayImageInstallResult{}, errors.New("SDK 许可证尚未接受，请在 Android Studio 的 SDK Manager 中查看并接受后重试")
		}
		return PlayImageInstallResult{}, fmt.Errorf("Google Play 镜像安装失败；请检查 Java、网络和 SDK Manager：%w", err)
	}
	if !completePlayImage(dir, api) {
		if progress.licenseIssue {
			return PlayImageInstallResult{}, errors.New("SDK 许可证尚未接受，请在 Android Studio 的 SDK Manager 中查看并接受后重试")
		}
		return PlayImageInstallResult{}, errors.New("SDK Manager 未生成完整的 Google Play 系统镜像")
	}
	return PlayImageInstallResult{API: api}, nil
}

var _ io.Writer = (*sdkProgress)(nil)
