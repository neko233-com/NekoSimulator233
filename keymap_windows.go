package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/display"
	"golang.org/x/sys/windows"
)

type mappedPoint struct{ X, Y float64 }
type joystickConfig struct {
	Center mappedPoint `json:"center"`
	Radius float64     `json:"radius"`
}
type keymapFile struct {
	Version  int                    `json:"version"`
	Keys     map[uint16]mappedPoint `json:"keys"`
	Joystick *joystickConfig        `json:"joystick,omitempty"`
}
type keymapStore struct {
	path     string
	revision [32]byte
	exists   bool
	joystick *joystickConfig
}

// USB HID keyboard usage IDs for the fixed WASD movement cluster.
func joystickKey(k uint16) bool { return k == 0x1a || k == 0x04 || k == 0x16 || k == 0x07 }

func keymapPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "keymap.json")
}

func keymapPathForInstance(name string) string {
	if strings.EqualFold(name, avd.DefaultName) {
		return keymapPath()
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "keymaps", strings.ToLower(name)+".json")
}

func (s *keymapStore) read() (map[uint16]mappedPoint, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.exists = false
		s.joystick = nil
		return make(map[uint16]mappedPoint), nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 64<<10 {
		return nil, errors.New("键位文件过大")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var data keymapFile
	if err = dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("读取键位失败: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("键位文件存在多余内容")
	}
	if (data.Version != 1 && data.Version != 2) || (data.Version == 1 && data.Joystick != nil) || len(data.Keys) > 128 {
		return nil, errors.New("不支持的键位文件")
	}
	for key, p := range data.Keys {
		if !display.ValidKeyboardUsage(key) || p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
			return nil, errors.New("键位文件包含无效的坐标或按键")
		}
		if data.Joystick != nil && joystickKey(key) {
			return nil, errors.New("WASD 同时被方向摇杆和单点键位占用")
		}
	}
	if p := data.Joystick; p != nil && (p.Center.X < 0 || p.Center.X > 1 || p.Center.Y < 0 || p.Center.Y > 1 || p.Radius < 0.04 || p.Radius > 0.25) {
		return nil, errors.New("方向摇杆的坐标或半径无效")
	}
	if data.Keys == nil {
		data.Keys = make(map[uint16]mappedPoint)
	}
	s.exists = true
	s.revision = sha256.Sum256(b)
	s.joystick = data.Joystick
	return data.Keys, nil
}

func (s *keymapStore) save(keys map[uint16]mappedPoint) error {
	return s.saveWithJoystick(keys, s.joystick)
}

func (s *keymapStore) saveWithJoystick(keys map[uint16]mappedPoint, stick *joystickConfig) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	// Windows mutex ownership is thread-affine; keep acquisition and release on
	// the same OS thread even if a syscall causes the goroutine to reschedule.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, _ := windows.UTF16PtrFromString(`Local\NekoSimulator233-keymap`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(mutex)
	wait, err := windows.WaitForSingleObject(mutex, 0)
	if err != nil || (wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED) {
		return errors.New("另一个窗口正在保存键位，请重试")
	}
	defer windows.ReleaseMutex(mutex)
	current, err := os.ReadFile(s.path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists != s.exists || (exists && sha256.Sum256(current) != s.revision) {
		return errors.New("键位已被其他窗口修改，请重新打开画面后再编辑")
	}
	version := 1
	if stick != nil {
		version = 2
	}
	b, err := json.MarshalIndent(keymapFile{Version: version, Keys: keys, Joystick: stick}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".keymap-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	from, _ := windows.UTF16PtrFromString(f.Name())
	to, _ := windows.UTF16PtrFromString(s.path)
	if err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	s.exists = true
	s.revision = sha256.Sum256(b)
	if stick == nil {
		s.joystick = nil
	} else {
		copy := *stick
		s.joystick = &copy
	}
	return nil
}
