//go:build windows

package avd

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PrepareBundledRuntime extracts the optional MSI payload once, without
// modifying an existing runtime or any instance data.
func PrepareBundledRuntime(ctx context.Context, archive, local string) error {
	if _, err := os.Stat(archive); errors.Is(err, os.ErrNotExist) {
		return nil // A development or older installation has no bundle.
	} else if err != nil {
		return err
	}
	if local == "" {
		return errors.New("未找到 LOCALAPPDATA")
	}
	root := filepath.Join(local, "NekoSimulator233-data", "runtime")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if !noReparse(root) {
		return errors.New("运行环境目录包含链接")
	}
	unlock, err := lockSDKInstall(ctx, root)
	if err != nil {
		return err
	}
	defer unlock()
	dest := filepath.Join(root, "sdk")
	if _, err := os.Lstat(dest); err == nil {
		if !noReparse(dest) || !exists(filepath.Join(dest, "emulator", "emulator.exe")) || !exists(filepath.Join(dest, "platform-tools", "adb.exe")) {
			return errors.New("现有运行环境不完整，已保留原文件")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("无法读取内置运行环境：%w", err)
	}
	defer z.Close()
	stage, err := os.MkdirTemp(root, ".runtime-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage) // MkdirTemp created this directory directly inside root.
	seen := make(map[string]bool)
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" || strings.ContainsAny(name, "\\:") || !filepath.IsLocal(filepath.FromSlash(name)) || f.Mode()&os.ModeSymlink != 0 {
			return errors.New("内置运行环境包含无效路径")
		}
		for _, part := range strings.Split(name, "/") {
			if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return errors.New("内置运行环境包含无效路径")
			}
		}
		key := strings.ToLower(name)
		if seen[key] {
			return errors.New("内置运行环境包含重复路径")
		}
		seen[key] = true
		target := filepath.Join(stage, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			return errors.New("内置运行环境包含无效文件")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, &contextReader{ctx: ctx, r: in})
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if !exists(filepath.Join(stage, "emulator", "emulator.exe")) || !exists(filepath.Join(stage, "platform-tools", "adb.exe")) || !exists(filepath.Join(stage, "build-tools", "35.0.0", "aapt.exe")) || !completePlayImage(filepath.Join(stage, "system-images", "android-34", "google_apis_playstore", "x86_64"), 34) {
		return errors.New("内置 Android 运行环境不完整")
	}
	return os.Rename(stage, dest)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
