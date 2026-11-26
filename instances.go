package main

import (
	"fmt"
	"strings"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

const (
	systemAuto      = "自动（Android 14/15）"
	systemAndroid11 = "Android 11（32 位兼容）"
)

func independentViewerKey(name string) string {
	return "independent:" + strings.ToLower(name)
}

func selectedManager(name string) (avd.Manager, error) {
	m, err := avd.DiscoverManager()
	if err != nil {
		return m, err
	}
	return m.WithName(name)
}

func newSystemAPI(choice string) (int, error) {
	switch choice {
	case "", systemAuto:
		return 0, nil
	case systemAndroid11:
		return 30, nil
	default:
		return 0, fmt.Errorf("不支持的 Android 系统选择")
	}
}
