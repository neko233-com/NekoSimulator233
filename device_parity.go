package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

const (
	deviceSortName   = "名称"
	deviceSortState  = "状态"
	deviceSortSystem = "系统版本"
)

// androidLabel converts an Android API level to the marketing version shown
// on MuMu device cards.
func androidLabel(api int) string {
	switch api {
	case 30:
		return "Android 11"
	case 34:
		return "Android 14"
	case 35:
		return "Android 15"
	case 0:
		return "Android 设备"
	default:
		return fmt.Sprintf("Android API %d", api)
	}
}

// visibleDevices mirrors MuMu's search + sort toolbar over the project-owned
// instance list. The returned slice is a new sorted/filtered copy.
func visibleDevices(items []avd.InstanceSummary, search, sortMode string) []avd.InstanceSummary {
	out := make([]avd.InstanceSummary, 0, len(items))
	needle := strings.ToLower(strings.TrimSpace(search))
	for _, item := range items {
		if needle != "" && !strings.Contains(strings.ToLower(item.Name), needle) &&
			!strings.Contains(strings.ToLower(androidLabel(item.API)), needle) &&
			!strings.Contains(strings.ToLower(item.State), needle) {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		switch sortMode {
		case deviceSortState:
			return out[i].State < out[j].State || (out[i].State == out[j].State && strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name))
		case deviceSortSystem:
			return out[i].API > out[j].API || (out[i].API == out[j].API && strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name))
		default:
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		}
	})
	return out
}

// sharedFolder returns the per-instance host shared folder, mirroring MuMu's
// "MuMu共享文件夹". It never follows links and creates the directory only when
// explicitly opened by the user.
func sharedFolder(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `\/:*?"<>|`) {
		return "", fmt.Errorf("实例名称无效")
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "shared")
	dir := filepath.Join(root, name)
	return dir, nil
}

func openSharedFolder(name string) error {
	dir, err := sharedFolder(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return exec.Command("explorer.exe", dir).Start()
}
