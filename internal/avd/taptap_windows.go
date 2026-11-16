package avd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/neko233-com/NekoSimulator233/internal/provision"
)

const tapTapPackage = "com.taptap"

type TapTapInstallResult struct {
	AlreadyInstalled bool   `json:"already_installed"`
	DownloadedBytes  int64  `json:"downloaded_bytes,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
}

type tapTapSetupOps struct {
	installed func(context.Context) (bool, error)
	download  func(context.Context, io.Writer) (provision.DownloadResult, error)
	verify    func(context.Context, string) error
	install   func(context.Context, string) error
}

// InstallTapTap downloads the official APK into a temporary file, validates
// the package name and installs it without replacing an existing application.
func (m Manager) InstallTapTap(ctx context.Context, instance Instance) (TapTapInstallResult, error) {
	result, err := installTapTap(ctx, tapTapSetupOps{
		installed: func(ctx context.Context) (bool, error) {
			var installed bool
			err := m.withCurrentInstance(ctx, instance, func(ctx context.Context, current Instance) error {
				var err error
				installed, err = m.hasTapTap(ctx, current)
				return err
			})
			return installed, err
		},
		download: provision.DownloadTapTap,
		verify:   m.verifyTapTapAPK,
		install: func(ctx context.Context, path string) error {
			return m.withCurrentInstance(ctx, instance, func(ctx context.Context, current Instance) error {
				installed, err := m.hasTapTap(ctx, current)
				if err != nil || installed {
					return err
				}
				return m.installAPKsUnchecked(ctx, current, []string{path}, false)
			})
		},
	})
	if err != nil {
		return result, err
	}
	if err := m.markTapTapAutoDone(); err != nil {
		return result, fmt.Errorf("TapTap 已安装，但无法记录首次准备状态：%w", err)
	}
	return result, nil
}

func installTapTap(ctx context.Context, ops tapTapSetupOps) (TapTapInstallResult, error) {
	installed, err := ops.installed(ctx)
	if err != nil {
		return TapTapInstallResult{}, err
	}
	if installed {
		return TapTapInstallResult{AlreadyInstalled: true}, nil
	}
	f, err := os.CreateTemp("", "neko-taptap-*.apk")
	if err != nil {
		return TapTapInstallResult{}, fmt.Errorf("无法创建临时安装包：%w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	result, err := ops.download(ctx, f)
	closeErr := f.Close()
	if err != nil {
		return TapTapInstallResult{}, err
	}
	if closeErr != nil {
		return TapTapInstallResult{}, fmt.Errorf("保存 TapTap 安装包失败：%w", closeErr)
	}
	if err := ops.verify(ctx, path); err != nil {
		return TapTapInstallResult{}, err
	}
	if err := ops.install(ctx, path); err != nil {
		return TapTapInstallResult{}, err
	}
	installed, err = ops.installed(ctx)
	if err != nil {
		return TapTapInstallResult{}, fmt.Errorf("安装后检查 TapTap 失败：%w", err)
	}
	if !installed {
		return TapTapInstallResult{}, errors.New("安装后未检测到 TapTap")
	}
	return TapTapInstallResult{DownloadedBytes: result.Bytes, SHA256: result.SHA256}, nil
}

func (m Manager) hasTapTap(ctx context.Context, instance Instance) (bool, error) {
	out, err := m.adb(ctx, instance.Serial, "shell", "pm", "list", "packages", tapTapPackage)
	if err != nil {
		return false, fmt.Errorf("检查 TapTap 安装状态失败：%w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "package:"+tapTapPackage {
			return true, nil
		}
	}
	return false, nil
}

func (m Manager) verifyTapTapAPK(ctx context.Context, path string) error {
	entries, err := os.ReadDir(filepath.Join(m.SDK, "build-tools"))
	if err != nil {
		return errors.New("缺少 Android SDK Build Tools，无法检查 TapTap 安装包")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		aapt := filepath.Join(m.SDK, "build-tools", entry.Name(), "aapt.exe")
		if !exists(aapt) {
			continue
		}
		cmd := exec.CommandContext(ctx, aapt, "dump", "badging", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("无法检查 TapTap 安装包：%w", err)
		}
		if !isTapTapBadging(out) {
			return errors.New("下载的安装包不是预期的 TapTap 应用")
		}
		return nil
	}
	return errors.New("缺少 Android SDK Build Tools 的 aapt.exe，无法检查 TapTap 安装包")
}

func isTapTapBadging(out []byte) bool {
	first, _, _ := strings.Cut(string(out), "\n")
	return strings.HasPrefix(first, "package: name='"+tapTapPackage+"' ")
}
