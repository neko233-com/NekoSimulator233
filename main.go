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
	"github.com/neko233-com/NekoSimulator233/internal/migration"
	"github.com/neko233-com/NekoSimulator233/internal/mumu"
	"github.com/neko233-com/NekoSimulator233/internal/update"
	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

var version = "0.0.0"

type app struct {
	window           *mygo.Window
	client           mumu.Client
	root             string
	instances        []mumu.Instance
	busy             bool
	message          string
	updateMessage    string
	viewers          map[string]*viewer
	resources        map[string]*resourceSettings
	selected         string
	available        []avd.InstanceSummary
	newInstance      string
	newSystem        string
	backupCancel     context.CancelFunc
	closeAfterBackup bool
	sdkCancel        context.CancelFunc
	closeAfterSDK    bool
	sdkJob           uint64
	updateInProgress bool
	closeAfterUpdate bool
	suppressedUpdate string
}

func (a *app) selectedName() string {
	if a.selected == "" {
		return avd.DefaultName
	}
	return a.selected
}

func independentViewerKey(name string) string { return "independent:" + strings.ToLower(name) }

func selectedManager(name string) (avd.Manager, error) {
	m, err := avd.DiscoverManager()
	if err != nil {
		return m, err
	}
	return m.WithName(name)
}

func (a *app) startIndependent() {
	if a.busy {
		return
	}
	name := a.selectedName()
	key := independentViewerKey(name)
	if v := a.viewers[key]; v != nil {
		v.win.Show()
		v.win.Focus()
		return
	}
	a.busy = true
	a.message = "正在启动独立 Android 实例并验证 Vulkan…"
	go func() {
		manager, err := selectedManager(name)
		var instance avd.Instance
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			instance, err = manager.Start(ctx)
			cancel()
		}
		var source *avd.Controller
		var bindingsPath string
		if err == nil {
			source, err = instance.Open()
		}
		if err == nil {
			bindingsPath, err = resolveKeymapPathForInstance(name)
			if err != nil {
				_ = source.Close()
			}
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = err.Error()
				return
			}
			a.message = fmt.Sprintf("独立实例已启动 · Vulkan: %s", instance.VulkanDevice)
			a.markInstanceState(name, "running")
			if !instance.KeyboardEnabled {
				a.message += " · 键盘未启用，请关闭后重新启动实例"
			}
			if a.viewers == nil {
				a.viewers = make(map[string]*viewer)
			}
			a.viewers[key] = openViewer(name, source, bindingsPath, func() { delete(a.viewers, key) })
		})
	}()
}

func (a *app) stopIndependent() {
	if a.busy {
		return
	}
	name := a.selectedName()
	key := independentViewerKey(name)
	if v := a.viewers[key]; v != nil {
		v.win.Close()
	}
	a.busy = true
	a.message = "正在关闭独立实例…"
	go func() {
		manager, err := selectedManager(name)
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
				a.markInstanceState(name, "stopped")
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
	name := a.selectedName()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: "选择 Android APK（分包可多选）", Multiple: true, Filters: []mygo.FileFilter{{Name: "Android APK", Extensions: []string{"apk"}}},
		})
		if err == nil && len(paths) != 0 {
			parent.Update(func() { a.message = "正在安装 APK，请稍候…" })
			manager, discoverErr := selectedManager(name)
			err = discoverErr
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				instance, readyErr := manager.Ready(ctx)
				cancel()
				err = readyErr
				if err == nil {
					ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
					err = manager.InstallAPKs(ctx, instance, paths, replace)
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
		manager, independentErr := avd.DiscoverManager()
		var available []avd.InstanceSummary
		if independentErr == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			available, independentErr = manager.ListInstances(ctx)
			cancel()
		}
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
			if independentErr == nil {
				a.setAvailable(available)
			}
			if err != nil {
				a.message = "MuMu：" + err.Error()
				if independentErr != nil {
					a.message += "；独立实例：" + independentErr.Error()
				}
				return
			}
			a.client, a.root, a.instances = client, root, instances
			a.message = fmt.Sprintf("已连接 MuMu · %d 个实例", len(instances))
			if independentErr != nil {
				a.message += "；独立实例：" + independentErr.Error()
			}
		})
	}()
}

