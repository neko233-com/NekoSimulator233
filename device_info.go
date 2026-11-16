package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func (a *app) showDeviceInfo() {
	if a.busy {
		return
	}
	name := a.selectedName()
	a.busy = true
	a.message = "正在读取独立实例兼容信息…"
	go func() {
		manager, err := selectedManager(name)
		var info avd.DeviceInfo
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			instance, readyErr := manager.Ready(ctx)
			err = readyErr
			if err == nil {
				info, err = manager.DeviceInfo(ctx, instance)
			}
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "读取兼容信息失败：" + err.Error()
				return
			}
			a.message = fmt.Sprintf("%s · Android API %d · ABI：%s · Native Bridge：%s", name, info.API, strings.Join(info.ABIs, ", "), info.NativeBridge)
		})
	}()
}
