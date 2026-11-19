package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

// viewerGeometry mirrors MuMu's customer_config.setting.player.window_landscape:
// the last position/size of an instance's viewer window is persisted per AVD
// and restored the next time the window opens.
type viewerGeometry struct {
	Left      int  `json:"left"`
	Top       int  `json:"top"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Monitor   int  `json:"monitor,omitempty"`
	Maximized bool `json:"maximized,omitempty"`
}

func viewerGeometryPath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `\/:*?"<>|`) {
		return "", errors.New("实例名称无效")
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "avd")
	return filepath.Join(root, name+".avd", "neko-viewer.json"), nil
}

func loadViewerGeometry(name string) (viewerGeometry, bool) {
	path, err := viewerGeometryPath(name)
	if err != nil {
		return viewerGeometry{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 4096 {
		return viewerGeometry{}, false
	}
	var g viewerGeometry
	if err := json.Unmarshal(b, &g); err != nil {
		return viewerGeometry{}, false
	}
	if g.Width < 320 || g.Height < 240 || g.Width > 8192 || g.Height > 8192 {
		return viewerGeometry{}, false
	}
	return g, true
}

func saveViewerGeometry(name string, g viewerGeometry) error {
	path, err := viewerGeometryPath(name)
	if err != nil {
		return err
	}
	if g.Width < 320 || g.Height < 240 || g.Width > 8192 || g.Height > 8192 {
		return errors.New("窗口尺寸无效")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(g)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".viewer-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// defaultViewerGeometry matches MuMu's default landscape viewer size.
func defaultViewerGeometry() viewerGeometry {
	return viewerGeometry{Width: 1707, Height: 1000}
}

// ensureDefaultInstanceGeometry seeds the default instance with MuMu-like
// dimensions when no saved geometry exists.
func ensureDefaultInstanceGeometry(name string) {
	if _, ok := loadViewerGeometry(name); ok {
		return
	}
	g := defaultViewerGeometry()
	if !strings.EqualFold(name, avd.DefaultName) {
		return
	}
	_ = saveViewerGeometry(name, g)
}
