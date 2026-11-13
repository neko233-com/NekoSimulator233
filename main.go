package main

import (
	"fmt"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/mumu"
)

type app struct {
	window    *mygo.Window
	client    mumu.Client
	instances []mumu.Instance
	busy      bool
	message   string
}

func (a *app) refresh() {
	if a.busy {
		return
	}
	a.busy = true
	a.message = "正在读取 MuMu 实例…"
	go func() {
		root, err := mumu.Discover()
		var client mumu.Client
		var instances []mumu.Instance
		if err == nil {
			client, err = mumu.New(root)
		}
		if err == nil {
			instances, err = client.List()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = err.Error()
				return
			}
			a.client, a.instances = client, instances
			a.message = fmt.Sprintf("已连接 MuMu · %d 个实例", len(instances))
		})
	}()
}

func (a *app) open(instance mumu.Instance) {
	if a.busy {
		return
	}
	a.busy = true
	a.message = "正在打开实例…"
	go func() {
		err := a.client.Open(instance)
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = err.Error()
			} else {
				a.refresh()
			}
		})
	}()
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(28).Gap(16).Children(func() {
		ui.Text(c, "NekoSimulator233").FontSize(26).Bold()
		ui.Text(c, "MuMu 实例管理").FontSize(15).TextColor(t.TextMuted)
		ui.Text(c, a.message).FontSize(13)
		if ui.Button(c.Key("refresh"), "刷新实例").Disabled(a.busy).Clicked() {
			a.refresh()
		}
		for _, instance := range a.instances {
			instance := instance
			ui.Row(c.Key("instance-" + instance.Index)).Gap(16).Children(func() {
				state := "已停止"
				if instance.Started {
					state = "启动中"
				}
				if instance.AndroidStarted {
					state = "运行中"
				}
				ui.Text(c, fmt.Sprintf("%s  ·  Android %s  ·  %s", instance.Name, instance.AndroidVersion, state))
				if ui.PrimaryButton(c.Key("open-"+instance.Index), "打开").Disabled(a.busy).Clicked() {
					a.open(instance)
				}
			})
		}
		ui.Text(c, "此版本使用本机 MuMu 引擎运行实例。更新源尚未配置。").FontSize(12).TextColor(t.TextMuted)
	})
}

func main() {
	a := &app{message: "正在连接…"}
	mygo.App.WhenReady(func() {
		a.window = mygo.NewWindow(mygo.WindowOptions{Title: "NekoSimulator233", Width: 760, Height: 500, Content: ui.View(a.view)})
		a.refresh()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
