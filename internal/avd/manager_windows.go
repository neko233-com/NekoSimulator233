//go:build windows

package avd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"golang.org/x/sys/windows"
)

const DefaultName = "NekoSimulator233"
const defaultName = DefaultName

type Manager struct{ SDK, Home, Name string }
type Instance struct {
	Name, Serial, VulkanDevice string
	KeyboardEnabled            bool
	port                       int
	pid                        uint32
	token                      string
	sdk                        string
}

var errNoVulkan = errors.New("未检测到硬件 Vulkan GPU；请检查显卡驱动与 Android Emulator 配置")
var errInstanceNotRunning = errors.New("项目实例未运行")

func DiscoverManager() (Manager, error) {
	var candidates []string
	local := os.Getenv("LOCALAPPDATA")
	if local != "" {
		candidates = append(candidates, filepath.Join(local, "NekoSimulator233-data", "runtime", "sdk"))
	}
	for _, key := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if value := os.Getenv(key); value != "" {
			candidates = append(candidates, value)
		}
	}
	if local != "" {
		candidates = append(candidates, filepath.Join(local, "Android", "Sdk"))
	}
	for _, sdk := range candidates {
		if exists(filepath.Join(sdk, "emulator", "emulator.exe")) && exists(filepath.Join(sdk, "platform-tools", "adb.exe")) {
			if local == "" {
				return Manager{}, errors.New("未找到 LOCALAPPDATA")
			}
			return Manager{SDK: sdk, Home: filepath.Join(local, "NekoSimulator233-data", "avd"), Name: defaultName}, nil
		}
	}
	// A command-line-tools-only SDK can install its missing runtime packages.
	for _, sdk := range candidates {
		if _, _, err := sdkManagerTools(sdk); err == nil {
			if local == "" {
				return Manager{}, errors.New("未找到 LOCALAPPDATA")
			}
			return Manager{SDK: sdk, Home: filepath.Join(local, "NekoSimulator233-data", "avd"), Name: defaultName}, nil
		}
	}
	return Manager{}, errors.New("未找到可用的 Android SDK 或 Command-line Tools，请设置 ANDROID_HOME")
}

func exists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }
func (m Manager) validate() error {
	if m.SDK == "" || m.Home == "" || m.Name == "" || strings.ContainsAny(m.Name, "\\/:\r\n") || strings.ContainsAny(m.Home, "\r\n") {
		return errors.New("实例路径或名称无效")
	}
	return nil
}

