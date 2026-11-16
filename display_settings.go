package main

import (
	"context"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

type displaySettingsWindow struct {
	win                    *mygo.Window
	manager                avd.Manager
	width, height, density float64
	busy                   bool
	message                string
}

func (a *app) openDisplaySettings() {
	if a.busy {
		return
	}
	name := a.selectedName()
	key := independentViewerKey(name)
	if existing := a.displays[key]; existing != nil {
		existing.win.Show()
		existing.win.Focus()
		return
	}
	a.busy = true
	a.message = "正在读取显示设置…"
	go func() {
		manager, err := selectedManager(name)
		var values avd.DisplaySettings
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			values, err = manager.GetDisplaySettings(ctx)
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "读取显示设置失败：" + err.Error()
				return
			}
			a.message = ""
			s := &displaySettingsWindow{manager: manager, width: float64(values.Width), height: float64(values.Height), density: float64(values.Density)}
			s.win = mygo.NewWindow(mygo.WindowOptions{Title: "显示设置 - " + name, Width: 560, Height: 300, Content: ui.View(s.view)})
			if a.displays == nil {
				a.displays = make(map[string]*displaySettingsWindow)
			}
			a.displays[key] = s
			s.win.OnClosed(func() { delete(a.displays, key) })
		})
	}()
}

func (s *displaySettingsWindow) save() {
	if s.busy {
		return
	}
	for _, value := range []float64{s.width, s.height, s.density} {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			s.message = "请输入整数分辨率和 DPI"
			return
		}
	}
	if s.width < 320 || s.width > 8192 || s.height < 320 || s.height > 8192 || s.width*s.height > 32*1024*1024 || s.density < 72 || s.density > 1000 {
		s.message = "分辨率或 DPI 超出支持范围"
		return
	}
	values := avd.DisplaySettings{Width: int(s.width), Height: int(s.height), Density: int(s.density)}
	s.busy, s.message = true, "正在保存…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := s.manager.SetDisplaySettings(ctx, values)
		cancel()
		s.win.Update(func() {
			s.busy = false
			if err != nil {
				s.message = "保存失败：" + err.Error()
			} else {
				s.message = "设置已保存，下次启动时生效"
			}
		})
	}()
}

func (s *displaySettingsWindow) view(c *ui.Context) {
	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Text(c, "独立实例显示设置").FontSize(20).Bold()
		ui.Text(c, "请先关闭独立实例；设置将在下次启动时生效。")
		ui.Row(c).Gap(12).Children(func() {
			ui.NumberInput(c.Key("display-width"), &s.width, 320, 8192, 1).Label("宽度 px").Disabled(s.busy)
			ui.NumberInput(c.Key("display-height"), &s.height, 320, 8192, 1).Label("高度 px").Disabled(s.busy)
			ui.NumberInput(c.Key("display-density"), &s.density, 72, 1000, 1).Label("DPI").Disabled(s.busy)
		})
		if ui.PrimaryButton(c.Key("save-display"), "保存设置").Disabled(s.busy).Clicked() {
			s.save()
		}
		ui.Text(c, s.message).FontSize(12)
	})
}
