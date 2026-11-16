package avd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/sys/windows"
)

type ImportedFile struct {
	GuestPath string `json:"guest_path"`
}

type transferADB func(context.Context, ...string) ([]byte, error)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func validGuestFilePath(p string) bool {
	if !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func importFile(ctx context.Context, adb transferADB, local string, nonce []byte) (ImportedFile, error) {
	if !filepath.IsAbs(local) {
		return ImportedFile{}, errors.New("请选择绝对路径的本机文件")
	}
	info, err := os.Stat(local)
	if err != nil || !info.Mode().IsRegular() {
		return ImportedFile{}, errors.New("本机文件不存在或不是普通文件")
	}
	if len(nonce) != 16 {
		return ImportedFile{}, errors.New("无法创建文件传输目录")
	}
	base := "/sdcard/Download/NekoSimulator233"
	dir := base + "/" + hex.EncodeToString(nonce)
	name := filepath.Base(local)
	final := dir + "/" + name
	if !validGuestFilePath(final) {
		return ImportedFile{}, errors.New("文件名不能用于 Android 路径")
	}
	if _, err := adb(ctx, "shell", "mkdir", "-p", base); err != nil {
		return ImportedFile{}, fmt.Errorf("创建 Android 下载目录失败：%w", err)
	}
	if _, err := adb(ctx, "shell", "mkdir", dir); err != nil {
		return ImportedFile{}, fmt.Errorf("创建 Android 传输目录失败：%w", err)
	}
	temp := dir + "/.neko-transfer.part"
	if temp == final {
		temp = dir + "/.neko-transfer-2.part"
	}
	if _, err := adb(ctx, "push", local, temp); err != nil {
		return ImportedFile{}, fmt.Errorf("导入文件失败，Android 临时文件可能保留在 %s：%w", dir, err)
	}
	if err := ctx.Err(); err != nil {
		return ImportedFile{}, fmt.Errorf("导入已取消，Android 临时文件可能保留在 %s：%w", dir, err)
	}
	if _, err := adb(ctx, "shell", "mv", shellQuote(temp), shellQuote(final)); err != nil {
		return ImportedFile{}, fmt.Errorf("完成文件导入失败，Android 临时文件保留在 %s：%w", dir, err)
	}
	return ImportedFile{GuestPath: final}, nil
}

func (m Manager) ImportFile(ctx context.Context, instance Instance, local string) (result ImportedFile, err error) {
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return result, errors.New("无法创建文件传输目录")
	}
	err = m.withCurrentInstanceGuard(ctx, instance, func(ctx context.Context, current Instance) error {
		result, err = importFile(ctx, func(ctx context.Context, args ...string) ([]byte, error) { return m.adb(ctx, current.Serial, args...) }, local, nonce[:])
		return err
	})
	return result, err
}

func exportFile(ctx context.Context, adb transferADB, guest, destination string) error {
	if !validGuestFilePath(guest) {
		return errors.New("Android 文件路径必须是规范的绝对路径")
	}
	if !filepath.IsAbs(destination) {
		return errors.New("本机目标路径必须是绝对路径")
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("本机目标已存在，未覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := adb(ctx, "shell", "test", "-f", shellQuote(guest)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Android 文件不存在或不是普通文件")
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".neko-transfer-*.tmp")
	if err != nil {
		return fmt.Errorf("无法创建本机临时文件：%w", err)
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := adb(ctx, "pull", guest, temp); err != nil {
		return fmt.Errorf("导出 Android 文件失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(temp)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("导出的临时文件无效")
	}
	from, err := windows.UTF16PtrFromString(temp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if err := windows.MoveFile(from, to); err != nil {
		return fmt.Errorf("无法保存文件，目标可能已存在：%w", err)
	}
	return nil
}

func (m Manager) ExportFile(ctx context.Context, instance Instance, guest, destination string) error {
	return m.withCurrentInstanceGuard(ctx, instance, func(ctx context.Context, current Instance) error {
		return exportFile(ctx, func(ctx context.Context, args ...string) ([]byte, error) { return m.adb(ctx, current.Serial, args...) }, guest, destination)
	})
}
