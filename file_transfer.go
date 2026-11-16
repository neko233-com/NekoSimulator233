package main

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func (a *app) chooseImportFile() {
	if a.busy {
		return
	}
	a.busy = true
	a.message = "请选择要导入模拟器的文件…"
	name, window := a.selectedName(), a.window
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: window, Title: "选择要导入 Android 的文件"})
		var result avd.ImportedFile
		if err == nil && len(paths) > 0 {
			manager, managerErr := selectedManager(name)
			err = managerErr
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				instance, readyErr := manager.Ready(ctx)
				err = readyErr
				if err == nil {
					result, err = manager.ImportFile(ctx, instance, paths[0])
				}
				cancel()
			}
		}
		window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "导入文件失败：" + err.Error()
			} else if len(paths) == 0 {
				a.message = "已取消选择文件"
			} else {
				a.message = "已导入 " + name + "：" + result.GuestPath
			}
		})
	}()
}
