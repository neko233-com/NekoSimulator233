package main

import (
	"context"
	"time"
)

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