func writeNew(path, contents string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(contents)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func imageTagConfig(tag string) string {
	if tag == "google_apis_playstore" {
		return "tag.id=google_apis_playstore\ntag.display=Google Play\nPlayStore.enabled=true"
	}
	return "tag.id=google_apis\ntag.display=Google APIs"
}

// Ensure creates only our own AVD metadata. Existing data and configuration
// are never overwritten or reset.
func (m Manager) Ensure() error {
	return m.ensureWithAPI(0)
}

// ensureWithAPI only chooses an image for a new AVD; existing metadata is
// always preserved. API 0 keeps the existing Android 15/14 default policy.
func (m Manager) ensureWithAPI(requestedAPI int) error {
	return m.ensureWithImagePolicy(requestedAPI, false)
}

func (m Manager) ensureWithImagePolicy(requestedAPI int, requirePlay bool) error {
	if err := m.validate(); err != nil {
		return err
	}
	if requestedAPI != 0 && requestedAPI != 30 && requestedAPI != 34 && requestedAPI != 35 {
		return errors.New("不支持的 Android 系统版本")
	}
	home, err := filepath.Abs(m.Home)
	if err != nil {
		return err
	}
	dir := filepath.Join(home, m.Name+".avd")
	ini := filepath.Join(home, m.Name+".ini")
	config := filepath.Join(dir, "config.ini")
	if exists(ini) && exists(config) {
		if _, err := ownedAVD(m); err != nil {
			return err
		}
		values, err := parseINI(config)
		if err != nil {
			return err
		}
		api, tag, err := systemImageIdentity(values)
		if err != nil {
			return err
		}
		if tag == "google_apis_playstore" && !completePlayImage(filepath.Join(m.SDK, "system-images", fmt.Sprintf("android-%d", api), tag, "x86_64"), api) {
			return errors.New("Google Play 系统镜像缺失或不完整，未启动实例")
		}
		return nil
	}
	if exists(ini) || exists(config) {
		return errors.New("实例配置不完整，已保留现有数据；请检查 AVD 目录")
	}
	var imagePath, api, imageTag string
	levels := []string{"35", "34"}
	if requestedAPI != 0 {
		levels = []string{strconv.Itoa(requestedAPI)}
	}
	tags := []string{"google_apis_playstore"}
	if !requirePlay {
		tags = append(tags, "google_apis")
	}
	// NEKO_PREFER_USERDEBUG_IMAGE makes new instances prefer the userdebug
	// google_apis image (MuMu-like identity injection). GUI and MCP enable it
	// by default; callers that require Play images are unaffected.
	if !requirePlay && strings.EqualFold(os.Getenv("NEKO_PREFER_USERDEBUG_IMAGE"), "1") {
		tags = []string{"google_apis", "google_apis_playstore"}
	}
	for _, tag := range tags {
		for _, level := range levels {
			p := filepath.Join("system-images", "android-"+level, tag, "x86_64")
			installed := exists(filepath.Join(m.SDK, p, "system.img"))
			if tag == "google_apis_playstore" {
				apiNumber, _ := strconv.Atoi(level)
				installed = completePlayImage(filepath.Join(m.SDK, p), apiNumber)
			}
			if installed {
				imagePath, api, imageTag = p, level, tag
				break
			}
		}
		if imagePath != "" {
			break
		}
	}
	if imagePath == "" {
		if requirePlay {
			return errors.New("Google Play 系统镜像缺失或不完整，请先使用“安装 Google Play 镜像…”后重试")
		}
		if requestedAPI != 0 {
			return fmt.Errorf("缺少 Android %d Google Play 或 Google APIs x86_64 系统镜像；请使用 SDK Manager 安装 system-images;android-%d;google_apis_playstore;x86_64", requestedAPI, requestedAPI)
		}
		return errors.New("缺少 Android 34 或 35 的 Google Play 或 Google APIs x86_64 系统镜像")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	// An existing directory with missing metadata may still contain guest disks.
	// Never turn it into a new AVD, even if it currently looks empty. Mkdir is
	// also the atomic check against a directory appearing after inspection.
	if err := os.Mkdir(dir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("实例目录已存在但配置不完整，已保留现有数据；请检查 AVD 目录")
		}
		return err
	}
	settings := fmt.Sprintf("AvdId=%s\navd.ini.displayname=%s\nabi.type=x86_64\nimage.sysdir.1=%s%c\n%s\nhw.cpu.arch=x86_64\nhw.cpu.ncore=2\nhw.ramSize=4096\nhw.lcd.width=1080\nhw.lcd.height=1920\nhw.lcd.density=480\nhw.gpu.enabled=yes\nhw.gpu.mode=host\nhw.keyboard=yes\ndisk.dataPartition.size=16G\nfastboot.forceColdBoot=yes\nshowDeviceFrame=no\n", m.Name, m.Name, imagePath, os.PathSeparator, imageTagConfig(imageTag))
	if err := writeNew(config, settings); err != nil {
		return fmt.Errorf("创建实例配置失败：%w", err)
	}
	if err := writeNew(filepath.Join(dir, tapTapPendingFile), tapTapMarkerContents); err != nil {
		return fmt.Errorf("创建默认应用准备状态失败：%w", err)
	}
	if err := writeNew(ini, fmt.Sprintf("avd.ini.encoding=UTF-8\npath=%s\ntarget=android-%s\n", dir, api)); err != nil {
		return fmt.Errorf("创建实例索引失败，原有数据已保留：%w", err)
	}
	return nil
}

// enableKeyboard updates only the owned AVD config while the VM is stopped.
// The atomic replacement preserves all other lines and the VM's user data.
func (m Manager) enableKeyboard() error {
	path := filepath.Join(m.Home, m.Name+".avd", "config.ini")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	found, changed := false, false
	for i, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(key) != "hw.keyboard" {
			continue
		}
		found = true
		if strings.TrimSpace(value) != "yes" {
			ending := ""
			if strings.HasSuffix(line, "\r") {
				ending = "\r"
			}
			lines[i] = "hw.keyboard=yes" + ending
			changed = true
		}
	}
	if !found {
		ending := ""
		if strings.Contains(string(data), "\r\n") {
			ending = "\r"
		}
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines[len(lines)-1] = "hw.keyboard=yes" + ending
			lines = append(lines, "")
		} else {
			lines = append(lines, "hw.keyboard=yes"+ending)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".keyboard-config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.WriteString(strings.Join(lines, "\n")); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	src, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func parseINI(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := make(map[string]string)
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		key, value, ok := strings.Cut(scan.Text(), "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values, scan.Err()
}

func (m Manager) discover() (Instance, error) {
	dirs := []string{filepath.Join(os.TempDir(), "avd", "running")}
	// Hosts and harnesses may run the agent with TEMP pointed at the Windows
	// directory while the emulator (launched earlier) advertised into the
	// per-user temp. Check both so an already-running instance is still found.
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, "Temp", "avd", "running"))
	}
	var lastErr error = errInstanceNotRunning
	for _, dir := range dirs {
		instance, err := m.discoverIn(dir, processRunning)
		if err == nil {
			return instance, nil
		}
		if !errors.Is(err, errInstanceNotRunning) {
			lastErr = err
		}
	}
	return Instance{}, lastErr
}

