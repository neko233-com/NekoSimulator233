package main

import (
	"context"
	"strings"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func (a *app) showCoreAppStatus() {
	if a.busy {
		return
	}
	name := a.selectedName()
	a.busy = true
	a.message = "正在检查 " + name + " 的常用应用…"
	go func() {
		manager, err := selectedManager(name)
		var statuses []avd.CoreAppStatus
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			instance, readyErr := manager.Ready(ctx)
			err = readyErr
			if err == nil {
				statuses, err = manager.CoreAppStatuses(ctx, instance)
			}
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "应用状态检查失败：" + err.Error()
				return
			}
			labels := map[string]string{"chrome": "Chrome", "play": "Play 商店", "play_services": "Play 服务", "google_services_framework": "Google 框架", "taptap": "TapTap", "wechat": "微信", "douyin": "抖音"}
			states := map[string]string{"enabled": "已启用", "disabled": "已停用", "missing": "未安装"}
			parts := make([]string, 0, len(statuses))
			for _, status := range statuses {
				parts = append(parts, labels[status.ID]+"："+states[status.State])
			}
			a.message = name + " · " + strings.Join(parts, "；")
		})
	}()
}

func (a *app) openCoreApp(id, label string) {
	if a.busy {
		return
	}
	name := a.selectedName()
	a.busy = true
	a.message = "正在打开 " + label + "…"
	go func() {
		manager, err := selectedManager(name)
		var result avd.CoreAppAction
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			instance, readyErr := manager.Ready(ctx)
			err = readyErr
			if err == nil {
				result, err = manager.OpenCoreApp(ctx, instance, id)
			}
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "打开 " + label + " 失败：" + err.Error()
				return
			}
			if result.Action == "play_listing" {
				a.message = "Chrome 尚未安装，已打开 Google Play 页面；请在模拟器中完成安装"
				return
			}
			a.message = "已请求在 " + name + " 中打开 " + label
		})
	}()
}

func (a *app) installTapTap() {
	if a.busy {
		return
	}
	name := a.selectedName()
	a.busy = true
	a.message = "正在从官网下载并安装 TapTap…"
	go func() {
		manager, err := selectedManager(name)
		var result avd.TapTapInstallResult
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), avd.TapTapInstallTimeout)
			instance, readyErr := manager.Ready(ctx)
			err = readyErr
			if err == nil {
				result, err = manager.InstallTapTap(ctx, instance)
			}
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "TapTap 安装未完成：" + err.Error()
			} else if result.AlreadyInstalled {
				a.message = name + " 原先已安装 TapTap"
			} else {
				a.message = name + " 已安装 TapTap"
			}
		})
	}()
}

func (a *app) openOptionalDownload(id, label string) {
	if a.busy {
		return
	}
	name := a.selectedName()
	a.busy = true
	a.message = "正在打开" + label + "官方下载页面…"
	go func() {
		manager, err := selectedManager(name)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			instance, readyErr := manager.Ready(ctx)
			err = readyErr
			if err == nil {
				err = manager.OpenOfficialAppDownload(ctx, instance, id)
			}
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "打开" + label + "下载页面失败：" + err.Error()
				return
			}
			a.message = "已请求在 " + name + " 中打开" + label + "官方下载页面；请在模拟器中完成安装"
		})
	}()
}
