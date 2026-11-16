package avd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type sdkRuntimePackage struct{ id, directory, executable string }

const tapTapBuildToolsPackage = "build-tools;35.0.0"

var sdkRuntimePackages = []sdkRuntimePackage{
	{id: "platform-tools", directory: "platform-tools", executable: "adb.exe"},
	{id: "emulator", directory: "emulator", executable: "emulator.exe"},
}

// InstallSDKRuntime installs only absent runtime packages using the selected
// SDK's command-line tools. It never replaces existing incomplete folders.
func (m Manager) InstallSDKRuntime(ctx context.Context, notify func(int)) error {
	return m.installSDKRuntime(ctx, notify, func(ctx context.Context, root, pkg string, notify func(int)) error {
		return runSDKPackageInstall(ctx, root, pkg, notify)
	})
}

func (m Manager) installSDKRuntime(ctx context.Context, notify func(int), install func(context.Context, string, string, func(int)) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.SDK == "" {
		return errors.New("Android SDK 路径为空")
	}
	root, err := filepath.Abs(m.SDK)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return errors.New("Android SDK 目录不存在")
	}
	missing, err := missingSDKRuntime(root)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if notify != nil {
			notify(100)
		}
		return nil
	}
	unlock, err := lockSDKInstall(ctx, root)
	if err != nil {
		return fmt.Errorf("无法锁定 SDK 安装：%w", err)
	}
	defer unlock()
	missing, err = missingSDKRuntime(root)
	if err != nil {
		return err
	}
	for index, pkg := range missing {
		if err := ctx.Err(); err != nil {
			return err
		}
		var step func(int)
		if notify != nil {
			step = func(n int) {
				if n >= 0 && n <= 100 {
					notify((index*100 + n) / len(missing))
				}
			}
		}
		if err := install(ctx, root, pkg.id, step); err != nil {
			return err
		}
		if !exists(filepath.Join(root, pkg.directory, pkg.executable)) {
			return fmt.Errorf("SDK Manager 未生成 %s", pkg.executable)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if notify != nil {
		notify(100)
	}
	return nil
}

func missingSDKRuntime(root string) ([]sdkRuntimePackage, error) {
	var missing []sdkRuntimePackage
	for _, pkg := range sdkRuntimePackages {
		dir := filepath.Join(root, pkg.directory)
		if exists(filepath.Join(dir, pkg.executable)) {
			continue
		}
		present, err := existsOrLink(dir)
		if err != nil {
			return nil, err
		}
		if present {
			return nil, fmt.Errorf("SDK 的 %s 目录已存在但缺少 %s；请检查，未覆盖", pkg.directory, pkg.executable)
		}
		missing = append(missing, pkg)
	}
	return missing, nil
}

func runSDKPackageInstall(ctx context.Context, root, pkg string, notify func(int)) error {
	if pkg != "platform-tools" && pkg != "emulator" && pkg != tapTapBuildToolsPackage {
		return errors.New("不支持的 SDK 运行组件")
	}
	java, err := sdkManagerJava()
	if err != nil {
		return err
	}
	tools, classpath, err := sdkManagerTools(root)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, java, "-Dcom.android.sdklib.toolsdir="+tools, "-classpath", classpath, "com.android.sdklib.tool.sdkmanager.SdkManagerCli", "--sdk_root="+root, "--install", pkg, "--channel=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader("")
	progress := &sdkProgress{last: -1, notify: notify}
	cmd.Stdout, cmd.Stderr = progress, progress
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if progress.licenseIssue {
			return errors.New("SDK 许可证尚未接受，请在 Android Studio 的 SDK Manager 中查看并接受后重试")
		}
		return fmt.Errorf("安装 Android %s 组件失败：%w", pkg, err)
	}
	if progress.licenseIssue {
		return errors.New("SDK 许可证尚未接受，请在 Android Studio 的 SDK Manager 中查看并接受后重试")
	}
	return nil
}