// Unknown or incomplete metadata for a live process is not proof that the
// owned instance is stopped. Never launch or report shutdown on that basis.
func (m Manager) discoverIn(directory string, probe func(uint32) (bool, error)) (Instance, error) {
	if err := m.validate(); err != nil {
		return Instance{}, err
	}
	files, err := filepath.Glob(filepath.Join(directory, "pid_*.ini"))
	if err != nil {
		return Instance{}, err
	}
	requested, err := filepath.Abs(filepath.Join(m.Home, m.Name+".avd"))
	if err != nil {
		return Instance{}, err
	}
	for _, file := range files {
		pidText := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), "pid_"), ".ini")
		pid, parseErr := strconv.ParseUint(pidText, 10, 32)
		if parseErr != nil {
			continue
		}
		running, probeErr := probe(uint32(pid))
		if probeErr == nil && !running {
			continue
		}
		values, err := parseINI(file)
		if errors.Is(err, os.ErrNotExist) {
			alive, probeAgain := probe(uint32(pid))
			if probeAgain != nil || alive {
				return Instance{}, errors.New("模拟器发现信息发生变化，无法确认实例状态")
			}
			continue
		}
		if err != nil {
			return Instance{}, errors.New("无法读取运行中模拟器的发现信息，未更改实例")
		}
		if values["avd.name"] == "" {
			return Instance{}, errors.New("运行中模拟器的发现信息尚不完整，请稍后重试")
		}
		if values["avd.name"] != m.Name {
			continue
		}
		if values["avd.dir"] == "" {
			return Instance{}, errors.New("同名模拟器缺少实例路径，未更改实例")
		}
		dir, err := filepath.Abs(values["avd.dir"])
		if err != nil {
			return Instance{}, errors.New("同名模拟器的实例路径无效，未更改实例")
		}
		if !strings.EqualFold(filepath.Clean(dir), filepath.Clean(requested)) {
			continue
		}
		if probeErr != nil {
			return Instance{}, fmt.Errorf("无法确认实例进程状态，未更改实例：%w", probeErr)
		}
		serial, err1 := strconv.Atoi(values["port.serial"])
		port, err2 := strconv.Atoi(values["grpc.port"])
		if err1 != nil || err2 != nil || serial < 5554 || serial > 5682 || serial%2 != 0 || port < 1 || port > 65535 || values["grpc.token"] == "" {
			return Instance{}, errors.New("实例正在运行，但控制端点不完整；未启动第二个实例")
		}
		return Instance{Name: m.Name, Serial: fmt.Sprintf("emulator-%d", serial), port: port, pid: uint32(pid), token: values["grpc.token"], sdk: m.SDK}, nil
	}
	return Instance{}, errInstanceNotRunning
}

