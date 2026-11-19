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
	ui.Row(c).Fill().Background(ui.RGB(28, 28, 36)).Children(func() {
		ui.Column(c).Width(76).FillHeight().Padding(10, 8).Gap(8).Background(ui.RGB(22, 22, 30)).Children(func() {
			for _, tab := range []string{"设备", "操作", "远控", "云手机", "反馈"} {
				label := tab
				if ui.Button(c.Key("side-tab-"+label), label).FillWidth().Height(48).Padding(0).Clicked() {
					a.sideTab = label
					a.deviceTools = label == "操作"
				}
			}
		})
		ui.Column(c).Grow(1).FillHeight().Padding(16, 24).Gap(12).Children(func() {
			switch a.sideTab {
			case "远控":
				ui.Text(c, "远控").FontSize(24).Bold().TextColor(ui.RGB(30, 36, 44))
				ui.Text(c, "本机桌面控制由 agent-desktop 提供，已作为 desktop_* 工具接入 neko-mcp。").FontSize(14)
				ui.Text(c, "可用 agent-desktop snapshot / find / click 观察并操作 Windows 应用与模拟器窗口。").FontSize(13).TextColor(ui.RGB(103, 110, 119))
				return
			case "云手机":
				ui.Text(c, "云手机").FontSize(24).Bold().TextColor(ui.RGB(30, 36, 44))
				ui.Text(c, "离线版本不包含云端实例；所有实例均在本机运行与保存。").FontSize(14).TextColor(ui.RGB(103, 110, 119))
				return
			case "反馈":
				ui.Text(c, "反馈").FontSize(24).Bold().TextColor(ui.RGB(30, 36, 44))
				ui.Text(c, "本版本无广告、无会员、无推广弹窗。").FontSize(14)
				ui.Text(c, "问题与建议请通过项目 GitHub Issue 提交。").FontSize(13).TextColor(ui.RGB(103, 110, 119))
				return
			}
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
				if ui.Button(c.Key("batch-operations"), "批量操作").Clicked() {
					a.batchPanel = !a.batchPanel
				}
				if ui.Button(c.Key("arrange-windows"), "排列窗口").Clicked() {
					a.arrangeViewers()
				}
			})
			if a.batchPanel {
				ui.Row(c).FillWidth().Gap(10).Children(func() {
					if ui.Button(c.Key("batch-start"), "批量开机").Disabled(a.busy).Clicked() {
						a.batchStart()
					}
					if ui.Button(c.Key("batch-stop"), "批量关机").Disabled(a.busy).Clicked() {
						a.batchStop()
					}
				})
			}
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("device-search"), &a.deviceSearch).Placeholder("搜索设备").Width(200)
				if a.deviceSort == "" {
					a.deviceSort = deviceSortName
				}
				ui.Select(c.Key("device-sort"), &a.deviceSort, []string{deviceSortName, deviceSortState, deviceSortSystem}).Label("排序")
				viewLabel := "列表"
				if a.deviceGridView {
					viewLabel = "网格"
				}
				if ui.Button(c.Key("device-view"), viewLabel).Clicked() {
					a.deviceGridView = !a.deviceGridView
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
				items := visibleDevices(a.available, a.deviceSearch, a.deviceSort)
				if len(items) == 0 {
					items = []avd.InstanceSummary{{Name: avd.DefaultName, State: "not_created"}}
				}
				if a.deviceGridView {
					ui.Grid(c).FillWidth().Columns(2).Gap(12).Children(func() {
						for i, item := range items {
							a.deviceCard(c, item, i)
						}
					})
				} else {
					for i, item := range items {
						a.deviceCard(c, item, i)
					}
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

func (a *app) deviceCard(c *ui.Context, item avd.InstanceSummary, index int) {
	state := "未创建"
	switch item.State {
	case "stopped":
		state = "未开机"
	case "running":
		state = "运行中"
	case "unknown":
		state = "状态未知"
	}
	if item.State == "running" {
		a.ensureThumb(item.Name)
	}
	thumb := a.thumbFor(item.Name)
	ui.Row(c.Key("device-card-"+item.Name)).FillWidth().Gap(24).AlignItems(ui.Center).Padding(10, 6).Radius(8).Background(ui.RGB(38, 38, 50)).Children(func() {
		ui.Column(c).Size(190, 106).Radius(6).Background(ui.RGB(58, 62, 78)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("#%d", index)).FontSize(11).TextColor(ui.RGB(232, 240, 250))
			if thumb != nil {
				ui.Image(c, thumb).Size(170, 76).Fit(ui.Cover)
			} else {
				ui.Column(c).Size(56, 56).Radius(5).Background(ui.RGB(36, 50, 67)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "安卓").FontSize(12).TextColor(ui.RGB(255, 255, 255))
				})
			}
			ui.Text(c, state).FontSize(14).TextColor(ui.RGB(255, 255, 255))
		})
		ui.Column(c).Grow(1).Gap(7).Children(func() {
			ui.Text(c, item.Name).FontSize(20).Bold().TextColor(ui.RGB(240, 243, 250))
			ui.Text(c, androidLabel(item.API)).FontSize(12).Bold().Width(96).TextColor(ui.RGB(255, 255, 255)).Background(ui.RGB(30, 110, 200)).Padding(3, 6)
			if item.API != 0 {
				ui.Text(c, fmt.Sprintf("%s  ·  %s", imageLabel(item.ImageTag), androidLabel(item.API))).FontSize(12).TextColor(ui.RGB(168, 176, 192))
			} else {
				ui.Text(c, "Android 设备").FontSize(12).TextColor(ui.RGB(150, 158, 174))
			}
			if item.Cores > 0 || item.RAMMiB > 0 || (item.Width > 0 && item.Height > 0) {
				spec := ""
				if item.Cores > 0 {
					spec = fmt.Sprintf("%d 核", item.Cores)
				}
				if item.RAMMiB > 0 {
					if spec != "" {
						spec += "  ·  "
					}
					spec += fmt.Sprintf("%d MB", item.RAMMiB)
				}
				if item.Width > 0 && item.Height > 0 {
					if spec != "" {
						spec += "  ·  "
					}
					spec += fmt.Sprintf("%d × %d", item.Width, item.Height)
					if item.Density > 0 {
						spec += fmt.Sprintf("  %d DPI", item.Density)
					}
				}
				ui.Text(c, spec).FontSize(12).TextColor(ui.RGB(104, 114, 124))
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
		if ui.Button(c.Key("device-open-"+item.Name), button).Disabled(a.busy || item.State == "unknown").Clicked() {
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
