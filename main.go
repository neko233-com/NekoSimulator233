package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moriyoshi/gpui-go/client"
	"github.com/moriyoshi/gpui-go/host"
	"github.com/moriyoshi/gpui-go/scene"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/macro"
	"github.com/neko233-com/NekoSimulator233/internal/mcpproxy"
	"github.com/neko233-com/NekoSimulator233/internal/runtimeinstall"
	"github.com/neko233-com/NekoSimulator233/internal/syncctl"
	"github.com/neko233-com/NekoSimulator233/internal/update"
	"github.com/neko233-com/NekoSimulator233/internal/viewlayout"
	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

var version = "0.0.0"

const (
	idSearchInput    uint64 = 101
	idNewNameInput   uint64 = 102
	idCloneNameInput uint64 = 103
	idRenameInput    uint64 = 104
	idManifestInput  uint64 = 105
)

type app struct {
	ctx        context.Context
	cancelApp  context.CancelFunc
	cli        *client.Client
	win        *client.Window
	icons      *iconBank
	uiQueue    chan func()
	dispatchFn map[uint64]func(*client.Event)

	busy          bool
	message       string
	updateMessage string

	sideTab        string
	available      []avd.InstanceSummary
	selected       string
	deviceSearch   scene.InputState
	deviceSort     string
	deviceGridView bool

	showCreate  bool
	newInstance scene.InputState
	newSystem   string

	batchPanel    bool
	batchSelected map[string]bool

	moreMenuDevice string
	cloneDevice    string
	cloneName      scene.InputState
	renameDevice   string
	renameName     scene.InputState

	thumbs       map[string]*client.Resource
	thumbPending map[string]bool

	viewers      map[string]*emulatorViewer
	starting     map[string]bool
	settingsWins map[string]*deviceSettingsUI
	syncHub      *syncctl.Hub
	macroStore   *macro.Store

	runtimeManifest scene.InputState
	runtimeCancel   context.CancelFunc
	sdkCancel       context.CancelFunc
	backupCancel    context.CancelFunc
	needsBootstrap  bool
	pendingLaunch   string
	bootstrapStage  string
	bootstrapPct    int
	ecosystem       mcpproxy.EcosystemReport
	tray            *trayController

	// Event handler IDs for GPUI inputs
	hSearch   uint64
	hNewName  uint64
	hClone    uint64
	hRename   uint64
	hManifest uint64
}

func main() {
	if os.Getenv("NEKO_PREFER_USERDEBUG_IMAGE") == "" {
		_ = os.Setenv("NEKO_PREFER_USERDEBUG_IMAGE", "1")
	}
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

	var launchTarget string
	for i := 1; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--launch" {
			launchTarget = strings.TrimSpace(os.Args[i+1])
		}
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	err := host.Run(rootCtx, func(ctx context.Context, cli *client.Client) error {
		a := newApp(ctx, rootCancel, cli, updateMessage)
		if err := a.start(launchTarget); err != nil {
			return err
		}
		return a.runLoop()
	})
	if err != nil {
		log.Fatal(err)
	}
}

func newApp(ctx context.Context, cancel context.CancelFunc, cli *client.Client, updateMsg string) *app {
	a := &app{
		ctx:           ctx,
		cancelApp:     cancel,
		cli:           cli,
		uiQueue:       make(chan func(), 256),
		dispatchFn:    make(map[uint64]func(*client.Event)),
		sideTab:       "设备",
		deviceSort:    deviceSortName,
		newSystem:     systemAuto,
		updateMessage: updateMsg,
		thumbs:        make(map[string]*client.Resource),
		thumbPending:  make(map[string]bool),
		viewers:       make(map[string]*emulatorViewer),
		settingsWins:  make(map[string]*deviceSettingsUI),
		batchSelected: make(map[string]bool),
		syncHub:       syncctl.NewHub(),
		macroStore:    macro.NewStore(""),
		ecosystem:     mcpproxy.DetectEcosystem(),
	}
	a.hSearch = cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
		if ch := ev.GetTextChanged(); ch != nil && a.deviceSearch.Accept(ch) {
			a.render()
		}
		return nil
	})
	a.hNewName = cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
		if ch := ev.GetTextChanged(); ch != nil {
			a.newInstance.Accept(ch)
		}
		return nil
	})
	a.hClone = cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
		if ch := ev.GetTextChanged(); ch != nil {
			a.cloneName.Accept(ch)
		}
		return nil
	})
	a.hRename = cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
		if ch := ev.GetTextChanged(); ch != nil {
			a.renameName.Accept(ch)
		}
		return nil
	})
	a.hManifest = cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
		if ch := ev.GetTextChanged(); ch != nil {
			a.runtimeManifest.Accept(ch)
		}
		return nil
	})
	return a
}

func (a *app) start(launchTarget string) error {
	a.icons = loadIconBank(a.ctx, a.cli)
	win, err := a.cli.OpenWindow(a.ctx, client.WindowOptions{
		Title:  "NekoSimulator233",
		Width:  940,
		Height: 620,
	}, a.buildView())
	if err != nil {
		return err
	}
	a.win = win
	a.tray = startSystemTray(
		func() { _ = showOrHideWindowByTitle("NekoSimulator233", true) },
		func() { _ = showOrHideWindowByTitle("NekoSimulator233", false) },
		func() {
			a.post(func() {
				for _, item := range a.available {
					if item.State == "running" {
						a.stopInstance(item.Name)
					}
				}
			})
		},
		func() {
			a.post(func() {
				if a.tray != nil {
					a.tray.Close()
				}
				a.cancelApp()
			})
		},
	)
	a.refresh()
	if launchTarget != "" {
		a.selected = launchTarget
		a.startInstance(launchTarget)
	}
	return nil
}

func (a *app) post(fn func()) {
	if fn == nil {
		return
	}
	select {
	case a.uiQueue <- fn:
	case <-a.ctx.Done():
	}
}