func processRunning(pid uint32) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err = windows.GetExitCodeProcess(h, &code); err != nil {
		return false, err
	}
	return code == 259, nil // STILL_ACTIVE
}

func (m Manager) adb(ctx context.Context, serial string, args ...string) ([]byte, error) {
	args = append([]string{"-s", serial}, args...)
	cmd := exec.CommandContext(ctx, filepath.Join(m.SDK, "platform-tools", "adb.exe"), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("ADB 命令失败：%w", err)
	}
	return out, nil
}

func (m Manager) inspect(ctx context.Context, instance Instance) (Instance, error) {
	out, err := m.adb(ctx, instance.Serial, "emu", "avd", "name")
	if err != nil {
		return Instance{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != m.Name {
		return Instance{}, errors.New("模拟器端口属于其他实例")
	}
	out, err = m.adb(ctx, instance.Serial, "shell", "getprop", "sys.boot_completed")
	if err != nil || strings.TrimSpace(string(out)) != "1" {
		return Instance{}, errors.New("Android 尚未启动完成")
	}
	out, err = m.adb(ctx, instance.Serial, "shell", "cmd", "gpu", "vkjson")
	if err != nil {
		return Instance{}, fmt.Errorf("无法检查 Vulkan：%w", err)
	}
	var result struct {
		Devices []struct {
			Properties struct {
				DeviceName string  `json:"deviceName"`
				DeviceType float64 `json:"deviceType"`
			} `json:"properties"`
		} `json:"devices"`
	}
	if json.Unmarshal(out, &result) != nil {
		return Instance{}, errors.New("Vulkan 设备信息无效")
	}
	for _, device := range result.Devices {
		if device.Properties.DeviceType == 1 || device.Properties.DeviceType == 2 {
			instance.VulkanDevice = device.Properties.DeviceName
			break
		}
	}
	if instance.VulkanDevice == "" {
		return Instance{}, errNoVulkan
	}
	config, err := parseINI(filepath.Join(m.Home, m.Name+".avd", "hardware-qemu.ini"))
	if err == nil {
		value := strings.ToLower(config["hw.keyboard"])
		instance.KeyboardEnabled = value == "true" || value == "yes" || value == "1"
	}
	return instance, nil
}

func freePort(port int) bool {
	l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}
func freeConsolePort() (int, error) {
	for port := 5554; port <= 5680; port += 2 {
		if freePort(port) && freePort(port+1) {
			return port, nil
		}
	}
	return 0, errors.New("没有可用的模拟器端口")
}
func freeGRPCPort() (int, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// An OS file lock coordinates separate GUI and MCP processes. The lock file
// remains in the project data directory and is not the user's VM data.
func (m Manager) lockLifecycle(ctx context.Context, create bool) (func(), error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(m.Home, 0700); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(filepath.Join(m.Home, ".lifecycle.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlap windows.Overlapped
	for {
		if err = ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
		if err == nil {
			return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlap); _ = f.Close() }, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			f.Close()
			return nil, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Start attaches only to our AVD, or launches one if it is absent. Boot and
// hardware Vulkan are verified before reporting a ready instance.
func (m Manager) Start(ctx context.Context) (Instance, error) {
	return m.StartWithProgress(ctx, nil)
}

// StartWithProgress prepares the official Play image on first start and then
// limits the boot phase separately. Existing instances are never changed.
func (m Manager) StartWithProgress(ctx context.Context, notify func(int)) (Instance, error) {
	imageProgress := func(n int) {
		if notify != nil {
			notify(n * 4 / 5)
		}
	}
	runtimeProgress := func(n int) {
		if notify != nil {
			notify(80 + n/5)
		}
	}
	if err := m.prepareStart(ctx, imageProgress, func(ctx context.Context, api int, notify func(int)) error {
		_, err := m.InstallPlayImage(ctx, api, notify)
		return err
	}); err != nil {
		return Instance{}, err
	}
	if err := m.InstallSDKRuntime(ctx, runtimeProgress); err != nil {
		return Instance{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return Instance{}, err
	}
	defer unlock()
	if err := m.ensureWithImagePolicy(0, true); err != nil {
		return Instance{}, err
	}
	if instance, err := m.discover(); err == nil {
		ready, err := m.waitReady(ctx, instance.Serial)
		if err == nil {
			m.scheduleTapTap(ready)
		}
		return ready, err
	} else if !errors.Is(err, errInstanceNotRunning) {
		return Instance{}, err
	}
	releaseBuildGuard, err := m.guardSystemBuild()
	if err != nil {
		return Instance{}, err
	}
	defer releaseBuildGuard()
	if err := m.enableKeyboard(); err != nil {
		return Instance{}, fmt.Errorf("启用独立实例键盘失败：%w", err)
	}
	port, err := freeConsolePort()
	if err != nil {
		return Instance{}, err
	}
	grpcPort, err := freeGRPCPort()
	if err != nil {
		return Instance{}, err
	}
	phone, err := m.PhoneIdentity()
	if err != nil {
		return Instance{}, fmt.Errorf("读取手机身份配置失败：%w", err)
	}
	// emulator.exe translates -prop before handing off to QEMU, so the property
	// overrides must precede the -avd option.
	args := append(phone.PropArgs(), "-avd", m.Name, "-no-window", "-no-boot-anim", "-no-snapshot", "-gpu", "host", "-feature", "Vulkan", "-port", strconv.Itoa(port), "-grpc", strconv.Itoa(grpcPort), "-grpc-use-token")
	if !phone.Empty() {
		// Required for adb remount so phone identity can be written at boot.
		args = append(args, "-writable-system")
	}
	resources, err := resourcesOverride(filepath.Join(m.Home, m.Name+".avd"))
	if err != nil {
		return Instance{}, err
	}
	if resources != nil {
		args = append(args, "-memory", strconv.Itoa(resources.RAMMiB), "-cores", strconv.Itoa(resources.Cores))
	}
	cmd := exec.Command(filepath.Join(m.SDK, "emulator", "emulator.exe"), args...)
	cmd.SysProcAttr = emulatorProcAttr()
	cmd.Env = append(os.Environ(), "ANDROID_AVD_HOME="+m.Home, "ANDROID_HOME="+m.SDK)
	if os.Getenv("NEKO_EMULATOR_DEBUG") == "1" {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		return Instance{}, fmt.Errorf("启动 Android Emulator 失败：%w", err)
	}
	// A launcher that exits successfully may have handed off to QEMU. An
	// unsuccessful exit is useful only while no matching instance exists.
	launcherExit := make(chan error, 1)
	go func() { launcherExit <- cmd.Wait(); close(launcherExit) }()
	ready, err := m.waitReadyWithLauncher(ctx, fmt.Sprintf("emulator-%d", port), launcherExit)
	if err == nil {
		if identity, idErr := m.PhoneIdentity(); idErr == nil && !identity.Empty() {
			// Identity injection reboots the guest; use a fresh timeout so the
			// caller's start deadline cannot cancel the reboot handshake.
			applyCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			if applyErr := m.ApplyPhoneIdentity(applyCtx, ready); applyErr != nil {
				cancel()
				return ready, fmt.Errorf("手机身份注入失败：%w", applyErr)
			}
			time.Sleep(5 * time.Second)
			ready, err = m.waitReady(applyCtx, ready.Serial)
			cancel()
		}
	}
	if err == nil {
		m.scheduleTapTap(ready)
	}
	return ready, err
}

// emulatorProcAttr detaches QEMU from the launching console. Without
// DETACHED_PROCESS, closing the cmd/PowerShell window sends CTRL_CLOSE_EVENT to
// the whole process group and silently kills every running emulator.
func emulatorProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func (m Manager) waitReady(ctx context.Context, serial string) (Instance, error) {
	return m.waitReadyWithLauncher(ctx, serial, nil)
}

func (m Manager) waitReadyWithLauncher(ctx context.Context, serial string, launcherExit <-chan error) (Instance, error) {
	return waitInstanceReady(ctx, serial, m.discover, m.inspect, launcherExit, 5*time.Second)
}

func waitInstanceReady(ctx context.Context, serial string, discover func() (Instance, error), inspect func(context.Context, Instance) (Instance, error), launcherExit <-chan error, handoffGrace time.Duration) (Instance, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var launcherFailure error
	var failureTimer *time.Timer
	var failureDeadline <-chan time.Time
	defer func() {
		if failureTimer != nil {
			failureTimer.Stop()
		}
	}()
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return Instance{}, fmt.Errorf("实例启动或 Vulkan 检查已取消：%w", err)
		}
		instance, err := discover()
		if err == nil && instance.Serial == serial {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			ready, probeErr := inspect(probeCtx, instance)
			cancel()
			if probeErr == nil {
				if err := ctx.Err(); err != nil {
					return Instance{}, fmt.Errorf("实例启动或 Vulkan 检查已取消：%w", err)
				}
				return ready, nil
			}
			if errors.Is(probeErr, errNoVulkan) {
				return Instance{}, probeErr
			}
			last = probeErr
		} else if err != nil {
			last = err
		}
		select {
		case <-ctx.Done():
			return Instance{}, fmt.Errorf("实例启动或 Vulkan 检查超时：%v：%w", last, ctx.Err())
		case launchErr, ok := <-launcherExit:
			launcherExit = nil
			if !ok || launchErr == nil {
				continue // A successful launcher exit can be a normal handoff.
			}
			// Allow a child process time to register even if the wrapper
			// exits unsuccessfully after handing off to it.
			launcherFailure = launchErr
			failureTimer = time.NewTimer(handoffGrace)
			failureDeadline = failureTimer.C
		case <-failureDeadline:
			failureDeadline = nil
			if instance, err := discover(); err == nil && instance.Serial == serial {
				continue
			}
			return Instance{}, fmt.Errorf("Android Emulator 启动程序异常退出且实例未注册，请检查虚拟化、显卡驱动和 SDK 安装：%w", launcherFailure)
		case <-ticker.C:
		}
	}
}

func (i Instance) Open() (*Controller, error) {
	c, err := DialController(i.port, i.token)
	if err != nil {
		return nil, err
	}
	c.keyboardDisabled = !i.KeyboardEnabled
	c.navigate = func(action display.Action) error {
		codes := map[display.Action]int{display.Back: 4, display.Home: 3, display.Overview: 187, display.VolumeUp: 24, display.VolumeDown: 25}
		code, ok := codes[action]
		if !ok {
			return errors.New("不支持的导航操作")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return (Manager{SDK: i.sdk}).PressKey(ctx, i, code)
	}
	return c, nil
}

// Ready checks the already-running project instance without starting it.
func (m Manager) Ready(ctx context.Context) (Instance, error) {
	instance, err := m.discover()
	if err != nil {
		return Instance{}, err
	}
	return m.inspect(ctx, instance)
}

var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)

func (m Manager) ListApps(ctx context.Context, instance Instance) ([]string, error) {
	out, err := m.adb(ctx, instance.Serial, "shell", "pm", "list", "packages", "-3")
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		name := strings.TrimPrefix(strings.TrimSpace(line), "package:")
		if packageName.MatchString(name) {
			result = append(result, name)
		}
	}
	return result, nil
}

func (m Manager) LaunchApp(ctx context.Context, instance Instance, name string) error {
	if !packageName.MatchString(name) {
		return errors.New("Android 包名无效")
	}
	out, err := m.adb(ctx, instance.Serial, "shell", "monkey", "-p", name, "-c", "android.intent.category.LAUNCHER", "1")
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "Events injected: 1") {
		return errors.New("应用没有可启动的入口，或启动失败")
	}
	return nil
}

// InstallAPK uses the SDK adb client directly; the local path is not sent to a
// shell. Replacement is opt-in so an installed package is not silently updated.
func (m Manager) InstallAPK(ctx context.Context, instance Instance, path string, replace bool) error {
	return m.InstallAPKs(ctx, instance, []string{path}, replace)
}

// InstallAPKs installs one APK or a set of split APKs as one package-manager
// transaction. The lifecycle lock keeps the selected ADB target stable.
func (m Manager) InstallAPKs(ctx context.Context, instance Instance, paths []string, replace bool) error {
	return m.withCurrentInstanceGuard(ctx, instance, func(ctx context.Context, current Instance) error {
		return m.installAPKsUnchecked(ctx, current, paths, replace)
	})
}

// The caller must hold the instance lifecycle lock and have verified identity.
func (m Manager) installAPKsUnchecked(ctx context.Context, instance Instance, paths []string, replace bool) error {
	if len(paths) == 0 {
		return errors.New("请选择至少一个 APK 文件")
	}
	fullPaths := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		full, err := filepath.Abs(path)
		if err != nil {
			return errors.New("APK 路径无效")
		}
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || !strings.EqualFold(filepath.Ext(full), ".apk") {
			return errors.New("APK 文件不存在或无效")
		}
		key := strings.ToLower(filepath.Clean(full))
		if seen[key] {
			return errors.New("不能重复选择同一个 APK 文件")
		}
		seen[key] = true
		fullPaths = append(fullPaths, full)
	}
	args := []string{"install"}
	if len(fullPaths) > 1 {
		args[0] = "install-multiple"
	}
	// Android requires -t for packages marked testOnly, including APKs
	// produced by some development builds. Signature checks still apply.
	args = append(args, "-t")
	if replace {
		args = append(args, "-r")
	} else {
		// Current adb can replace by default. Package Manager's -R makes
		// the no-replacement policy atomic even across concurrent installs.
		args = append(args, "-R")
	}
	args = append(args, fullPaths...)
	out, err := m.adb(ctx, instance.Serial, args...)
	if err != nil {
		return apkInstallError(out, err)
	}
	success := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Failure") || strings.HasPrefix(line, "Error") {
			return apkInstallError(out, errors.New("Android 包管理器报告安装失败"))
		}
		if line == "Success" {
			success = true
		}
	}
	if !success {
		return apkInstallError(out, errors.New("Android 包管理器未报告安装成功"))
	}
	return nil
}

