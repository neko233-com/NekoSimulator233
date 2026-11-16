package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/keymap"
	"golang.org/x/sys/windows"
)

type mappedPoint = keymap.Point
type joystickConfig = keymap.Joystick
type keymapFile = keymap.File
type keymapStore struct {
	path     string
	revision [32]byte
	exists   bool
	joystick *joystickConfig
}

// USB HID keyboard usage IDs for the fixed WASD movement cluster.
func joystickKey(k uint16) bool { return keymap.JoystickKey(k) }

func keymapPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "keymap.json")
}

func keymapPathForInstance(name string) string {
	return keymap.LegacyPath(filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data"), name, avd.DefaultName)
}

func resolveKeymapPathForInstance(name string) (string, error) {
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data")
	return keymap.Resolve(root, filepath.Join(root, "avd", name+".avd"), name, avd.DefaultName)
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
	data, err := keymap.Parse(b)
	if err != nil {
		return nil, err
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
	unlock, err := keymap.Lock()
	if err != nil {
		return err
	}
	defer unlock()
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
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if exists {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if err = windows.MoveFileEx(from, to, flags); err != nil {
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