func (a *app) runLoop() error {
	evCh := make(chan []*client.Event, 32)
	errCh := make(chan error, 1)
	go func() {
		for {
			batch, err := a.cli.NextEvents(a.ctx)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			select {
			case evCh <- batch:
			case <-a.ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-a.ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case fn := <-a.uiQueue:
			fn()
		case batch := <-evCh:
			for _, ev := range batch {
				if ev.GetWindowClosed() != nil && a.win != nil && ev.GetWindow().GetId() == a.win.Handle().GetId() {
					a.closeAllViewers()
					return a.cli.Quit(a.ctx)
				}
				if err := a.cli.Dispatch(a.ctx, ev); err != nil {
					return err
				}
				a.routeEvent(ev)
			}
		}
	}
}

func (a *app) routeEvent(ev *client.Event) {
	if fn, ok := a.dispatchFn[ev.GetHandlerId()]; ok && fn != nil {
		fn(ev)
		return
	}
	for _, v := range a.viewers {
		if v.handleEvent(ev) {
			return
		}
	}
	for _, s := range a.settingsWins {
		if s.handleEvent(ev) {
			return
		}
	}
}

func (a *app) render() {
	if a.win == nil {
		return
	}
	// Reset per-snapshot handler map for the main manager window.
	a.dispatchFn = make(map[uint64]func(*client.Event))
	_ = a.win.Set(a.ctx, a.buildView())
}

func (a *app) click(fn func()) uint64 {
	// GPUI handlers are registered lazily on Client and cleared logically by
	// tracking active IDs in dispatchFn for the current view snapshot.
	id := a.cli.OnClick(func(_ context.Context, _ *client.Event) error { return nil })
	a.dispatchFn[id] = func(_ *client.Event) {
		if fn != nil {
			fn()
		}
	}
	return id
}

func (a *app) selectedName() string {
	if a.selected == "" {
		return avd.DefaultName
	}
	return a.selected
}

func (a *app) setAvailable(items []avd.InstanceSummary) {
	a.available = items
	for _, item := range items {
		if strings.EqualFold(item.Name, a.selectedName()) {
			a.selected = item.Name
			return
		}
	}
	if len(items) > 0 {
		a.selected = items[0].Name
	}
}

func (a *app) markInstanceState(name, state string) {
	for i := range a.available {
		if strings.EqualFold(a.available[i].Name, name) {
			a.available[i].State = state
			a.available[i].Reason = ""
			return
		}
	}
}

func (a *app) refresh() {
	if a.busy {
		return
	}
	a.busy = true
	a.message = "正在刷新本地设备列表…"
	a.render()
	go func() {
		if exe, err := os.Executable(); err == nil {
			_ = avd.PrepareBundledRuntime(context.Background(), filepath.Join(filepath.Dir(exe), "android-runtime.zip"), os.Getenv("LOCALAPPDATA"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var items []avd.InstanceSummary
		var err error
		if manager, discErr := avd.DiscoverManager(); discErr == nil {
			items, err = manager.ListInstances(ctx)
		} else {
			home, _ := avd.LocalAVDHome()
			items, err = avd.ListLocalInstances(ctx, home)
		}
		eco := mcpproxy.DetectEcosystem()
		a.post(func() {
			a.busy = false
			a.ecosystem = eco
			if err != nil {
				a.message = "读取设备列表失败：" + err.Error()
			} else {
				a.setAvailable(items)
				a.message = fmt.Sprintf("已加载 %d 台本地 Android 设备（100%% 纯净无广告）", len(items))
			}
			a.render()
		})
	}()
}

func (a *app) startInstance(name string) {
	if name == "" {
		name = a.selectedName()
	}
	key := independentViewerKey(name)
	if v := a.viewers[key]; v != nil {
		focusWindowByTitle(v.title)
		return
	}
	if a.starting == nil {
		a.starting = make(map[string]bool)
	}
	if a.starting[key] {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.starting[key] = true
	a.sdkCancel = cancel
	a.message = fmt.Sprintf("正在启动 %s（Android 15 · 2c4g · Vulkan 加速）…", name)
	a.render()

	go func() {
		manager, err := selectedManager(name)
		if err != nil && avd.IsMissingSDKError(err) {
			a.post(func() {
				a.bootstrapStage = "正在首次自动部署 Android 15 运行环境…"
				a.bootstrapPct = 1
				a.message = a.bootstrapStage
				a.render()
			})
			bootErr := avd.BootstrapRuntime(ctx, "", func(stage string, pct int) {
				a.post(func() {
					if ctx.Err() == nil {
						a.bootstrapStage = stage
						a.bootstrapPct = pct
						a.message = fmt.Sprintf("首次初始化 %s：%s (%d%%)", name, stage, pct)
						a.render()
					}
				})
			})
			a.post(func() {
				a.bootstrapStage = ""
				a.bootstrapPct = 0
			})
			if bootErr != nil {
				err = bootErr
			} else {
				manager, err = selectedManager(name)
			}
		}
		var instance avd.Instance
		if err == nil {
			instance, err = manager.StartWithProgress(ctx, func(percent int) {
				a.post(func() {
					if a.sdkCancel != nil && ctx.Err() == nil {
						a.message = fmt.Sprintf("正在准备 %s 运行环境：%d%%", name, percent)
						a.render()
					}
				})
			})
		}
		var source *avd.Controller
		var bindingsPath string
		if err == nil {
			source, err = instance.Open()
		}
		if err == nil {
			bindingsPath, err = resolveKeymapPathForInstance(name)
			if err != nil && source != nil {
				_ = source.Close()
			}
		}
		a.post(func() {
			defer cancel()
			delete(a.starting, key)
			a.sdkCancel = nil
			if err != nil {
				if avd.IsMissingSDKError(err) {
					a.needsBootstrap = true
					a.pendingLaunch = name
					a.message = "未检测到 Android 运行时，请点击「一键部署 Android 运行时并启动」"
				} else {
					a.message = "启动失败：" + err.Error()
				}
				a.render()
				return
			}
			a.needsBootstrap = false
			a.pendingLaunch = ""
			a.markInstanceState(name, "running")
			a.message = fmt.Sprintf("%s 已启动 · Vulkan: %s", name, instance.VulkanDevice)
			v := openEmulatorViewer(a, name, source, bindingsPath, instance)
			if v != nil {
				a.viewers[key] = v
			}
			a.render()
		})
	}()
}

func (a *app) bootstrapAndLaunch() {
	if a.busy {
		return
	}
	target := a.pendingLaunch
	if target == "" {
		target = a.selectedName()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.busy = true
	a.sdkCancel = cancel
	a.bootstrapStage = "准备部署 Android 运行时…"
	a.bootstrapPct = 1
	a.message = a.bootstrapStage
	a.render()
	go func() {
		err := avd.BootstrapRuntime(ctx, "", func(stage string, pct int) {
			a.post(func() {
				if ctx.Err() == nil {
					a.bootstrapStage = stage
					a.bootstrapPct = pct
					a.message = fmt.Sprintf("%s (%d%%)", stage, pct)
					a.render()
				}
			})
		})
		a.post(func() {
			defer cancel()
			a.busy = false
			a.sdkCancel = nil
			a.bootstrapStage = ""
			a.bootstrapPct = 0
			if err != nil {
				a.message = "运行时部署失败：" + err.Error()
				a.render()
				return
			}
			a.needsBootstrap = false
			a.pendingLaunch = ""
			a.message = "Android 运行时部署完成，正在启动 " + target + "…"
			a.render()
			if target != "" {
				a.startInstance(target)
			} else {
				a.refresh()
			}
		})
	}()
}

func (a *app) restartInstance(name string) {
	if a.busy {
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在重启 %s…", name)
	a.render()
	go func() {
		manager, err := selectedManager(name)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err = manager.RestartInstance(ctx)
			cancel()
		}
		a.post(func() {
			a.busy = false
			if err != nil {
				a.message = "重启失败：" + err.Error()
			} else {
				a.message = name + " 已重启"
			}
			a.refresh()
		})
	}()
}

func (a *app) trimInstance(name string) {
	if a.busy {
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在清理 %s 磁盘空间…", name)
	a.render()
	go func() {
		manager, err := selectedManager(name)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err = manager.TrimDisk(ctx, avd.Instance{Name: name})
			cancel()
		}
		a.post(func() {
			a.busy = false
			if err != nil {
				a.message = "磁盘清理失败：" + err.Error()
			} else {
				a.message = name + " 磁盘清理完成"
			}
			a.render()
		})
	}()
}

func (a *app) resetInstanceData(name string) {
	if a.busy {
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在重置 %s 数据…", name)
	a.render()
	go func() {
		home, err := avd.LocalAVDHome()
		if err == nil {
			avdDir := filepath.Join(home, name+".avd")
			for _, pat := range []string{"userdata-qemu.img*", "cache.img*", "sdcard.img*"} {
				matches, _ := filepath.Glob(filepath.Join(avdDir, pat))
				for _, m := range matches {
					_ = os.RemoveAll(m)
				}
			}
			_ = os.RemoveAll(filepath.Join(avdDir, "snapshots"))
		}
		a.post(func() {
			a.busy = false
			if err != nil {
				a.message = "重置失败：" + err.Error()
			} else {
				a.message = name + " 已重置为出厂状态"
			}
			a.refresh()
		})
	}()
}

func (a *app) restoreBackupPrompt(name string) {
	_, _ = a.cli.PromptPaths(a.ctx, &client.PathPrompt{Files: true, Multiple: false, Prompt: "选择要恢复的 .nekobak 备份文件"}, func(_ context.Context, res *client.PathResult) error {
		if res == nil || res.Cancelled || len(res.Paths) == 0 {
			return nil
		}
		path := res.Paths[0]
		a.post(func() {
			if a.busy {
				return
			}
			a.busy = true
			a.message = "正在恢复备份到 " + name + "…"
			a.render()
			go func() {
				manager, err := selectedManager(name)
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					err = manager.ImportBackup(ctx, path)
					cancel()
				}
				a.post(func() {
					a.busy = false
					if err != nil {
						a.message = "恢复备份失败：" + err.Error()
					} else {
						a.message = "已恢复备份：" + name
					}
					a.refresh()
				})
			}()
		})
		return nil
	})
}

func (a *app) stopInstance(name string) {
	if a.busy {
		return
	}
	if name == "" {
		name = a.selectedName()
	}
	key := independentViewerKey(name)
	if v := a.viewers[key]; v != nil {
		v.closeWindow()
	}
	a.busy = true
	a.message = fmt.Sprintf("正在关闭 %s…", name)
	a.render()
	go func() {
		manager, err := selectedManager(name)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err = manager.Stop(ctx)
			cancel()
		}
		a.post(func() {
			a.busy = false
			if err != nil {
				a.message = "关闭失败：" + err.Error()
			} else {
				a.markInstanceState(name, "stopped")
				a.message = name + " 已安全关机"
			}
			a.render()
		})
	}()
}

func (a *app) createInstance() {
	if a.busy {
		return
	}
	name := strings.TrimSpace(a.newInstance.Value)
	if name == "" {
		a.message = "请输入新设备名称（字母/数字/下划线/连字符）"
		a.render()
		return
	}
	api, err := newSystemAPI(a.newSystem)
	if err != nil {
		a.message = err.Error()
		a.render()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.busy = true
	a.sdkCancel = cancel
	a.message = fmt.Sprintf("正在创建设备 %s…", name)
	a.render()
	go func() {
		defer cancel()
		manager, err := avd.DiscoverManager()
		var items []avd.InstanceSummary
		if err == nil {
			if os.Getenv("NEKO_PREFER_USERDEBUG_IMAGE") == "1" {
				err = manager.CreateInstanceAPI(ctx, name, api)
			} else {
				err = manager.CreatePlayInstance(ctx, name, api, func(percent int) {
					a.post(func() {
						a.message = fmt.Sprintf("正在准备系统镜像：%d%%", percent)
						a.render()
					})
				})
			}
			if err == nil {
				items, _ = manager.ListInstances(ctx)
			}
		}
		a.post(func() {
			a.busy = false
			a.sdkCancel = nil
			if err != nil {
				a.message = "创建设备失败：" + err.Error()
			} else {
				a.showCreate = false
				a.newInstance.Reset("")
				if len(items) > 0 {
					a.setAvailable(items)
				}
				a.selected = name
				a.message = fmt.Sprintf("设备 %s 已创建", name)
			}
			a.render()
		})
	}()
}

func (a *app) cloneInstance(srcName string) {
	dstName := strings.TrimSpace(a.cloneName.Value)
	if dstName == "" {
		a.message = "请输入克隆后的新设备名称"
		a.render()
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在克隆 %s → %s…", srcName, dstName)
	a.render()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		m, err := avd.DiscoverManagerOrOffline()
		if m.Home != "" {
			err = m.CloneInstance(ctx, srcName, dstName)
		}
		var items []avd.InstanceSummary
		if err == nil {
			items, _ = avd.ListLocalInstances(ctx, m.Home)
		}
		a.post(func() {
			a.busy = false
			if err != nil {
				a.message = "克隆失败：" + err.Error()
			} else {
				a.cloneDevice = ""
				a.cloneName.Reset("")
				if len(items) > 0 {
					a.setAvailable(items)
				}
				a.message = fmt.Sprintf("已克隆 %s 为 %s", srcName, dstName)
			}
			a.render()
		})
	}()
}

func (a *app) renameInstance(oldName string) {
	newName := strings.TrimSpace(a.renameName.Value)
	if newName == "" {
		a.message = "请输入新的设备名称"
		a.render()
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在重命名 %s → %s…", oldName, newName)
	a.render()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m, _ := avd.DiscoverManagerOrOffline()
		err := m.RenameInstance(ctx, oldName, newName)
		var items []avd.InstanceSummary
		if err == nil {
			items, _ = avd.ListLocalInstances(ctx, m.Home)
		}
		a.post(func() {
			a.busy = false
			if err != nil {
				a.message = "重命名失败：" + err.Error()
			} else {
				a.renameDevice = ""
				a.renameName.Reset("")
				if len(items) > 0 {
					a.setAvailable(items)
				}
				a.selected = newName
				a.message = fmt.Sprintf("已重命名为 %s", newName)
			}
			a.render()
		})
	}()
}

func (a *app) deleteInstance(name string) {
	a.busy = true
	a.moreMenuDevice = ""
	a.message = fmt.Sprintf("正在删除 %s…", name)
	a.render()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m, _ := avd.DiscoverManagerOrOffline()
		err := m.DeleteInstance(ctx, name)
		var items []avd.InstanceSummary
		if err == nil {
			items, _ = avd.ListLocalInstances(ctx, m.Home)
		}
		a.post(func() {
			a.busy = false
			if err != nil {
				a.message = "删除失败：" + err.Error()
			} else {
				if len(items) > 0 {
					a.setAvailable(items)
				}
				a.message = fmt.Sprintf("已删除设备 %s", name)
			}
			a.render()
		})
	}()
}

func (a *app) arrangeWindows() {
	if len(a.viewers) == 0 {
		a.message = "没有打开的模拟器窗口可排列"
		a.render()
		return
	}
	keys := make([]string, 0, len(a.viewers))
	for k := range a.viewers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	work := primaryWorkArea()
	tiles := viewlayout.Tile(len(keys), work)
	arranged := 0
	for i, k := range keys {
		if v := a.viewers[k]; v != nil {
			t := tiles[i]
			if setWindowBoundsByTitle(v.title, t.X, t.Y, t.Width, t.Height) {
				arranged++
			}
		}
	}
	a.message = fmt.Sprintf("已平铺排列 %d 个模拟器窗口", arranged)
	a.render()
}

func (a *app) batchStart() {
	if a.busy {
		return
	}
	var targets []string
	for _, item := range a.available {
		if (len(a.batchSelected) == 0 || a.batchSelected[item.Name]) && (item.State == "stopped" || item.State == "not_created") {
			targets = append(targets, item.Name)
		}
	}
	if len(targets) == 0 {
		a.message = "没有可批量开机的设备"
		a.render()
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在批量开机：共 %d 台设备…", len(targets))
	a.render()
	go func() {
		started, failed := 0, 0
		for _, name := range targets {
			inst, bindings, err := startInstanceForBatch(name)
			if err != nil {
				failed++
				continue
			}
			started++
			a.post(func() {
				key := independentViewerKey(name)
				if a.viewers[key] == nil {
					if src, openErr := inst.Open(); openErr == nil {
						if v := openEmulatorViewer(a, name, src, bindings, inst); v != nil {
							a.viewers[key] = v
						}
					}
				}
				a.markInstanceState(name, "running")
				a.message = fmt.Sprintf("批量开机中… 已启动 %d，失败 %d", started, failed)
				a.render()
			})
		}
		a.post(func() {
			a.busy = false
			a.message = fmt.Sprintf("批量开机完成：成功 %d，失败 %d", started, failed)
			a.render()
		})
	}()
}

func (a *app) batchStop() {
	if a.busy {
		return
	}
	var targets []string
	for _, item := range a.available {
		if (len(a.batchSelected) == 0 || a.batchSelected[item.Name]) && item.State == "running" {
			targets = append(targets, item.Name)
		}
	}
	if len(targets) == 0 {
		a.message = "没有运行中的设备可关闭"
		a.render()
		return
	}
	a.busy = true
	a.message = fmt.Sprintf("正在批量关机：共 %d 台设备…", len(targets))
	for _, name := range targets {
		if v := a.viewers[independentViewerKey(name)]; v != nil {
			v.closeWindow()
		}
	}
	a.render()
	go func() {
		stopped, failed := 0, 0
		for _, name := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			m, err := selectedManager(name)
			if err == nil {
				err = m.Stop(ctx)
			}
			cancel()
			if err != nil {
				failed++
			} else {
				stopped++
				a.post(func() { a.markInstanceState(name, "stopped") })
			}
		}
		a.post(func() {
			a.busy = false
			a.message = fmt.Sprintf("批量关机完成：成功 %d，失败 %d", stopped, failed)
			a.render()
		})
	}()
}

func (a *app) downloadRuntime() {
	if a.busy || a.runtimeCancel != nil {
		return
	}
	manifest := strings.TrimSpace(a.runtimeManifest.Value)
	if manifest == "" {
		a.message = "请填写运行时清单 URL 或本地 manifest.json 路径"
		a.render()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runtimeCancel = cancel
	a.busy = true
	a.message = "正在下载并校验 Android 运行组件…"
	a.render()
	go func() {
		doc, err := runtimeinstall.LoadManifest(ctx, manifest)
		if err == nil {
			err = runtimeinstall.Download(ctx, doc, filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "runtime", "sdk"))
		}
		a.post(func() {
			a.runtimeCancel = nil
			a.busy = false
			if err != nil {
				a.message = "运行组件下载失败：" + err.Error()
			} else {
				a.message = "Android 运行组件安装完成"
				a.refresh()
			}
			a.render()
		})
	}()
}

func (a *app) exportBackup(name string) {
	if a.busy {
		return
	}
	_, _ = a.cli.PromptSavePath(a.ctx, &client.SavePathPrompt{
		SuggestedName: name + ".nekobak",
	}, func(_ context.Context, res *client.PathResult) error {
		if res == nil || res.Cancelled || len(res.Paths) == 0 {
			return nil
		}
		dst := res.Paths[0]
		if filepath.Ext(dst) == "" {
			dst += ".nekobak"
		}
		a.busy = true
		a.message = fmt.Sprintf("正在导出备份 %s…", dst)
		a.render()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			m, err := selectedManager(name)
			if err == nil {
				err = m.ExportBackup(ctx, dst)
			}
			a.post(func() {
				a.busy = false
				if err != nil {
					a.message = "导出备份失败：" + err.Error()
				} else {
					a.message = "备份完成：" + dst
				}
				a.render()
			})
		}()
		return nil
	})
}

func (a *app) ensureThumb(name string) {
	if a.thumbs[name] != nil || a.thumbPending[name] {
		return
	}
	// First try loading cached last-frame thumbnail from disk (MuMu boot_screenshot parity).
	thumbPath := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "avd", name+".avd", "neko-thumb.bmp")
	if b, err := os.ReadFile(thumbPath); err == nil && len(b) > 54 {
		if res, upErr := a.cli.UploadResource(a.ctx, client.ResourceImage, "image/bmp", b); upErr == nil {
			a.thumbs[name] = res
			return
		}
	}
	a.thumbPending[name] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		m, err := selectedManager(name)
		var bmp []byte
		if err == nil {
			if inst, readyErr := m.Ready(ctx); readyErr == nil {
				if ctrl, openErr := inst.Open(); openErr == nil {
					if img, capErr := ctrl.Capture(nil); capErr == nil {
						bmp = encodeRGBAAsBMP(img)
					}
					_ = ctrl.Close()
				}
			}
		}
		if len(bmp) > 0 {
			_ = os.MkdirAll(filepath.Dir(thumbPath), 0o700)
			_ = os.WriteFile(thumbPath, bmp, 0o600)
		}
		a.post(func() {
			delete(a.thumbPending, name)
			if len(bmp) > 0 {
				if res, upErr := a.cli.UploadResource(a.ctx, client.ResourceImage, "image/bmp", bmp); upErr == nil {
					old := a.thumbs[name]
					a.thumbs[name] = res
					a.render()
					if old != nil {
						_ = old.Release(a.ctx)
					}
				}
			}
		})
	}()
}

func (a *app) closeAllViewers() {
	for _, v := range a.viewers {
		if v != nil {
			v.stop()
		}
	}
}

// buildView renders Figure 1: clean, light, ad-free MuMu-style Entry Manager.
func (a *app) buildView() *scene.Node {
	return scene.Div(1,
		a.buildSidebar(),
		a.buildMainArea(),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexRow.Enum(),
		Width:         scene.Percent(100),
		Height:        scene.Percent(100),
		Background:    scene.RGB(colBgPage),
		TextColor:     scene.RGB(colTextPrimary),
		FontFamily:    scene.Ptr[string](uiFontFamily),
	})
}

func (a *app) buildSidebar() *scene.Node {
	navItem := func(icon *client.Resource, label string) *scene.Node {
		active := a.sideTab == label
		bg := uint32(colBgSidebar)
		fg := uint32(colTextMuted)
		if active {
			bg = colBgHover
			fg = colPrimaryBlue
		}
		return scene.Div(eid(),
			svgIcon(icon, 20, fg),
			labelText(label, 12, fg, active),
		).Focusable(0).OnClick(a.click(func() {
			a.sideTab = label
			a.moreMenuDevice = ""
			a.render()
		})).Accessible(label, &scene.Accessibility{Role: scene.RoleButton, Selected: scene.Ptr(active)}).
			Style(&scene.Style{
				Display:        scene.DisplayFlex.Enum(),
				FlexDirection:  scene.FlexColumn.Enum(),
				AlignItems:     scene.AlignCenter.Enum(),
				JustifyContent: scene.JustifyCenter.Enum(),
				Width:          scene.Pixels(60),
				Height:         scene.Pixels(56),
				Gap:            scene.Pixels(4),
				Background:     scene.RGB(bg),
				CornerRadius:   scene.Ptr[float32](10),
				Cursor:         scene.CursorPointer.Enum(),
			}).Hover(&scene.Style{Background: scene.RGB(colBgHover)})
	}

	return scene.Div(eid(),
		scene.Div(eid(),
			rasterIcon(a.icons.Logo, 36),
		).Style(&scene.Style{
			Padding:        &scene.Edges{Top: scene.Pixels(6), Bottom: scene.Pixels(10)},
			AlignItems:     scene.AlignCenter.Enum(),
			JustifyContent: scene.JustifyCenter.Enum(),
		}),
		navItem(a.icons.Device, "设备"),
		navItem(a.icons.Remote, "远控"),
		navItem(a.icons.Tools, "工具"),
		navItem(a.icons.Settings, "设置"),
		scene.Div(eid()).Style(&scene.Style{FlexGrow: scene.Ptr[float32](1)}),
		pillButton("托盘", a.click(func() {
			_ = showOrHideWindowByTitle("NekoSimulator233", false)
		}), colBgSubtle, colBgHover, colTextMuted, 0),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		AlignItems:    scene.AlignCenter.Enum(),
		Width:         scene.Pixels(76),
		Height:        scene.Percent(100),
		Padding:       &scene.Edges{Top: scene.Pixels(10), Bottom: scene.Pixels(12), Left: scene.Pixels(8), Right: scene.Pixels(8)},
		Gap:           scene.Pixels(8),
		Background:    scene.RGB(colBgSidebar),
		BorderWidth:   scene.Ptr[float32](1),
		BorderColor:   scene.RGB(colBorderLight),
		FlexShrink:    scene.Ptr[float32](0),
	})
}

