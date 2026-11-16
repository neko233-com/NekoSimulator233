package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func (a *app) view(c *ui.Context) {
	if a.selected == "" {
		a.selected = avd.DefaultName
	}
	if a.newSystem == "" {
		a.newSystem = systemAuto
	}
	ui.Row(c).Fill().Background(ui.RGB(247, 247, 247)).Children(func() {
		ui.Column(c).Width(72).FillHeight().Padding(10, 8).Gap(8).Background(ui.RGB(241, 241, 241)).Children(func() {
			if ui.Button(c.Key("device-tab"), "设备").FillWidth().Height(48).Padding(0).Disabled(a.busy).Clicked() {
				a.deviceTools = false
			}
			if ui.Button(c.Key("tools-tab"), "操作").FillWidth().Height(48).Padding(0).Disabled(a.busy).Clicked() {
				a.deviceTools = true
			}
		})
		ui.Column(c).Grow(1).FillHeight().Padding(16, 24).Gap(12).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c.Key("new-device"), "＋ 新建设备").Disabled(a.busy).Clicked() {
					a.showCreate = !a.showCreate
					a.deviceTools = false
				}
				if ui.Button(c.Key("open-tools"), "设备操作").Disabled(a.busy).Clicked() {
					a.deviceTools = !a.deviceTools
				}
				if ui.Button(c.Key("refresh-devices"), "刷新设备").Disabled(a.busy).Clicked() {
					a.refresh()
				}
			})
			if a.message != "" {
				ui.Text(c, a.message).FontSize(12).TextColor(ui.RGB(103, 110, 119))
			}
			if a.showCreate && !a.deviceTools {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					ui.TextInput(c.Key("device-name"), &a.newInstance).Placeholder("新设备名称").Width(190).Disabled(a.busy)
					ui.Select(c.Key("device-system"), &a.newSystem, []string{systemAuto, systemAndroid11}).Label("系统版本").Disabled(a.busy)
					if ui.Button(c.Key("device-create"), "创建").Disabled(a.busy).Clicked() {
						a.createIndependent()
					}
				})
			}
			if a.deviceTools {
				a.toolsView(c)
				return
			}
			ui.Scroll(c).FillWidth().Grow(1).Gap(12).Children(func() {
				items := a.available
				if len(items) == 0 {
					items = []avd.InstanceSummary{{Name: avd.DefaultName, State: "not_created"}}
				}
				for _, item := range items {
					a.deviceCard(c, item)
				}
			})
			if a.sdkCancel != nil && ui.Button(c.Key("cancel-device-start"), "取消准备或启动").Clicked() {
				a.sdkCancel()
				a.message = "正在取消准备或启动…"
			}
			if a.updateMessage != "" {
				ui.Text(c, a.updateMessage).FontSize(12).TextColor(ui.RGB(103, 110, 119))
			}
		})
	})
}

func (a *app) deviceCard(c *ui.Context, item avd.InstanceSummary) {
	state := "未创建"
	switch item.State {
	case "stopped":
		state = "未开机"
	case "running":
		state = "运行中"
	case "unknown":
		state = "状态未知"
	}
	ui.Row(c.Key("device-card-" + item.Name)).FillWidth().Gap(24).AlignItems(ui.Center).PaddingY(4).Children(func() {
		ui.Column(c).Size(190, 106).Radius(6).Background(ui.RGB(124, 144, 163)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Size(56, 68).Radius(5).Background(ui.RGB(36, 50, 67)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "安卓").FontSize(12).TextColor(ui.RGB(255, 255, 255))
			})
			ui.Text(c, state).FontSize(14).TextColor(ui.RGB(255, 255, 255))
		})
		ui.Column(c).Grow(1).Gap(7).Children(func() {
			ui.Text(c, item.Name).FontSize(20).Bold()
			if item.API != 0 {
				ui.Text(c, fmt.Sprintf("Android API %d  ·  %s", item.API, imageLabel(item.ImageTag))).FontSize(12).TextColor(ui.RGB(68, 78, 88))
			} else {
				ui.Text(c, "Android 设备").FontSize(12).TextColor(ui.RGB(104, 114, 124))
			}
			if item.Reason != "" {
				ui.Text(c, item.Reason).FontSize(12).TextColor(ui.RGB(160, 64, 64))
			} else if item.TapTapSetup == "retry_pending" {
				ui.Text(c, "TapTap 准备未完成，可在设备操作中重试").FontSize(12).TextColor(ui.RGB(104, 114, 124))
			}
		})
		button := "▶ 启动"
		if item.State == "running" {
			button = "▶ 打开"
		}
		if ui.PrimaryButton(c.Key("device-open-"+item.Name), button).Disabled(a.busy || item.State == "unknown").Clicked() {
			a.selected = item.Name
			a.startIndependent()
		}
		label := "操作"
		if strings.EqualFold(a.selectedName(), item.Name) {
			label = "⋯"
		}
		if ui.Button(c.Key("device-tools-"+item.Name), label).Disabled(a.busy || item.State == "unknown").Clicked() {
			a.selected = item.Name
			a.deviceTools = true
		}
	})
}

func imageLabel(tag string) string {
	switch tag {
	case "google_apis_playstore":
		return "Google Play"
	case "google_apis":
		return "Google APIs"
	default:
		return "Android"
	}
}
