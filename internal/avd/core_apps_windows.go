package avd

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type CoreAppStatus struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	State   string `json:"state" jsonschema:"Package state for the current Android user: enabled, disabled, or missing"`
}

type CoreAppAction struct {
	Action string `json:"action" jsonschema:"opened or play_listing; a listing still requires user installation"`
}

var coreApps = []CoreAppStatus{
	{ID: "chrome", Package: "com.android.chrome"},
	{ID: "play", Package: "com.android.vending"},
	{ID: "play_services", Package: "com.google.android.gms"},
	{ID: "google_services_framework", Package: "com.google.android.gsf"},
	{ID: "taptap", Package: "com.taptap"},
	{ID: "wechat", Package: "com.tencent.mm"},
	{ID: "douyin", Package: "com.ss.android.ugc.aweme"},
}

type coreAppADB func(context.Context, ...string) ([]byte, error)

func queryCoreApps(ctx context.Context, adb coreAppADB) ([]CoreAppStatus, error) {
	states := make(map[string]string)
	for _, query := range []struct{ flag, state string }{{"-e", "enabled"}, {"-d", "disabled"}} {
		out, err := adb(ctx, "shell", "pm", "list", "packages", query.flag)
		if err != nil {
			return nil, fmt.Errorf("查询 Android 应用失败：%w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			name, ok := strings.CutPrefix(line, "package:")
			if !ok || (name != "android" && !packageName.MatchString(name)) {
				return nil, errors.New("Android 返回了无法识别的应用列表")
			}
			if prior, exists := states[name]; exists && prior != query.state {
				return nil, errors.New("Android 应用状态在查询期间发生变化，请重试")
			}
			states[name] = query.state
		}
	}
	result := make([]CoreAppStatus, len(coreApps))
	for i, app := range coreApps {
		app.State = states[app.Package]
		if app.State == "" {
			app.State = "missing"
		}
		result[i] = app
	}
	return result, nil
}

func (m Manager) CoreAppStatuses(ctx context.Context, instance Instance) (result []CoreAppStatus, err error) {
	err = m.withCurrentInstance(ctx, instance, func(ctx context.Context, current Instance) error {
		result, err = queryCoreApps(ctx, func(ctx context.Context, args ...string) ([]byte, error) {
			return m.adb(ctx, current.Serial, args...)
		})
		return err
	})
	return result, err
}

func openCoreApp(ctx context.Context, id string, adb coreAppADB) (CoreAppAction, error) {
	if id != "chrome" && id != "play" && id != "taptap" {
		return CoreAppAction{}, errors.New("不支持的快捷应用")
	}
	statuses, err := queryCoreApps(ctx, adb)
	if err != nil {
		return CoreAppAction{}, err
	}
	byID := make(map[string]CoreAppStatus, len(statuses))
	for _, app := range statuses {
		byID[app.ID] = app
	}
	app := byID[id]
	if app.State == "disabled" {
		return CoreAppAction{}, errors.New("该应用已在 Android 中停用，请先在系统设置中启用")
	}
	if app.State == "missing" {
		if id != "chrome" || byID["play"].State != "enabled" {
			return CoreAppAction{}, errors.New("该应用尚未安装或不可用")
		}
		out, err := adb(ctx, "shell", "am", "start", "-W", "-a", "android.intent.action.VIEW", "-d", "https://play.google.com/store/apps/details?id=com.android.chrome", "-p", "com.android.vending")
		if err != nil || !strings.Contains(string(out), "Status: ok") || strings.Contains(string(out), "Error:") {
			return CoreAppAction{}, errors.New("无法打开 Chrome 的 Google Play 页面")
		}
		return CoreAppAction{Action: "play_listing"}, nil
	}
	out, err := adb(ctx, "shell", "monkey", "-p", app.Package, "-c", "android.intent.category.LAUNCHER", "1")
	if err != nil || !strings.Contains(string(out), "Events injected: 1") {
		return CoreAppAction{}, errors.New("应用没有可启动的入口，或启动失败")
	}
	return CoreAppAction{Action: "opened"}, nil
}

func (m Manager) OpenCoreApp(ctx context.Context, instance Instance, id string) (result CoreAppAction, err error) {
	err = m.withCurrentInstance(ctx, instance, func(ctx context.Context, current Instance) error {
		result, err = openCoreApp(ctx, id, func(ctx context.Context, args ...string) ([]byte, error) {
			return m.adb(ctx, current.Serial, args...)
		})
		return err
	})
	return result, err
}
