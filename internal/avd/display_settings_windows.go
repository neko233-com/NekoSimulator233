package avd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

type DisplaySettings struct {
	Width   int `json:"width" jsonschema:"Guest display width in pixels, 320 to 8192"`
	Height  int `json:"height" jsonschema:"Guest display height in pixels, 320 to 8192"`
	Density int `json:"density" jsonschema:"Guest display density in dpi, 72 to 1000"`
}

func (d DisplaySettings) validate() error {
	if d.Width < 320 || d.Height < 320 || d.Width > 8192 || d.Height > 8192 || int64(d.Width)*int64(d.Height) > 32*1024*1024 || d.Density < 72 || d.Density > 1000 {
		return errors.New("分辨率须为 320–8192 像素且不超过 33554432 像素，DPI 须为 72–1000")
	}
	return nil
}

func displayFromConfig(data []byte) (DisplaySettings, error) {
	d := DisplaySettings{Width: 720, Height: 1280, Density: 240}
	fields := map[string]*int{"hw.lcd.width": &d.Width, "hw.lcd.height": &d.Height, "hw.lcd.density": &d.Density}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if target := fields[strings.TrimSpace(key)]; ok && target != nil {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return DisplaySettings{}, fmt.Errorf("实例显示配置 %s 无效", strings.TrimSpace(key))
			}
			*target = n
		}
	}
	if err := scanner.Err(); err != nil {
		return DisplaySettings{}, err
	}
	return d, d.validate()
}

func (m Manager) readDisplayConfig() (string, []byte, error) {
	dir, err := ownedAVD(m)
	if err != nil {
		return "", nil, err
	}
	config := filepath.Join(dir, "config.ini")
	f, err := openOfflineImage(config)
	if err != nil {
		return "", nil, fmt.Errorf("无法读取实例显示配置：%w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		return "", nil, errors.New("实例配置无法读取或过大")
	}
	return config, data, nil
}

func (m Manager) GetDisplaySettings(ctx context.Context) (DisplaySettings, error) {
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return DisplaySettings{}, err
	}
	defer unlock()
	_, data, err := m.readDisplayConfig()
	if err != nil {
		return DisplaySettings{}, err
	}
	return displayFromConfig(data)
}

func (m Manager) SetDisplaySettings(ctx context.Context, settings DisplaySettings) error {
	if err := settings.validate(); err != nil {
		return err
	}
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := m.discover(); err == nil {
		return errors.New("独立实例正在运行，请先关闭后修改显示设置")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	config, data, err := m.readDisplayConfig()
	if err != nil {
		return err
	}
	if _, err := displayFromConfig(data); err != nil {
		return err
	}
	updates := map[string]string{"hw.lcd.width": strconv.Itoa(settings.Width), "hw.lcd.height": strconv.Itoa(settings.Height), "hw.lcd.density": strconv.Itoa(settings.Density)}
	foundKeys := make(map[string]bool, len(updates))
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		key = strings.TrimSpace(key)
		if value, found := updates[key]; ok && found {
			ending := ""
			if strings.HasSuffix(line, "\r") {
				ending = "\r"
			}
			lines[i] = key + "=" + value + ending
			foundKeys[key] = true
		}
	}
	// Append missing keys in deterministic order, keeping the existing newline style.
	ending := ""
	if strings.Contains(string(data), "\r\n") {
		ending = "\r"
	}
	for _, key := range []string{"hw.lcd.width", "hw.lcd.height", "hw.lcd.density"} {
		if value, ok := updates[key]; ok && !foundKeys[key] {
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lines[len(lines)-1] = key + "=" + value + ending
				lines = append(lines, "")
			} else {
				lines = append(lines, key+"="+value+ending)
			}
		}
	}
	updated := strings.Join(lines, "\n")
	if updated == string(data) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(config), ".display-config-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.WriteString(updated); err != nil {
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
	if err = ctx.Err(); err != nil {
		return err
	}
	src, err := windows.UTF16PtrFromString(temp)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(config)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
