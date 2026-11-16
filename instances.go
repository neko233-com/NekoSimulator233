package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

const (
	systemAuto      = "自动（Android 14/15）"
	systemAndroid11 = "Android 11（32 位兼容）"
)

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

func (a *app) selectedStatus() string {
	for _, item := range a.available {
		if strings.EqualFold(item.Name, a.selectedName()) {
			prefix := ""
			if item.API != 0 {
				prefix = fmt.Sprintf("Android API %d · ", item.API)
			}
			switch item.ImageTag {
			case "google_apis_playstore":
				prefix += "Google Play 镜像 · "
			case "google_apis":
				prefix += "Google APIs 镜像（默认不含 Play 商店） · "
			}
			switch item.State {
			case "not_created":
				return prefix + "上次检查：实例尚未创建"
			case "stopped":
				return prefix + "上次检查：实例已停止"
			case "running":
				return prefix + "上次检查：实例进程运行中"
			case "unknown":
				return prefix + "当前实例：状态未知 · " + item.Reason
			}
		}
	}
	return ""
}

func (a *app) markInstanceState(name, state string) {
	for i := range a.available {
		if strings.EqualFold(a.available[i].Name, name) {
			a.available[i].State, a.available[i].Reason = state, ""
			return
		}
	}
}

func (a *app) instanceChoices() []string {
	names := make([]string, 0, len(a.available)+1)
	for _, item := range a.available {
		if item.State != "unknown" {
			names = append(names, item.Name)
		}
	}
	if len(names) == 0 {
		return []string{avd.DefaultName}
	}
	return names
}

func (a *app) setAvailable(items []avd.InstanceSummary) {
	a.available = items
	choices := a.instanceChoices()
	for _, name := range choices {
		if strings.EqualFold(name, a.selectedName()) {
			a.selected = name
			return
		}
	}
	a.selected = choices[0]
}

func (a *app) createIndependent() {
	if a.busy {
		return
	}
	name := strings.TrimSpace(a.newInstance)
	api, err := newSystemAPI(a.newSystem)
	if err != nil {
		a.message = err.Error()
		return
	}
	a.busy = true
	a.message = "正在创建独立实例…"
	go func() {
		manager, err := avd.DiscoverManager()
		var items []avd.InstanceSummary
		created := false
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err = manager.CreateInstanceAPI(ctx, name, api)
			if err == nil {
				created = true
				items, err = manager.ListInstances(ctx)
			}
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if created {
				if err != nil {
					items = append(append([]avd.InstanceSummary(nil), a.available...), avd.InstanceSummary{Name: name, State: "stopped"})
				}
				a.selected = name
				a.setAvailable(items)
				a.newInstance = ""
				if err != nil {
					a.message = fmt.Sprintf("实例 %s 已创建；刷新列表失败：%v", name, err)
				} else {
					a.message = fmt.Sprintf("实例 %s 已创建，尚未启动", name)
				}
			} else if err != nil {
				a.message = "创建实例失败：" + err.Error()
			}
		})
	}()
}