// Android's package-manager rejection (e.g. a conflicting package signature)
// is usually printed by adb, not represented by its process exit status.
func apkInstallError(output []byte, err error) error {
	reason := strings.TrimSpace(string(output))
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if stderr != "" {
			if reason != "" {
				reason += "\n"
			}
			reason += stderr
		}
	}
	if reason == "" {
		return fmt.Errorf("安装 APK 失败：%w", err)
	}
	if strings.Contains(reason, "INSTALL_FAILED_NO_MATCHING_ABIS") {
		reason += "；APK 的 CPU 架构与实例不匹配。仅含 32 位 ARM 库的应用可尝试新建 Android 11 兼容实例"
	}
	const maxReason = 2048
	if runes := []rune(reason); len(runes) > maxReason {
		reason = string(runes[len(runes)-maxReason:])
	}
	return fmt.Errorf("安装 APK 失败：%s：%w", reason, err)
}

func (m Manager) PressKey(ctx context.Context, instance Instance, keycode int) error {
	if keycode < 0 || keycode > 999 {
		return errors.New("Android 按键代码无效")
	}
	_, err := m.adb(ctx, instance.Serial, "shell", "input", "keyevent", strconv.Itoa(keycode))
	return err
}

// textScript uses shell quoting for every segment and splits literal "%s",
// which Android input text otherwise interprets as a space. Text is supplied
// over stdin, never in the host process command line.
func textScript(text string) (string, error) {
	if len(text) == 0 || len(text) > 512 {
		return "", errors.New("文本长度必须为 1 到 512 字节")
	}
	for _, r := range text {
		if r < 32 || r > 126 {
			return "", errors.New("当前输入接口仅支持可打印 ASCII")
		}
	}
	quote := func(part string) string {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(part, " ", "%s"), "'", "'\\''") + "'"
	}
	parts := strings.Split(text, "%s")
	var commands []string
	for i, part := range parts {
		if i > 0 {
			commands = append(commands, "input text '%'; input text 's'")
		}
		if part != "" {
			commands = append(commands, "input text "+quote(part))
		}
	}
	return "set -e\n" + strings.Join(commands, "\n") + "\n", nil
}

