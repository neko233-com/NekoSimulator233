package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func (a *app) cancelSDKForClose() bool {
	if a.sdkCancel == nil {
		return false
	}
	a.closeAfterSDK = true
	a.sdkCancel()
	a.message = "正在取消准备或启动，结束后关闭…"
	return true
}

func (a *app) installPlayImage() {
	if a.busy {
		return
	}
	api, err := newSystemAPI(a.newSystem)
	if err != nil {
		a.message = err.Error()
		return
	}
	if api == 0 {
		api = 34
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.busy, a.sdkCancel = true, cancel
	a.sdkJob++
	job := a.sdkJob
	a.message = fmt.Sprintf("正在安装 Android %d Google Play 镜像…", api)
	parent := a.window
	go func() {
		defer cancel()
		manager, err := avd.DiscoverManager()
		var result avd.PlayImageInstallResult
		if err == nil {
			result, err = manager.InstallPlayImage(ctx, api, func(percent int) {
				parent.Update(func() {
					if a.sdkJob == job && a.sdkCancel != nil && ctx.Err() == nil {
						a.message = fmt.Sprintf("正在安装 Android %d Google Play 镜像：%d%%", api, percent)
					}
				})
			})
		}
		parent.Update(func() {
			a.busy, a.sdkCancel = false, nil
			switch {
			case errors.Is(err, context.Canceled):
				a.message = "镜像安装已取消；如 SDK Manager 留下不完整目录，请先检查"
			case err != nil:
				a.message = "Google Play 镜像安装未完成：" + err.Error()
			case result.AlreadyInstalled:
				a.message = fmt.Sprintf("Android %d Google Play 镜像已存在；新实例可使用", api)
			default:
				a.message = fmt.Sprintf("Android %d Google Play 镜像安装完成；新实例可使用", api)
			}
			if a.closeAfterSDK {
				a.closeAfterSDK = false
				parent.Close()
			}
		})
	}()
}
