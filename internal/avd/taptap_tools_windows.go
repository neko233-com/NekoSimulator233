package avd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (m Manager) installTapTapBuildTools(ctx context.Context) error {
	return m.prepareTapTapBuildTools(ctx, runSDKPackageInstall)
}

// prepareTapTapBuildTools never replaces an existing incomplete version.
// The SDK lock is shared with image and runtime package installation.
func (m Manager) prepareTapTapBuildTools(ctx context.Context, install func(context.Context, string, string, func(int)) error) error {
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
	if aapt, err := findAAPT(root); err != nil || aapt != "" {
		return err
	}
	unlock, err := lockSDKInstall(ctx, root)
	if err != nil {
		return fmt.Errorf("无法锁定 SDK 安装：%w", err)
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if aapt, err := findAAPT(root); err != nil || aapt != "" {
		return err
	}
	target := filepath.Join(root, "build-tools", "35.0.0")
	present, err := existsOrLink(target)
	if err != nil {
		return err
	}
	if present {
		return errors.New("SDK Build Tools 35.0.0 目录已存在但缺少 aapt.exe；请检查，未覆盖")
	}
	if err := install(ctx, root, tapTapBuildToolsPackage, nil); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !exists(filepath.Join(target, "aapt.exe")) {
		return errors.New("SDK Manager 未生成 Build Tools 的 aapt.exe")
	}
	return nil
}
