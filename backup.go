package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

// Normal window close waits for cancellation cleanup. OS session shutdown may
// still terminate the process, so the backend also avoids overwriting targets.
func (a *app) cancelBackupForClose() bool {
	if a.backupCancel == nil {
		return false
	}
	a.closeAfterBackup = true
	a.backupCancel()
	a.message = "正在取消备份或恢复，清理完成后关闭…"
	return true
}

func (a *app) transferBackup(restore bool) {
	if a.busy {
		return
	}
	name := a.selectedName()
	if restore {
		name = strings.TrimSpace(a.newInstance)
		if name == "" {
			a.message = "请先填写恢复后的新实例名称"
			return
		}
	}
	a.busy = true
	if restore {
		a.message = "请选择独立实例备份…"
	} else {
		a.message = "请选择备份保存位置…"
	}
	parent := a.window
	ctx, cancel := context.WithCancel(context.Background())
	a.backupCancel = cancel
	go func() {
		defer cancel()
		manager, err := selectedManager(name)
		var path string
		if err == nil && restore {
			var paths []string
			paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: "选择独立实例备份", Filters: []mygo.FileFilter{{Name: "Neko 备份", Extensions: []string{"nekobak"}}}})
			if len(paths) > 0 {
				path = paths[0]
			}
		} else if err == nil {
			path, err = mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: parent, Title: "备份独立实例", DefaultPath: name + ".nekobak", Filters: []mygo.FileFilter{{Name: "Neko 备份", Extensions: []string{"nekobak"}}}})
			if path != "" && filepath.Ext(path) == "" {
				path += ".nekobak"
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		completed := false
		var instances []avd.InstanceSummary
		var listErr error
		if err == nil && path != "" {
			parent.Update(func() {
				if restore {
					a.message = "正在恢复独立实例，数据量较大时需要较长时间…"
				} else {
					a.message = "正在备份独立实例，数据量较大时需要较长时间…"
				}
			})
			if restore {
				err = manager.ImportBackup(ctx, path)
			} else {
				err = manager.ExportBackup(ctx, path)
			}
			completed = err == nil
			if completed && restore {
				listCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				instances, listErr = manager.ListInstances(listCtx)
				stop()
			}
		}
		parent.Update(func() {
			a.busy = false
			a.backupCancel = nil
			switch {
			case errors.Is(err, context.Canceled):
				a.message = "备份或恢复已取消"
			case err != nil:
				a.message = "备份或恢复失败：" + err.Error()
			case !completed:
				a.message = "已取消选择备份文件"
			case restore:
				if listErr != nil {
					instances = append(append([]avd.InstanceSummary(nil), a.available...), avd.InstanceSummary{Name: name, State: "stopped"})
				}
				a.selected = name
				a.setAvailable(instances)
				a.newInstance = ""
				a.message = fmt.Sprintf("实例 %s 已恢复，尚未启动", name)
				if listErr != nil {
					a.message += "；刷新列表失败：" + listErr.Error()
				}
			default:
				a.message = "备份完成：" + path
			}
			if a.closeAfterBackup {
				a.closeAfterBackup = false
				parent.Close()
			}
		})
	}()
}