func (a *app) buildMainArea() *scene.Node {
	var body *scene.Node
	switch a.sideTab {
	case "远控":
		body = a.buildRemoteView()
	case "工具":
		body = a.buildToolsView()
	case "设置":
		body = a.buildGlobalSettingsView()
	default:
		body = a.buildDevicesView()
	}

	footerText := a.message
	if a.updateMessage != "" {
		footerText += "  |  " + a.updateMessage
	}

	return scene.Div(eid(),
		body,
		scene.Div(eid(),
			labelText(footerText, 12, colTextMuted, false),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Width:         scene.Percent(100),
			Padding:       &scene.Edges{Top: scene.Pixels(6), Bottom: scene.Pixels(6), Left: scene.Pixels(20), Right: scene.Pixels(20)},
			Background:    scene.RGB(colBgCard),
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
		}),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		FlexGrow:      scene.Ptr[float32](1),
		Height:        scene.Percent(100),
	})
}

func (a *app) buildDevicesView() *scene.Node {
	sortNext := func() {
		switch a.deviceSort {
		case deviceSortName:
			a.deviceSort = deviceSortState
		case deviceSortState:
			a.deviceSort = deviceSortSystem
		default:
			a.deviceSort = deviceSortName
		}
		a.render()
	}

	toolbar := scene.Div(eid(),
		scene.Div(eid(),
			iconPillButton(a.icons.Plus, "新建设备", a.click(func() {
				a.showCreate = !a.showCreate
				a.render()
			}), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			iconPillButton(a.icons.Batch, "批量操作", a.click(func() {
				a.batchPanel = !a.batchPanel
				a.render()
			}), colBgCard, colBgHover, colTextPrimary, colBorderLight),
			iconPillButton(a.icons.Arrange, "排列窗口", a.click(a.arrangeWindows), colBgCard, colBgHover, colTextPrimary, colBorderLight),
			iconPillButton(a.icons.Refresh, "刷新", a.click(a.refresh), colBgCard, colBgHover, colTextPrimary, colBorderLight),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(8),
		}),
		scene.Div(eid(),
			scene.Div(eid(),
				svgIcon(a.icons.Search, 14, colTextMuted),
				scene.TextInput(idSearchInput, a.deviceSearch, scene.TextInputOptions{
					Placeholder: "搜索设备…",
					OnChange:    a.hSearch,
				}).Style(&scene.Style{
					Width:      scene.Pixels(150),
					FontSize:   scene.Ptr[float32](13),
					TextColor:  scene.RGB(colTextPrimary),
					FontFamily: scene.Ptr[string](uiFontFamily),
				}),
			).Style(&scene.Style{
				Display:       scene.DisplayFlex.Enum(),
				FlexDirection: scene.FlexRow.Enum(),
				AlignItems:    scene.AlignCenter.Enum(),
				Gap:           scene.Pixels(6),
				Padding:       &scene.Edges{Top: scene.Pixels(5), Bottom: scene.Pixels(5), Left: scene.Pixels(10), Right: scene.Pixels(10)},
				Background:    scene.RGB(colBgCard),
				BorderWidth:   scene.Ptr[float32](1),
				BorderColor:   scene.RGB(colBorderLight),
				CornerRadius:  scene.Ptr[float32](8),
			}),
			iconPillButton(a.icons.Sort, "排序: "+a.deviceSort, a.click(sortNext), colBgCard, colBgHover, colTextPrimary, colBorderLight),
			iconPillButton(func() *client.Resource {
				if a.deviceGridView {
					return a.icons.Grid
				}
				return a.icons.List
			}(), "", a.click(func() {
				a.deviceGridView = !a.deviceGridView
				a.render()
			}), colBgCard, colBgHover, colTextPrimary, colBorderLight),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(8),
		}),
	).Style(&scene.Style{
		Display:        scene.DisplayFlex.Enum(),
		FlexDirection:  scene.FlexRow.Enum(),
		AlignItems:     scene.AlignCenter.Enum(),
		JustifyContent: scene.JustifySpaceBetween.Enum(),
		Width:          scene.Percent(100),
	})

	children := []*scene.Node{toolbar}

	if a.needsBootstrap || a.bootstrapStage != "" {
		title := "未检测到本地 Android SDK / Emulator 运行时"
		sub := "点击右侧按钮即可自动下载官方 Command-line Tools、ADB、Emulator 与 Android 15 系统镜像并立即启动。"
		if a.bootstrapStage != "" {
			title = fmt.Sprintf("%s（%d%%）", a.bootstrapStage, a.bootstrapPct)
			sub = "首次部署需要下载 Android 运行时组件，请保持网络连接…"
		}
		children = append(children, scene.Div(eid(),
			scene.Div(eid(),
				labelText(title, 13, colTextPrimary, true),
				labelText(sub, 12, colTextMuted, false),
			).Style(&scene.Style{
				Display:       scene.DisplayFlex.Enum(),
				FlexDirection: scene.FlexColumn.Enum(),
				FlexGrow:      scene.Ptr[float32](1),
				Gap:           scene.Pixels(2),
			}),
			pillButton("一键部署 Android 运行时并启动", a.click(a.bootstrapAndLaunch), colPrimaryBlue, colPrimaryHover, colWhite, 0),
		).Style(&scene.Style{
			Display:        scene.DisplayFlex.Enum(),
			FlexDirection:  scene.FlexRow.Enum(),
			AlignItems:     scene.AlignCenter.Enum(),
			JustifyContent: scene.JustifySpaceBetween.Enum(),
			Gap:            scene.Pixels(12),
			Padding:        scene.Padding(12),
			Background:     scene.RGB(0xEFF6FF),
			BorderWidth:    scene.Ptr[float32](1),
			BorderColor:    scene.RGB(colPrimaryBlue),
			CornerRadius:   scene.Ptr[float32](10),
		}))
	}

	if a.showCreate {
		sysBtn := func(label string) *scene.Node {
			active := a.newSystem == label
			bg, fg := uint32(colBgCard), uint32(colTextPrimary)
			if active {
				bg, fg = colPrimaryBlue, colWhite
			}
			return pillButton(label, a.click(func() {
				a.newSystem = label
				a.render()
			}), bg, colBgHover, fg, colBorderLight)
		}
		children = append(children, scene.Div(eid(),
			labelText("新建设备名称:", 13, colTextPrimary, true),
			scene.TextInput(idNewNameInput, a.newInstance, scene.TextInputOptions{
				Placeholder: "例如 MuMu-Device-1",
				OnChange:    a.hNewName,
			}).Style(&scene.Style{
				Width:        scene.Pixels(200),
				Padding:      &scene.Edges{Top: scene.Pixels(6), Bottom: scene.Pixels(6), Left: scene.Pixels(10), Right: scene.Pixels(10)},
				Background:   scene.RGB(colBgPage),
				BorderWidth:  scene.Ptr[float32](1),
				BorderColor:  scene.RGB(colBorderLight),
				CornerRadius: scene.Ptr[float32](6),
				FontSize:     scene.Ptr[float32](13),
				FontFamily:   scene.Ptr[string](uiFontFamily),
			}),
			sysBtn(systemAuto),
			sysBtn(systemAndroid11),
			pillButton("立即创建", a.click(a.createInstance), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			pillButton("取消", a.click(func() { a.showCreate = false; a.render() }), colBgSubtle, colBgHover, colTextMuted, 0),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			FlexWrap:      scene.Ptr(true),
			Gap:           scene.Pixels(8),
			Padding:       scene.Padding(12),
			Background:    scene.RGB(colBgCard),
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
			CornerRadius:  scene.Ptr[float32](10),
		}))
	}

	if a.batchPanel {
		children = append(children, scene.Div(eid(),
			labelText("批量控制：", 13, colTextPrimary, true),
			pillButton("批量开机", a.click(a.batchStart), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			pillButton("批量关机", a.click(a.batchStop), colDarkBtn, colDarkBtnHover, colWhite, 0),
			pillButton("全选", a.click(func() {
				for _, item := range a.available {
					a.batchSelected[item.Name] = true
				}
				a.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("清空选择", a.click(func() {
				a.batchSelected = make(map[string]bool)
				a.render()
			}), colBgSubtle, colBgHover, colTextMuted, 0),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(8),
			Padding:       scene.Padding(10),
			Background:    scene.RGB(colBgCard),
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
			CornerRadius:  scene.Ptr[float32](10),
		}))
	}

	items := visibleDevices(a.available, a.deviceSearch.Value, a.deviceSort)
	if len(items) == 0 {
		items = []avd.InstanceSummary{{Name: avd.DefaultName, State: "not_created", API: 35, Cores: 2, RAMMiB: 4096, Width: 1080, Height: 1920, Density: 480}}
	}

	cardNodes := make([]*scene.Node, 0, len(items))
	for i, item := range items {
		cardNodes = append(cardNodes, a.buildDeviceCard(item, i))
	}

	listContainer := scene.Div(eid(), cardNodes...).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		Width:         scene.Percent(100),
		Gap:           scene.Pixels(12),
	})
	if a.deviceGridView {
		listContainer.Style(&scene.Style{
			Display:     scene.DisplayGrid.Enum(),
			GridColumns: scene.Ptr[uint32](2),
			Width:       scene.Percent(100),
			Gap:         scene.Pixels(12),
		})
	}

	scrollArea := scene.Div(eid(), listContainer).Style(&scene.Style{
		Width:     scene.Percent(100),
		FlexGrow:  scene.Ptr[float32](1),
		OverflowY: scene.OverflowScroll.Enum(),
	})
	children = append(children, scrollArea)

	return scene.Div(eid(), children...).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		Width:         scene.Percent(100),
		FlexGrow:      scene.Ptr[float32](1),
		Padding:       &scene.Edges{Top: scene.Pixels(18), Bottom: scene.Pixels(14), Left: scene.Pixels(22), Right: scene.Pixels(22)},
		Gap:           scene.Pixels(14),
	})
}

