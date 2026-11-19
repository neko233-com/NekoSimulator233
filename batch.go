package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/viewlayout"
)

// startInstanceForBatch starts one project-owned instance and opens its viewer.
// It mirrors startIndependent but never touches a.busy so batch jobs can drive
// several instances sequentially. Viewer creation must happen on the UI thread.
func startInstanceForBatch(name string) (avd.Instance, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	manager, err := selectedManager(name)
	if err != nil {
		return avd.Instance{}, "", err
	}
	instance, err := manager.StartWithProgress(ctx, nil)
	if err != nil {
		return avd.Instance{}, "", err
	}
	bindings, err := resolveKeymapPathForInstance(name)
	return instance, bindings, err
}

// batchStart boots every instance that is stopped or not yet created and opens
// a viewer for each, matching MuMu's multi-open entry action.
func (a *app) batchStart() {
	if a.busy {
		return
	}
	var targets []string
	for _, item := range a.available {
		if item.State == "stopped" || item.State == "not_created" {
			targets = append(targets, item.Name)
		}
	}
	if len(targets) == 0 {
		a.message = "没有可批量开机的实例"
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在批量开机：共 %d 个实例…", len(targets))
	go func() {
		started, failed := 0, 0
		for _, name := range targets {
			if (a.viewers != nil) && a.viewers[independentViewerKey(name)] != nil {
				started++
				continue
			}
			instance, bindings, err := startInstanceForBatch(name)
			if err != nil {
				failed++
				continue
			}
			key := independentViewerKey(name)
			a.window.Update(func() {
				if a.viewers == nil {
					a.viewers = make(map[string]*viewer)
				}
				if a.viewers[key] == nil {
					source, openErr := instance.Open()
					if openErr == nil {
						a.viewers[key] = openViewer(name, source, bindings, instance.Serial, func() { delete(a.viewers, key) })
					}
				}
			})
			started++
			a.window.Update(func() { a.message = fmt.Sprintf("批量开机中… 已启动 %d，失败 %d", started, failed) })
		}
		a.window.Update(func() {
			a.busy = false
			a.message = fmt.Sprintf("批量开机完成：成功 %d，失败 %d", started, failed)
			a.refresh()
		})
	}()
}

// batchStop closes all running viewers and shuts down every running instance.
func (a *app) batchStop() {
	if a.busy {
		return
	}
	var targets []string
	for _, item := range a.available {
		if item.State == "running" {
			targets = append(targets, item.Name)
		}
	}
	if len(targets) == 0 {
		a.message = "没有运行中的实例可关闭"
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在批量关机：共 %d 个实例…", len(targets))
	for _, name := range targets {
		key := independentViewerKey(name)
		if v := a.viewers[key]; v != nil {
			v.win.Close()
		}
	}
	go func() {
		stopped, failed := 0, 0
		for _, name := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			manager, err := selectedManager(name)
			if err == nil {
				err = manager.Stop(ctx)
			}
			cancel()
			if err != nil {
				failed++
			} else {
				stopped++
				a.window.Update(func() { a.markInstanceState(name, "stopped") })
			}
		}
		a.window.Update(func() {
			a.busy = false
			a.message = fmt.Sprintf("批量关机完成：成功 %d，失败 %d", stopped, failed)
			a.refresh()
		})
	}()
}

// arrangeViewers tiles all open viewer windows in the primary display's work
// area, like MuMu's "排列窗口" action.
func (a *app) arrangeViewers() {
	if len(a.viewers) == 0 {
		a.message = "没有打开的画面窗口可排列"
		return
	}
	keys := make([]string, 0, len(a.viewers))
	for key := range a.viewers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	windows := make([]*mygo.Window, 0, len(keys))
	for _, key := range keys {
		if v := a.viewers[key]; v != nil && v.win != nil {
			windows = append(windows, v.win)
		}
	}
	if len(windows) == 0 {
		a.message = "没有可排列的画面窗口"
		return
	}
	work := mygo.Screen.PrimaryDisplay().WorkArea
	tiles := viewlayout.Tile(len(windows), viewlayout.Rect{
		X: work.X, Y: work.Y, Width: work.Width, Height: work.Height,
	})
	for i, w := range windows {
		tile := tiles[i]
		w.SetPosition(tile.X, tile.Y)
		w.SetSize(tile.Width, tile.Height)
		w.Show()
	}
	a.message = fmt.Sprintf("已排列 %d 个画面窗口", len(windows))
}