func (a *app) open(instance mumu.Instance) {
	if a.busy {
		return
	}
	key := "mumu:" + instance.Index
	if v := a.viewers[key]; v != nil {
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
				a.viewers[key] = openViewer(instance.Name, renderer, "", func() { delete(a.viewers, key) })
			}
		})
	}()
}

func (a *app) checkMuMuMigration(instance mumu.Instance) {
	if a.busy {
		return
	}
	a.busy = true
	a.message = "正在只读检查迁移条件…"
	client := a.client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
		defer cancel()
		manager, err := avd.DiscoverManager()
		var report migration.Report
		if err == nil {
			report, err = migration.Check(ctx, instance.Index, manager.SDK, client.ListContext)
		}
		a.window.Update(func() {
			a.busy = false
			if err != nil {
				a.message = "迁移检查失败：" + err.Error()
				return
			}
			image := "源系统版本尚无匹配的受支持镜像"
			if report.MatchingImageAPI != 0 {
				image = fmt.Sprintf("检测到 Android API %d system.img，完整性未验证", report.MatchingImageAPI)
				if !report.MatchingImageFilePresent {
					image = fmt.Sprintf("缺少 Android API %d system.img", report.MatchingImageAPI)
				}
			}
			state := "未确认源实例运行状态"
			if report.SourceRunningReported {
				state = "源实例正在运行"
			}
			a.message = fmt.Sprintf("MuMu %s：%s；%s。完整数据迁移尚未实现，未修改实例。", report.SourceIndex, state, image)
		})
	}()
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Fill().Padding(28).Gap(12).Children(func() {
		ui.Text(c, "NekoSimulator233").FontSize(26).Bold()
		ui.Text(c, "Android 模拟器").FontSize(15).TextColor(t.TextMuted)
		ui.Text(c, a.message).FontSize(13)
		if a.selected == "" {
			a.selected = avd.DefaultName
		}
		if a.newSystem == "" {
			a.newSystem = systemAuto
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Select(c.Key("independent-select"), &a.selected, a.instanceChoices()).Label("独立实例").Disabled(a.busy)
			ui.TextInput(c.Key("new-instance"), &a.newInstance).Placeholder("新建或恢复的实例名称").Width(220).Disabled(a.busy)
			if ui.Button(c.Key("create-instance"), "新建实例").Disabled(a.busy).Clicked() {
				a.createIndependent()
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Select(c.Key("new-system"), &a.newSystem, []string{systemAuto, systemAndroid11}).Label("新实例系统").Disabled(a.busy)
			if ui.Button(c.Key("install-play-image"), "安装 Google Play 镜像…").Disabled(a.busy).Clicked() {
				a.installPlayImage()
			}
			if a.sdkCancel != nil && ui.Button(c.Key("cancel-play-image"), "取消镜像安装").Clicked() {
				a.sdkCancel()
				a.message = "正在取消镜像安装…"
			}
		})
		if status := a.selectedStatus(); status != "" {
			ui.Text(c, status).FontSize(12).TextColor(t.TextMuted)
		}
		for _, item := range a.available {
			if item.State == "unknown" {
				ui.Text(c, fmt.Sprintf("%s：%s", item.Name, item.Reason)).FontSize(12).TextColor(t.TextMuted)
			}
		}
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
			if ui.Button(c.Key("device-info"), "系统兼容信息").Disabled(a.busy).Clicked() {
				a.showDeviceInfo()
			}
			if ui.Button(c.Key("install-apk"), "安装 APK…").Disabled(a.busy).Clicked() {
				a.chooseAPK(false)
			}
			if ui.Button(c.Key("update-apk"), "更新 APK…").Disabled(a.busy).Clicked() {
				a.chooseAPK(true)
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("backup-instance"), "备份选中实例…").Disabled(a.busy).Clicked() {
				a.transferBackup(false)
			}
			if ui.Button(c.Key("restore-instance"), "恢复备份到新实例…").Disabled(a.busy).Clicked() {
				a.transferBackup(true)
			}
			if a.backupCancel != nil && ui.Button(c.Key("cancel-backup"), "取消备份或恢复").Clicked() {
				a.backupCancel()
				a.message = "正在取消，请稍候…"
			}
		})
		ui.Text(c, "备份未加密，包含应用数据；恢复请先填写新的实例名称。").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("core-app-status"), "常用应用状态").Disabled(a.busy).Clicked() {
				a.showCoreAppStatus()
			}
			if ui.Button(c.Key("open-chrome"), "打开 Chrome").Disabled(a.busy).Clicked() {
				a.openCoreApp("chrome", "Chrome")
			}
			if ui.Button(c.Key("open-play"), "打开 Google Play").Disabled(a.busy).Clicked() {
				a.openCoreApp("play", "Google Play")
			}
			if ui.Button(c.Key("open-taptap"), "打开 TapTap").Disabled(a.busy).Clicked() {
				a.openCoreApp("taptap", "TapTap")
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("install-taptap"), "安装 TapTap…").Disabled(a.busy).Clicked() {
				a.installTapTap()
			}
			if ui.Button(c.Key("download-wechat"), "微信官方下载…").Disabled(a.busy).Clicked() {
				a.openOptionalDownload("wechat", "微信")
			}
			if ui.Button(c.Key("download-douyin"), "抖音官方下载…").Disabled(a.busy).Clicked() {
				a.openOptionalDownload("douyin", "抖音")
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
				if ui.Button(c.Key("migration-check-"+instance.Index), "检查迁移条件").Disabled(a.busy).Clicked() {
					a.checkMuMuMigration(instance)
				}
			})
		}
		ui.Text(c, "独立实例使用本机 Android SDK；下方实例使用 MuMu 引擎。").FontSize(12).TextColor(t.TextMuted)
		if a.updateMessage != "" {
			ui.Text(c, a.updateMessage).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

func lookupUpdate(skipOnce bool) (*update.Manifest, string, string) {
	pending, pendingErr := update.PendingReboot()
	if pendingErr != nil {
		return nil, "", "更新状态读取失败: " + pendingErr.Error()
	}
	if pending {
		return nil, "", "更新已安装，请重启 Windows 完成更新"
	}
	if skipOnce && len(os.Args) > 1 && os.Args[1] == "--skip-update-once" {
		b, _ := os.ReadFile(filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "last-update.txt"))
		return nil, "", string(b)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, "", "更新检查失败: " + err.Error()
	}
	installed := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233", "NekoSimulator233.exe")
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(installed)) {
		return nil, "", ""
	}
	manifest := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "update.json")
	m, err := update.Load(manifest, version)
	if err != nil {
		return nil, "", "更新检查失败: " + err.Error()
	}
	if m == nil {
		return nil, "", ""
	}
	return m, executable, ""
}

