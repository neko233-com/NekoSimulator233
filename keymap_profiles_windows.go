//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/neko233-com/NekoSimulator233/internal/keymap"
	"golang.org/x/sys/windows"
)

const defaultKeymapProfile = keymap.DefaultProfile

func keymapProfilesDir(base string) string {
	return keymap.ProfilesDir(base)
}

func validateKeymapProfileName(name string) error {
	return keymap.ValidateProfileName(name)
}

func keymapProfilePath(base, name string) (string, error) {
	if name == defaultKeymapProfile {
		return base, nil
	}
	if err := validateKeymapProfileName(name); err != nil {
		return "", err
	}
	return filepath.Join(keymapProfilesDir(base), name+".json"), nil
}

func regularNonLink(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return err
	}
	if attrs&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return errors.New("键位方案不是普通文件")
	}
	return nil
}

func profileDirectory(path string) (bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return false, errors.New("键位方案目录无效或包含链接")
	}
	return true, nil
}

func ensureProfileDirectories(base string) error {
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		return err
	}
	dir := keymapProfilesDir(base)
	for _, path := range []string{filepath.Dir(dir), dir} {
		exists, err := profileDirectory(path)
		if err != nil {
			return err
		}
		if !exists {
			if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			if exists, err = profileDirectory(path); err != nil || !exists {
				return errors.New("无法安全创建键位方案目录")
			}
		}
	}
	return nil
}

func listKeymapProfiles(base string) ([]string, error) {
	result := []string{defaultKeymapProfile}
	dir := keymapProfilesDir(base)
	for _, path := range []string{filepath.Dir(dir), dir} {
		exists, err := profileDirectory(path)
		if err != nil {
			return nil, err
		}
		if !exists {
			return result, nil
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if validateKeymapProfileName(name) != nil || regularNonLink(filepath.Join(keymapProfilesDir(base), entry.Name())) != nil {
			continue
		}
		result = append(result, name)
	}
	sort.Slice(result[1:], func(i, j int) bool { return strings.ToLower(result[i+1]) < strings.ToLower(result[j+1]) })
	return result, nil
}

func (v *viewer) activeProfile() string {
	if v.profile == "" {
		return defaultKeymapProfile
	}
	return v.profile
}

func (v *viewer) keymapBase() string {
	if v.baseKeymap != "" {
		return v.baseKeymap
	}
	if v.keymap != nil {
		return v.keymap.path
	}
	return ""
}

func (v *viewer) profileChoices() []string {
	if len(v.profiles) == 0 {
		return []string{defaultKeymapProfile}
	}
	return v.profiles
}

func (v *viewer) switchKeymapProfile(name string) error {
	if v.keymap == nil {
		return errors.New("键位配置不可用")
	}
	base := v.keymapBase()
	path, err := keymapProfilePath(base, name)
	if err != nil {
		return err
	}
	if name != defaultKeymapProfile {
		if _, err = listKeymapProfiles(base); err != nil {
			return err
		}
		if err = regularNonLink(path); err != nil {
			return fmt.Errorf("无法打开键位方案：%w", err)
		}
	}
	next := &keymapStore{path: path}
	bindings, err := next.read()
	if err != nil {
		return err
	}
	v.releaseHostInput()
	v.baseKeymap = base
	v.keymap, v.bindings, v.joystick, v.profile = next, bindings, next.joystick, name
	v.pendingPoint = nil
	return nil
}

func (v *viewer) createKeymapProfile(name string) error {
	if v.keymap == nil {
		return errors.New("键位配置不可用")
	}
	name = strings.TrimSpace(name)
	if err := validateKeymapProfileName(name); err != nil {
		return err
	}
	base := v.keymapBase()
	if err := ensureProfileDirectories(base); err != nil {
		return err
	}
	path, err := keymapProfilePath(base, name)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("键位方案已存在，不会覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next := &keymapStore{path: path}
	if err := next.saveWithJoystick(v.bindings, v.joystick); err != nil {
		return err
	}
	profiles, err := listKeymapProfiles(base)
	if err != nil {
		return fmt.Errorf("方案已保存，但无法刷新列表：%w", err)
	}
	v.releaseHostInput()
	v.baseKeymap = base
	v.keymap, v.joystick, v.profile, v.profiles = next, next.joystick, name, profiles
	v.pendingPoint = nil
	return nil
}