func (a *app) buildDeviceCard(item avd.InstanceSummary, index int) *scene.Node {
	a.ensureThumb(item.Name)
	thumbRes := a.thumbs[item.Name]

	thumbOverlay := "未开机"
	if item.State == "running" {
		thumbOverlay = ""
	} else if item.State == "not_created" {
		thumbOverlay = "待初始化"
	}

	var thumbChildren []*scene.Node
	if thumbRes != nil {
		thumbChildren = append(thumbChildren, scene.Image(eid(), thumbRes.Handle()).Style(&scene.Style{
			Width:  scene.Percent(100),
			Height: scene.Percent(100),
		}))
		if thumbOverlay != "" {
			thumbChildren = append(thumbChildren, scene.Div(eid(),
				svgIcon(a.icons.Power, 11, colWhite),
				labelText(thumbOverlay, 11, colWhite, true),
			).Style(&scene.Style{
				Position:       scene.PositionAbsolute.Enum(),
				Inset:          &scene.Edges{Left: scene.Pixels(0), Right: scene.Pixels(0), Bottom: scene.Pixels(0)},
				Display:        scene.DisplayFlex.Enum(),
				FlexDirection:  scene.FlexRow.Enum(),
				AlignItems:     scene.AlignCenter.Enum(),
				JustifyContent: scene.JustifyCenter.Enum(),
				Gap:            scene.Pixels(4),
				Padding:        &scene.Edges{Top: scene.Pixels(4), Bottom: scene.Pixels(4)},
				Background:     &scene.Color{Red: 0, Green: 0, Blue: 0, Alpha: 0.65},
			}))
		}
	} else {
		thumbChildren = append(thumbChildren, scene.Div(eid(),
			svgIcon(a.icons.Device, 24, colTextSubtle),
			labelText(thumbOverlay, 12, colWhite, true),
		).Style(&scene.Style{
			Display:        scene.DisplayFlex.Enum(),
			FlexDirection:  scene.FlexColumn.Enum(),
			AlignItems:     scene.AlignCenter.Enum(),
			JustifyContent: scene.JustifyCenter.Enum(),
			Width:          scene.Percent(100),
			Height:         scene.Percent(100),
			Gap:            scene.Pixels(4),
		}))
	}
	thumbChildren = append(thumbChildren, scene.Div(eid(),
		labelText(fmt.Sprintf("# %d", index), 11, colWhite, true),
	).Style(&scene.Style{
		Position:     scene.PositionAbsolute.Enum(),
		Inset:        &scene.Edges{Top: scene.Pixels(6), Left: scene.Pixels(6)},
		Padding:      &scene.Edges{Top: scene.Pixels(2), Bottom: scene.Pixels(2), Left: scene.Pixels(6), Right: scene.Pixels(6)},
		Background:   &scene.Color{Red: 0, Green: 0, Blue: 0, Alpha: 0.6},
		CornerRadius: scene.Ptr[float32](4),
	}))

	thumbBox := scene.Div(eid(), thumbChildren...).Style(&scene.Style{
		Position:     scene.PositionRelative.Enum(),
		Width:        scene.Pixels(160),
		Height:       scene.Pixels(92),
		Background:   scene.RGB(0x28303D),
		CornerRadius: scene.Ptr[float32](8),
		OverflowX:    scene.OverflowHidden.Enum(),
		OverflowY:    scene.OverflowHidden.Enum(),
		FlexShrink:   scene.Ptr[float32](0),
	})

	titleRow := []*scene.Node{labelText(item.Name, 17, colTextPrimary, true)}
	if item.State == "running" {
		titleRow = append(titleRow, scene.Div(eid(),
			labelText("• 运行中", 12, colGreenText, true),
		).Style(&scene.Style{
			Padding:      &scene.Edges{Top: scene.Pixels(2), Bottom: scene.Pixels(2), Left: scene.Pixels(8), Right: scene.Pixels(8)},
			Background:   scene.RGB(colGreenBadgeBg),
			CornerRadius: scene.Ptr[float32](10),
		}))
	}

	cores := item.Cores
	if cores <= 0 {
		cores = 4
	}
	ramMB := item.RAMMiB
	if ramMB <= 0 {
		ramMB = 4096
	}
	ramLabel := fmt.Sprintf("%d GB", max(1, ramMB/1024))
	if ramMB%1024 != 0 {
		ramLabel = fmt.Sprintf("%.1f GB", float64(ramMB)/1024.0)
	}
	w, h, dpi := item.Width, item.Height, item.Density
	if w <= 0 || h <= 0 {
		w, h, dpi = 1080, 1920, 480
	}

	chips := scene.Div(eid(),
		scene.Div(eid(), labelText(androidLabel(item.API), 11, colWhite, true)).Style(&scene.Style{
			Padding:      &scene.Edges{Top: scene.Pixels(2), Bottom: scene.Pixels(2), Left: scene.Pixels(8), Right: scene.Pixels(8)},
			Background:   scene.RGB(colAndroidBadge),
			CornerRadius: scene.Ptr[float32](4),
		}),
		scene.Div(eid(), labelText(fmt.Sprintf("%d核", cores), 11, colTextPrimary, false)).Style(&scene.Style{
			Padding:      &scene.Edges{Top: scene.Pixels(2), Bottom: scene.Pixels(2), Left: scene.Pixels(7), Right: scene.Pixels(7)},
			Background:   scene.RGB(colBgChip),
			CornerRadius: scene.Ptr[float32](4),
		}),
		scene.Div(eid(), labelText(ramLabel, 11, colTextPrimary, false)).Style(&scene.Style{
			Padding:      &scene.Edges{Top: scene.Pixels(2), Bottom: scene.Pixels(2), Left: scene.Pixels(7), Right: scene.Pixels(7)},
			Background:   scene.RGB(colBgChip),
			CornerRadius: scene.Ptr[float32](4),
		}),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexRow.Enum(),
		AlignItems:    scene.AlignCenter.Enum(),
		Gap:           scene.Pixels(6),
	})

	infoCol := scene.Div(eid(),
		scene.Div(eid(), titleRow...).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(8),
		}),
		chips,
		labelText(fmt.Sprintf("%d × %d   |   %d DPI", w, h, dpi), 12, colTextMuted, false),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		FlexGrow:      scene.Ptr[float32](1),
		Gap:           scene.Pixels(7),
	})

	actionIcon := a.icons.Play
	actionTip := "启动设备"
	onPrimary := func() {
		a.selected = item.Name
		a.startInstance(item.Name)
	}
	if item.State == "running" {
		actionIcon = a.icons.Power
		actionTip = "关闭设备"
		onPrimary = func() {
			a.selected = item.Name
			a.stopInstance(item.Name)
		}
	}

	circleBtn := func(icon *client.Resource, bg, hover, fg uint32, tip string, fn func()) *scene.Node {
		return scene.Div(eid(), svgIcon(icon, 16, fg)).
			Focusable(0).
			OnClick(a.click(fn)).
			Accessible(tip, &scene.Accessibility{Role: scene.RoleButton}).
			Style(&scene.Style{
				Display:        scene.DisplayFlex.Enum(),
				AlignItems:     scene.AlignCenter.Enum(),
				JustifyContent: scene.JustifyCenter.Enum(),
				Width:          scene.Pixels(36),
				Height:         scene.Pixels(36),
				Background:     scene.RGB(bg),
				CornerRadius:   scene.Ptr[float32](18),
				Cursor:         scene.CursorPointer.Enum(),
			}).Hover(&scene.Style{Background: scene.RGB(hover)})
	}

	actionsRow := []*scene.Node{}
	if a.batchPanel {
		sel := a.batchSelected[item.Name]
		lbl := "选中"
		bg := uint32(colBgSubtle)
		fg := uint32(colTextMuted)
		if sel {
			lbl = "✓ 已选"
			bg = colPrimaryBlue
			fg = colWhite
		}
		actionsRow = append(actionsRow, pillButton(lbl, a.click(func() {
			a.batchSelected[item.Name] = !a.batchSelected[item.Name]
			a.render()
		}), bg, colBgHover, fg, 0))
	}
	actionsRow = append(actionsRow,
		circleBtn(actionIcon, colDarkBtn, colDarkBtnHover, colWhite, actionTip, onPrimary),
		circleBtn(a.icons.More, colBgSubtle, colBgHover, colTextPrimary, "更多操作", func() {
			if a.moreMenuDevice == item.Name {
				a.moreMenuDevice = ""
			} else {
				a.moreMenuDevice = item.Name
			}
			a.selected = item.Name
			a.render()
		}),
	)

	cardMain := scene.Div(eid(),
		thumbBox,
		infoCol,
		scene.Div(eid(), actionsRow...).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(10),
			FlexShrink:    scene.Ptr[float32](0),
		}),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexRow.Enum(),
		AlignItems:    scene.AlignCenter.Enum(),
		Width:         scene.Percent(100),
		Gap:           scene.Pixels(16),
	})

	cardChildren := []*scene.Node{cardMain}
	if a.moreMenuDevice == item.Name {
		menuNodes := []*scene.Node{
			pillButton("打开画面", a.click(func() { a.moreMenuDevice = ""; a.startInstance(item.Name) }), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			pillButton("设备设置", a.click(func() { a.moreMenuDevice = ""; a.openDeviceSettings(item.Name) }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("重启设备", a.click(func() { a.moreMenuDevice = ""; a.restartInstance(item.Name) }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("克隆设备", a.click(func() { a.cloneDevice = item.Name; a.cloneName.Reset(item.Name + "-copy"); a.render() }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("重命名", a.click(func() { a.renameDevice = item.Name; a.renameName.Reset(item.Name); a.render() }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("清理磁盘", a.click(func() { a.moreMenuDevice = ""; a.trimInstance(item.Name) }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("共享文件夹", a.click(func() { _ = openSharedFolder(item.Name) }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("桌面快捷方式", a.click(func() {
				if p, err := avd.CreateDesktopShortcut(item.Name, ""); err != nil {
					a.message = err.Error()
				} else {
					a.message = "已创建桌面快捷方式：" + p
				}
				a.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("导出备份", a.click(func() { a.moreMenuDevice = ""; a.exportBackup(item.Name) }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("恢复备份", a.click(func() { a.moreMenuDevice = ""; a.restoreBackupPrompt(item.Name) }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("重置数据", a.click(func() { a.moreMenuDevice = ""; a.resetInstanceData(item.Name) }), colRedBg, colRedBg, colRedText, 0),
			pillButton("删除", a.click(func() { a.deleteInstance(item.Name) }), colRedBg, colRedBg, colRedText, 0),
		}
		cardChildren = append(cardChildren, scene.Div(eid(), menuNodes...).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			FlexWrap:      scene.Ptr(true),
			Gap:           scene.Pixels(8),
			Padding:       &scene.Edges{Top: scene.Pixels(10), Bottom: scene.Pixels(2)},
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
			CornerRadius:  scene.Ptr[float32](8),
		}))
	}

	if a.cloneDevice == item.Name {
		cardChildren = append(cardChildren, scene.Div(eid(),
			labelText("克隆名称:", 12, colTextPrimary, true),
			scene.TextInput(idCloneNameInput, a.cloneName, scene.TextInputOptions{OnChange: a.hClone}).Style(&scene.Style{
				Width:        scene.Pixels(180),
				Padding:      scene.Padding(6),
				Background:   scene.RGB(colBgPage),
				BorderWidth:  scene.Ptr[float32](1),
				BorderColor:  scene.RGB(colBorderLight),
				CornerRadius: scene.Ptr[float32](6),
				FontSize:     scene.Ptr[float32](12),
			}),
			pillButton("确认克隆", a.click(func() { a.cloneInstance(item.Name) }), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			pillButton("取消", a.click(func() { a.cloneDevice = ""; a.render() }), colBgSubtle, colBgHover, colTextMuted, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(8)}))
	}

	if a.renameDevice == item.Name {
		cardChildren = append(cardChildren, scene.Div(eid(),
			labelText("新设备名称:", 12, colTextPrimary, true),
			scene.TextInput(idRenameInput, a.renameName, scene.TextInputOptions{OnChange: a.hRename}).Style(&scene.Style{
				Width:        scene.Pixels(180),
				Padding:      scene.Padding(6),
				Background:   scene.RGB(colBgPage),
				BorderWidth:  scene.Ptr[float32](1),
				BorderColor:  scene.RGB(colBorderLight),
				CornerRadius: scene.Ptr[float32](6),
				FontSize:     scene.Ptr[float32](12),
			}),
			pillButton("确认重命名", a.click(func() { a.renameInstance(item.Name) }), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			pillButton("取消", a.click(func() { a.renameDevice = ""; a.render() }), colBgSubtle, colBgHover, colTextMuted, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(8)}))
	}

	return scene.Div(eid(), cardChildren...).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		Width:         scene.Percent(100),
		Padding:       &scene.Edges{Top: scene.Pixels(14), Bottom: scene.Pixels(14), Left: scene.Pixels(16), Right: scene.Pixels(18)},
		Gap:           scene.Pixels(10),
		Background:    scene.RGB(colBgCard),
		BorderWidth:   scene.Ptr[float32](1),
		BorderColor:   scene.RGB(colBorderLight),
		CornerRadius:  scene.Ptr[float32](12),
	})
}

func (a *app) buildRemoteView() *scene.Node {
	card := func(title, desc, status string) *scene.Node {
		return scene.Div(eid(),
			labelText(title, 15, colTextPrimary, true),
			labelText(desc, 13, colTextMuted, false),
			labelText(status, 12, colPrimaryBlue, true),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexColumn.Enum(),
			Gap:           scene.Pixels(4),
			Padding:       scene.Padding(16),
			Background:    scene.RGB(colBgCard),
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
			CornerRadius:  scene.Ptr[float32](10),
		})
	}
	syncStatus := "当前未开启多开同步"
	if a.syncHub.Enabled() {
		syncStatus = "正在同步主控设备：" + a.syncHub.Master()
	}
	return scene.Div(eid(),
		labelText("远控与多开同步中心", 20, colTextPrimary, true),
		card("多开窗口同步器", "在任意运行中的模拟器窗口点击顶部「同步」图标，即可将鼠标点击、拖动与按键实时广播到其他选中的设备窗口。", syncStatus),
		card("agent-desktop 宿主桌面协同", "neko-mcp.exe 支持通过 NEKO_DESKTOP_PROXY=1 代理暴露全部 desktop_* 宿主桌面控制工具。", fmt.Sprintf("可用状态：%v  %s", a.ecosystem.AgentDesktop.Available, a.ecosystem.AgentDesktop.Path)),
		card("纯本地无云控推送", "本版本彻底去除云手机商业广告与推广弹窗，所有设备与数据仅保存在本机。", os.Getenv("LOCALAPPDATA")+"\\NekoSimulator233-data"),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		FlexGrow:      scene.Ptr[float32](1),
		Padding:       scene.Padding(24),
		Gap:           scene.Pixels(14),
	})
}

func (a *app) buildToolsView() *scene.Node {
	ecoRow := func(t mcpproxy.ToolStatus) *scene.Node {
		badgeBg, badgeFg, badgeText := uint32(colRedBg), uint32(colRedText), "未检测到"
		if t.Available {
			badgeBg, badgeFg, badgeText = colGreenBadgeBg, colGreenText, "已就绪"
		}
		return scene.Div(eid(),
			scene.Div(eid(),
				labelText(t.Name, 14, colTextPrimary, true),
				labelText(t.Notes, 12, colTextMuted, false),
			).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), FlexGrow: scene.Ptr[float32](1), Gap: scene.Pixels(2)}),
			scene.Div(eid(), labelText(badgeText, 12, badgeFg, true)).Style(&scene.Style{
				Padding:      &scene.Edges{Top: scene.Pixels(3), Bottom: scene.Pixels(3), Left: scene.Pixels(10), Right: scene.Pixels(10)},
				Background:   scene.RGB(badgeBg),
				CornerRadius: scene.Ptr[float32](10),
			}),
		).Style(&scene.Style{
			Display:        scene.DisplayFlex.Enum(),
			FlexDirection:  scene.FlexRow.Enum(),
			AlignItems:     scene.AlignCenter.Enum(),
			JustifyContent: scene.JustifySpaceBetween.Enum(),
			Padding:        scene.Padding(12),
			Background:     scene.RGB(colBgCard),
			BorderWidth:    scene.Ptr[float32](1),
			BorderColor:    scene.RGB(colBorderLight),
			CornerRadius:   scene.Ptr[float32](8),
		})
	}

	return scene.Div(eid(),
		labelText("MCP 工具生态与运行时工具箱", 20, colTextPrimary, true),
		scene.Div(eid(),
			labelText("独立运行时下载（保持 MSI 安装包轻量）：", 13, colTextPrimary, true),
			scene.Div(eid(),
				scene.TextInput(idManifestInput, a.runtimeManifest, scene.TextInputOptions{
					Placeholder: "输入运行时 manifest.json 的 HTTPS URL 或本地路径…",
					OnChange:    a.hManifest,
				}).Style(&scene.Style{
					FlexGrow:     scene.Ptr[float32](1),
					Padding:      scene.Padding(8),
					Background:   scene.RGB(colBgPage),
					BorderWidth:  scene.Ptr[float32](1),
					BorderColor:  scene.RGB(colBorderLight),
					CornerRadius: scene.Ptr[float32](6),
					FontSize:     scene.Ptr[float32](13),
				}),
				pillButton("下载并安装运行时", a.click(a.downloadRuntime), colPrimaryBlue, colPrimaryHover, colWhite, 0),
				pillButton("一键部署官方 Android SDK", a.click(a.bootstrapAndLaunch), colDarkBtn, colDarkBtnHover, colWhite, 0),
			).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), Gap: scene.Pixels(8), Width: scene.Percent(100)}),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexColumn.Enum(),
			Gap:           scene.Pixels(8),
			Padding:       scene.Padding(14),
			Background:    scene.RGB(colBgCard),
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
			CornerRadius:  scene.Ptr[float32](10),
		}),
		ecoRow(a.ecosystem.AndroidSDK),
		ecoRow(a.ecosystem.AgentDesktop),
		ecoRow(a.ecosystem.REAMCP),
		ecoRow(a.ecosystem.Ghidra),
		ecoRow(a.ecosystem.CraftAnything),
		ecoRow(a.ecosystem.WiX),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		FlexGrow:      scene.Ptr[float32](1),
		Padding:       scene.Padding(24),
		Gap:           scene.Pixels(10),
		OverflowY:     scene.OverflowScroll.Enum(),
	})
}

func (a *app) buildGlobalSettingsView() *scene.Node {
	return scene.Div(eid(),
		labelText("全局设置", 20, colTextPrimary, true),
		scene.Div(eid(),
			labelText("NekoSimulator233 · 纯净无广告 Go + GPUI 版本 v"+version, 15, colTextPrimary, true),
			labelText("• 渲染引擎：Rust GPUI GPU 加速渲染（github.com/moriyoshi/gpui-go）", 13, colTextMuted, false),
			labelText("• 底层虚拟化：Android SDK Emulator + 硬件 Vulkan 直通", 13, colTextMuted, false),
			labelText("• 广告拦截策略：100% 去除桌面横幅、搜索推广词、每日新发现与商业弹窗", 13, colTextMuted, false),
			labelText("• 实例数据目录："+filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data"), 13, colTextMuted, false),
			pillButton("立即刷新全部状态", a.click(a.refresh), colPrimaryBlue, colPrimaryHover, colWhite, 0),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexColumn.Enum(),
			Gap:           scene.Pixels(10),
			Padding:       scene.Padding(20),
			Background:    scene.RGB(colBgCard),
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
			CornerRadius:  scene.Ptr[float32](12),
		}),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		FlexGrow:      scene.Ptr[float32](1),
		Padding:       scene.Padding(24),
		Gap:           scene.Pixels(14),
	})
}