func prepareUpdate() (bool, string) {
	m, executable, message := lookupUpdate(true)
	if m == nil {
		return false, message
	}
	if err := update.Start(m, version, executable); err != nil {
		return false, "更新准备失败: " + err.Error()
	}
	return true, ""
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--vulkan-preview-worker" {
		if err := vulkan.RunPreview(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--update-worker" {
		_ = update.Worker(os.Args[2:])
		return
	}
	started, updateMessage := prepareUpdate()
	if started {
		return
	}
	a := &app{message: "正在连接…", updateMessage: updateMessage}
	if len(os.Args) > 1 && os.Args[1] == "--skip-update-once" && strings.HasPrefix(updateMessage, "更新未完成") && !strings.Contains(updateMessage, "is still running; update deferred") {
		m, _, _ := lookupUpdate(false)
		a.suppressedUpdate = updateKey(m)
	}
	mygo.App.WhenReady(func() {
		a.window = mygo.NewWindow(mygo.WindowOptions{Title: "NekoSimulator233", Width: 760, Height: 500, Content: ui.View(a.view)})
		a.window.OnClose(func(e *mygo.CloseEvent) {
			if a.updateInProgress {
				a.closeAfterUpdate = true
				a.message = "正在完成更新检查，结束后关闭…"
				e.PreventDefault()
				return
			}
			if a.cancelBackupForClose() {
				e.PreventDefault()
				return
			}
			if a.cancelSDKForClose() {
				e.PreventDefault()
			}
		})
		a.refresh()
		a.startPeriodicUpdates()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
