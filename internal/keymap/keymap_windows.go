//go:build windows

// Package keymap defines the shared on-disk keymap format and paths used by
// both the viewer and offline instance backup.
package keymap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"golang.org/x/sys/windows"
)

const MaxFileSize = 64 << 10
const EmbeddedDirectory = "neko-keymaps"
const DefaultProfile = "默认"

type Point struct{ X, Y float64 }
type Joystick struct {
	Center Point   `json:"center"`
	Radius float64 `json:"radius"`
}
type File struct {
	Version  int              `json:"version"`
	Keys     map[uint16]Point `json:"keys"`
	Joystick *Joystick        `json:"joystick,omitempty"`
}

func JoystickKey(k uint16) bool { return k == 0x1a || k == 0x04 || k == 0x16 || k == 0x07 }

func Parse(data []byte) (File, error) {
	if len(data) > MaxFileSize {
		return File{}, errors.New("键位文件过大")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result File
	if err := decoder.Decode(&result); err != nil {
		return File{}, fmt.Errorf("读取键位失败: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return File{}, errors.New("键位文件存在多余内容")
	}
	if (result.Version != 1 && result.Version != 2) || (result.Version == 1 && result.Joystick != nil) || len(result.Keys) > 128 {
		return File{}, errors.New("不支持的键位文件")
	}
	for key, p := range result.Keys {
		if !display.ValidKeyboardUsage(key) || p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
			return File{}, errors.New("键位文件包含无效的坐标或按键")
		}
		if result.Joystick != nil && JoystickKey(key) {
			return File{}, errors.New("WASD 同时被方向摇杆和单点键位占用")
		}
	}
	if p := result.Joystick; p != nil && (p.Center.X < 0 || p.Center.X > 1 || p.Center.Y < 0 || p.Center.Y > 1 || p.Radius < 0.04 || p.Radius > 0.25) {
		return File{}, errors.New("方向摇杆的坐标或半径无效")
	}
	if result.Keys == nil {
		result.Keys = make(map[uint16]Point)
	}
	return result, nil
}

func ValidateProfileName(name string) error {
	if name == "" || name == DefaultProfile || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") || !filepath.IsLocal(name) || strings.ContainsAny(name, `\/:*?"<>|`) {
		return errors.New("方案名称无效或与默认方案冲突")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("方案名称不能包含控制字符")
		}
	}
	return nil
}

func ProfilesDir(base string) string {
	return filepath.Join(filepath.Dir(base), ".profiles", strings.TrimSuffix(filepath.Base(base), filepath.Ext(base)))
}

// Lock coordinates snapshots with GUI keymap saves across processes. Windows
// mutex ownership is thread-affine, so callers must release on the same goroutine.
func Lock() (func(), error) {
	runtime.LockOSThread()
	name, err := windows.UTF16PtrFromString(`Local\NekoSimulator233-keymap`)
	if err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	mutex, err := windows.CreateMutex(nil, false, name)
	// CreateMutex returns a valid handle together with ERROR_ALREADY_EXISTS
	// when another process or thread already opened the named mutex.
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if mutex != 0 {
			_ = windows.CloseHandle(mutex)
		}
		runtime.UnlockOSThread()
		return nil, err
	}
	wait, err := windows.WaitForSingleObject(mutex, 0)
	if err != nil || (wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED) {
		windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
		return nil, errors.New("另一个窗口正在保存键位，请重试")
	}
	return func() {
		_ = windows.ReleaseMutex(mutex)
		_ = windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
	}, nil
}

func LegacyPath(dataRoot, name, defaultName string) string {
	if strings.EqualFold(name, defaultName) {
		return filepath.Join(dataRoot, "keymap.json")
	}
	return filepath.Join(dataRoot, "keymaps", strings.ToLower(name)+".json")
}

// Resolve selects the per-AVD layout for a restored instance and otherwise
// keeps using the legacy path. A linked marker is rejected, never followed.
func Resolve(dataRoot, avdDir, name, defaultName string) (string, error) {
	dir := filepath.Join(avdDir, EmbeddedDirectory)
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	attrs, err := windows.GetFileAttributes(p)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return LegacyPath(dataRoot, name, defaultName), nil
	}
	if err != nil {
		return "", err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return "", errors.New("实例键位目录无效或包含链接")
	}
	return filepath.Join(dir, "default.json"), nil
}
