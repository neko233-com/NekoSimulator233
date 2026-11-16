package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/mumu"
	"github.com/neko233-com/NekoSimulator233/internal/update"
)

var version = "0.0.0"

type app struct {
	window        *mygo.Window
	client        mumu.Client
	root          string
	instances     []mumu.Instance
	busy          bool
	message       string
	updateMessage string
	viewers       map[string]*viewer
	resources     *resourceSettings
}

func (a *app) startIndependent() {
	if a.busy {
		return
	}
	if v := a.viewers["independent"]; v != nil {
		v.win.Show()
		v.win.Focus()
		return
	}
	a.busy = true
	a.message = "正在启动独立 Android 实例并验证 Vulkan…"
	go func() {
		manager, err := avd.DiscoverManager()
		var instance avd.Instance
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			instance, err = manager.Start(ctx)
			cancel()
		}
		var source *avd.Controller
		if err == nil {
			source, err = instance.Open()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = err.Error()
				return
			}
			a.message = fmt.Sprintf("独立实例已启动 · Vulkan: %s", instance.VulkanDevice)
			if !instance.KeyboardEnabled {
				a.message += " · 键盘未启用，请关闭后重新启动实例"
			}
			if a.viewers == nil {
				a.viewers = make(map[string]*viewer)
			}
			a.viewers["independent"] = openViewer("NekoSimulator233", source, func() { delete(a.viewers, "independent") })
		})
	}()
}

func (a *app) stopIndependent() {
	if a.busy {
		return
	}
	if v := a.viewers["independent"]; v != nil {
		v.win.Close()
	}
	a.busy = true
	a.message = "正在关闭独立实例…"
	go func() {
		manager, err := avd.DiscoverManager()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err = manager.Stop(ctx)
			cancel()
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = "独立实例已关闭，数据已保留"
			}
		})
	}()
}

// chooseAPK keeps the native picker and ADB work off the UI thread. The
// explicit update action is the only path that permits replacing a package.
func (a *app) chooseAPK(replace bool) {
	if a.busy {
		return
	}
	a.busy = true
	a.message = "请选择要安装到独立实例的 APK…"
	parent := a.window
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: "选择 Android APK", Filters: []mygo.FileFilter{{Name: "Android APK", Extensions: []string{"apk"}}},
		})
		if err == nil && len(paths) != 0 {
			parent.Update(func() { a.message = "正在安装 APK，请稍候…" })
			manager, discoverErr := avd.DiscoverManager()
			err = discoverErr
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				instance, readyErr := manager.Ready(ctx)
				cancel()
				err = readyErr
				if err == nil {
					ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
					err = manager.InstallAPK(ctx, instance, paths[0], replace)
					cancel()
				}
			}
		}
		parent.Update(func() {
			a.busy = false
			switch {
			case err != nil:
				a.message = "APK 安装失败：" + err.Error()
			case len(paths) == 0:
				a.message = "已取消选择 APK"
			case replace:
				a.message = "APK 已安装或更新，可在独立实例中打开"
			default:
				a.message = "APK 安装完成，可在独立实例中打开"
			}
		})
	}()
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
			a.client, a.root, a.instances = client, root, instances
			a.message = fmt.Sprintf("已连接 MuMu · %d 个实例", len(instances))
		})
	}()
}

func (a *app) open(instance mumu.Instance) {
	if a.busy {
		return
	}
	if v := a.viewers[instance.Index]; v != nil {
		v.win.Show()
		v.win.Focus()
		return
	}
	a.busy = true
	a.message = "正在连接画面…"
	root, client := a.root, a.client
	go func() {
		if !instance.AndroidStarted {
			err := client.Open(instance)
			a.window.Update(func() {
				a.busy = false
				if err != nil {
					a.message = err.Error()
				} else {
					a.message = "已请求启动，请在 Android 启动后刷新并打开画面"
				}
			})
			return
		}
		renderer, err := mumu.OpenRenderer(root, instance)
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = "已连接实例画面"
				if a.viewers == nil {
					a.viewers = make(map[string]*viewer)
				}
				a.viewers[instance.Index] = openViewer(instance.Name, renderer, func() { delete(a.viewers, instance.Index) })
			}
		})
	}()
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(28).Gap(16).Children(func() {
		ui.Text(c, "NekoSimulator233").FontSize(26).Bold()
		ui.Text(c, "Android 模拟器").FontSize(15).TextColor(t.TextMuted)
		ui.Text(c, a.message).FontSize(13)
		if ui.PrimaryButton(c.Key("independent"), "启动独立模拟器（Vulkan）").Disabled(a.busy).Clicked() {
			a.startIndependent()
		}
		if ui.Button(c.Key("stop-independent"), "关闭独立实例").Disabled(a.busy).Clicked() {
			a.stopIndependent()
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("resource-settings"), "资源设置…").Disabled(a.busy).Clicked() {
				a.openResources()
			}
			if ui.Button(c.Key("install-apk"), "安装 APK…").Disabled(a.busy).Clicked() {
				a.chooseAPK(false)
			}
			if ui.Button(c.Key("update-apk"), "更新 APK…").Disabled(a.busy).Clicked() {
				a.chooseAPK(true)
			}
		})
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
		ui.Text(c, "独立实例使用本机 Android SDK；下方实例使用 MuMu 引擎。").FontSize(12).TextColor(t.TextMuted)
		if a.updateMessage != "" {
			ui.Text(c, a.updateMessage).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

func prepareUpdate() (bool, string) {
	pending, pendingErr := update.PendingReboot()
	if pendingErr != nil {
		return false, "更新状态读取失败: " + pendingErr.Error()
	}
	if pending {
		return false, "更新已安装，请重启 Windows 完成更新"
	}
	if len(os.Args) > 1 && os.Args[1] == "--skip-update-once" {
		b, _ := os.ReadFile(filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "last-update.txt"))
		return false, string(b)
	}
	executable, err := os.Executable()
	if err != nil {
		return false, "更新检查失败: " + err.Error()
	}
	installed := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233", "NekoSimulator233.exe")
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(installed)) {
		return false, ""
	}
	manifest := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "update.json")
	m, err := update.Load(manifest, version)
	if err != nil {
		return false, "更新检查失败: " + err.Error()
	}
	if m == nil {
		return false, ""
	}
	if err = update.Start(m, version, executable); err != nil {
		return false, "更新准备失败: " + err.Error()
	}
	return true, ""
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--update-worker" {
		_ = update.Worker(os.Args[2:])
		return
	}
	started, updateMessage := prepareUpdate()
	if started {
		return
	}
	a := &app{message: "正在连接…", updateMessage: updateMessage}
	mygo.App.WhenReady(func() {
		a.window = mygo.NewWindow(mygo.WindowOptions{Title: "NekoSimulator233", Width: 760, Height: 500, Content: ui.View(a.view)})
		a.refresh()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