func (m Manager) InputText(ctx context.Context, instance Instance, text string) (result error) {
	if !utf8.ValidString(text) || len(text) == 0 || len(text) > 512 {
		return errors.New("文本必须是 1 到 512 字节的有效 UTF-8")
	}
	ascii := true
	for _, r := range text {
		if unicode.IsControl(r) {
			return errors.New("文本不能包含控制字符")
		}
		if r > 126 {
			ascii = false
		}
	}
	if !ascii {
		c, err := instance.Open()
		if err != nil {
			return err
		}
		defer c.Close()
		previous, err := c.Clipboard()
		if err != nil {
			return fmt.Errorf("读取实例剪贴板失败：%w", err)
		}
		if len(previous) > 64<<10 {
			return errors.New("实例剪贴板内容超过 64 KiB，无法安全恢复")
		}
		if err = c.SetClipboard(text); err != nil {
			return fmt.Errorf("设置实例剪贴板失败：%w", err)
		}
		defer func() {
			current, err := c.Clipboard()
			if err != nil {
				result = errors.Join(result, fmt.Errorf("检查实例剪贴板失败：%w", err))
				return
			}
			if current == text {
				if err = c.SetClipboard(previous); err != nil {
					result = errors.Join(result, fmt.Errorf("恢复实例剪贴板失败：%w", err))
				}
			}
		}()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			value, err := c.Clipboard()
			if err == nil && value == text {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return errors.New("等待实例剪贴板同步超时")
			case <-tick.C:
			}
		}
		if err = m.PressKey(ctx, instance, 279); err != nil {
			return fmt.Errorf("粘贴文本失败：%w", err)
		}
		// Input dispatch is asynchronous; keep the guest clipboard stable while
		// the focused application consumes the paste event.
		timer := time.NewTimer(350 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		return nil
	}
	script, err := textScript(text)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(m.SDK, "platform-tools", "adb.exe"), "-s", instance.Serial, "shell", "sh")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader(script)
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("输入文本失败：%w", err)
	}
	return nil
}

