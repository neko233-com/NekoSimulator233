package main

import (
	"context"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

type resourceSettings struct {
	win     *mygo.Window
	manager avd.Manager
	cores   float64
	ram     float64
	busy    bool
	message string
}

func (a *app) openResources() {
	if a.busy {
		return
	}
	name := a.selectedName()
	key := independentViewerKey(name)
	if existing := a.resources[key]; existing != nil {
		existing.win.Show()
		existing.win.Focus()
		return
	}
	a.busy = true
	a.message = "正在读取独立实例资源设置…"
	go func() {
		manager, err := selectedManager(name)
		var values avd.Resources
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			values, err = manager.GetResources(ctx)
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "读取资源设置失败：" + err.Error()
				return
			}
			a.message = ""
			s := &resourceSettings{manager: manager, cores: float64(values.Cores), ram: float64(values.RAMMiB)}
			s.win = mygo.NewWindow(mygo.WindowOptions{Title: "资源设置 - " + name, Width: 500, Height: 300, Content: ui.View(s.view)})
			if a.resources == nil {
				a.resources = make(map[string]*resourceSettings)
			}
			a.resources[key] = s
			s.win.OnClosed(func() { delete(a.resources, key) })
		})
	}()
}

func (s *resourceSettings) save() {
	if s.busy {
		return
	}
	if math.IsNaN(s.cores) || math.IsInf(s.cores, 0) || math.Trunc(s.cores) != s.cores || math.IsNaN(s.ram) || math.IsInf(s.ram, 0) || math.Trunc(s.ram) != s.ram || s.cores < 1 || s.cores > 64 || s.ram < 512 || s.ram > 131072 {
		s.message = "请输入有效的整数 CPU 核数和内存大小"
		return
	}
	values := avd.Resources{Cores: int(s.cores), RAMMiB: int(s.ram)}
	s.busy = true
	s.message = "正在保存…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := s.manager.SetResources(ctx, values)
		cancel()
		s.win.Update(func() {
			s.busy = false
			if err != nil {
				s.message = "保存失败：" + err.Error()
			} else {
				s.message = "设置已保存，下次启动独立实例时生效"
			}
		})
	}()
}

func (s *resourceSettings) view(c *ui.Context) {
	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Text(c, "独立实例资源设置").FontSize(20).Bold()
		ui.Text(c, "请先关闭独立实例；设置将在下次启动时生效。")
		ui.Row(c).Gap(12).Children(func() {
			ui.NumberInput(c.Key("cores"), &s.cores, 1, 64, 1).Label("CPU 核数").Disabled(s.busy)
			ui.NumberInput(c.Key("ram"), &s.ram, 512, 131072, 256).Label("内存 MiB").Disabled(s.busy)
		})
		if ui.PrimaryButton(c.Key("save-resources"), "保存设置").Disabled(s.busy).Clicked() {
			s.save()
		}
		ui.Text(c, s.message).FontSize(12)
	})
}
