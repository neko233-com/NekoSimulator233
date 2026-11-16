package main

import (
	"context"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