// Stop checks both the private AVD directory and console identity before
// sending shutdown; it never targets a device selected only by port.
// UIHierarchy returns untrusted guest content, bounded to four MiB.
func (m Manager) UIHierarchy(ctx context.Context, instance Instance) (string, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(m.SDK, "platform-tools", "adb.exe"), "-s", instance.Serial, "exec-out", "uiautomator", "dump", "/dev/tty")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	const limit = 4 << 20
	data, readErr := io.ReadAll(io.LimitReader(pipe, limit+1))
	if len(data) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", errors.New("UI 层级超过 4 MiB")
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return "", readErr
	}
	if waitErr != nil {
		return "", fmt.Errorf("读取 UI 层级失败：%w", waitErr)
	}
	text := string(data)
	begin := strings.Index(text, "<?xml")
	end := strings.LastIndex(text, "</hierarchy>")
	if begin < 0 || end < begin {
		return "", errors.New("设备未返回有效的 UI 层级")
	}
	return text[begin : end+len("</hierarchy>")], nil
}

func (m Manager) Stop(ctx context.Context) error {
	unlock, err := m.lockLifecycle(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	instance, err := m.discover()
	if err != nil {
		return err
	}
	out, err := m.adb(ctx, instance.Serial, "emu", "avd", "name")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != m.Name {
		return errors.New("模拟器端口属于其他实例，未发送关闭命令")
	}
	// `emu kill` terminates the emulator without a full Android shutdown.
	// Flush guest filesystem writes first; if sync fails, leave it running.
	if _, err = m.adb(ctx, instance.Serial, "shell", "sync"); err != nil {
		return fmt.Errorf("无法刷新实例数据，未关闭实例：%w", err)
	}
	if _, err = m.adb(ctx, instance.Serial, "emu", "kill"); err != nil {
		return err
	}
	return waitForProcessExit(ctx, instance.pid, processRunning)
}

// The discovery file can disappear before the emulator process has closed its
// disks. Wait on the original PID instead of treating file disappearance as exit.
func waitForProcessExit(ctx context.Context, pid uint32, probe func(uint32) (bool, error)) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive, err := probe(pid)
		if err != nil {
			return fmt.Errorf("无法确认实例进程是否已退出：%w", err)
		}
		if !alive {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待实例退出：%w", ctx.Err())
		case <-ticker.C:
		}
	}
}
