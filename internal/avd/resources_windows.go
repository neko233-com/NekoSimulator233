//go:build windows

package avd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
)

// Resources are startup overrides for the project-owned AVD, not host usage.
type Resources struct {
	Cores  int `json:"cores" jsonschema:"Android virtual CPU cores, 1 to 64"`
	RAMMiB int `json:"ram_mib" jsonschema:"Android guest RAM in MiB, 512 to 131072"`
}

func (r Resources) validate() error {
	if r.Cores < 1 || r.Cores > 64 || r.RAMMiB < 512 || r.RAMMiB > 131072 {
		return errors.New("CPU 核数须为 1–64，内存须为 512–131072 MiB")
	}
	return nil
}

func resourcesOverride(dir string) (*Resources, error) {
	path := filepath.Join(dir, "neko-resources.json")
	f, err := openOfflineImage(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取资源设置失败：%w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return nil, errors.New("资源设置文件无法读取或过大")
	}
	var r Resources
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&r); err != nil {
		return nil, fmt.Errorf("资源设置文件无效：%w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("资源设置文件包含额外内容")
	}
	if err = r.validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// GetResources reads the override or the existing AVD configuration without
// initializing an instance. Lifecycle locking coordinates with start/import.
func (m Manager) GetResources(ctx context.Context) (Resources, error) {
	defaults := Resources{Cores: 2, RAMMiB: 4096}
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return Resources{}, err
	}
	defer unlock()
	dir := filepath.Join(m.Home, m.Name+".avd")
	_, iniErr := os.Stat(filepath.Join(m.Home, m.Name+".ini"))
	_, dirErr := os.Stat(dir)
	if errors.Is(iniErr, os.ErrNotExist) && errors.Is(dirErr, os.ErrNotExist) {
		return defaults, nil
	}
	if _, err = ownedAVD(m); err != nil {
		return Resources{}, err
	}
	info, err := os.Stat(filepath.Join(dir, "config.ini"))
	if err != nil || !info.Mode().IsRegular() {
		return Resources{}, errors.New("实例配置不完整，无法读取资源设置")
	}
	if override, err := resourcesOverride(dir); err != nil {
		return Resources{}, err
	} else if override != nil {
		return *override, nil
	}
	values, err := parseINI(filepath.Join(dir, "config.ini"))
	if err != nil {
		return Resources{}, err
	}
	for key, target := range map[string]*int{"hw.cpu.ncore": &defaults.Cores, "hw.ramSize": &defaults.RAMMiB} {
		if values[key] != "" {
			*target, err = strconv.Atoi(values[key])
			if err != nil {
				return Resources{}, fmt.Errorf("实例配置 %s 无效", key)
			}
		}
	}
	return defaults, defaults.validate()
}

// SetResources never changes a running VM or its config/disk files. The
// override is replaced atomically and used only on the next startup.
func (m Manager) SetResources(ctx context.Context, r Resources) error {
	if err := r.validate(); err != nil {
		return err
	}
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err = m.discover(); err == nil {
		return errors.New("独立实例正在运行，请先关闭后修改资源设置")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	if err = m.ensureWithImagePolicy(0, true); err != nil {
		return err
	}
	dir, err := ownedAVD(m)
	if err != nil {
		return err
	}
	ptr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(ptr)
	if err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("实例目录不可用或是链接，未修改设置")
	}
	// Reject a malformed or linked previous override instead of overwriting it.
	if _, err = resourcesOverride(dir); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".resources-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(data, '\n')); err != nil {
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
	dst, err := windows.UTF16PtrFromString(filepath.Join(dir, "neko-resources.json"))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
