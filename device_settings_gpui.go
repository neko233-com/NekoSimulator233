package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moriyoshi/gpui-go/client"
	"github.com/moriyoshi/gpui-go/scene"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

// deviceSettingsUI implements the MuMu 6.8 multi-tab per-instance settings
// window (性能 / 显示 / 机型 / 网络 / 系统) using Go + GPUI.
type deviceSettingsUI struct {
	app       *app
	name      string
	win       *client.Window
	tab       string
	message   string
	handlers  map[uint64]func(*client.Event)
	resources avd.Resources
	display   avd.DisplaySettings
	phone     avd.PhoneIdentity
	settings  avd.InstanceSettings
	disk      avd.DiskUsageInfo

	coresIn   scene.InputState
	ramIn     scene.InputState
	widthIn   scene.InputState
	heightIn  scene.InputState
	dpiIn     scene.InputState
	brandIn   scene.InputState
	modelIn   scene.InputState
	imeiIn    scene.InputState
	macIn     scene.InputState
	androidIn scene.InputState

	hCores, hRAM, hW, hH, hDPI, hBrand, hModel, hIMEI, hMAC, hAndID uint64
}

func (a *app) openDeviceSettings(name string) {
	key := independentViewerKey(name)
	if existing := a.settingsWins[key]; existing != nil {
		focusWindowByTitle("设备设置 - " + name)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m, _ := avd.DiscoverManagerOrOffline()
		m, err := m.WithName(name)
		if err != nil {
			a.post(func() { a.message = err.Error(); a.render() })
			return
		}
		res, _ := m.GetResources(ctx)
		if res.Cores <= 0 {
			res = avd.Resources{Cores: 4, RAMMiB: 4096}
		}
		disp, _ := m.GetDisplaySettings(ctx)
		if disp.Width <= 0 {
			disp = avd.DisplaySettings{Width: 1080, Height: 1920, Density: 480}
		}
		phone, _ := m.PhoneIdentity()
		cfg, _ := m.InstanceSettings()
		disk, _ := m.DiskUsage()

		a.post(func() {
			s := &deviceSettingsUI{
				app:       a,
				name:      name,
				tab:       "性能",
				handlers:  make(map[uint64]func(*client.Event)),
				resources: res,
				display:   disp,
				phone:     phone,
				settings:  cfg,
				disk:      disk,
			}
			s.syncInputs()
			s.registerTextHandlers()
			win, openErr := a.cli.OpenWindow(a.ctx, client.WindowOptions{
				Title:  "设备设置 - " + name,
				Width:  660,
				Height: 500,
			}, s.buildView())
			if openErr != nil {
				a.message = "打开设置窗口失败：" + openErr.Error()
				a.render()
				return
			}
			s.win = win
			a.settingsWins[key] = s
		})
	}()
}

func (s *deviceSettingsUI) syncInputs() {
	s.coresIn.Reset(strconv.Itoa(s.resources.Cores))
	s.ramIn.Reset(strconv.Itoa(s.resources.RAMMiB))
	s.widthIn.Reset(strconv.Itoa(s.display.Width))
	s.heightIn.Reset(strconv.Itoa(s.display.Height))
	s.dpiIn.Reset(strconv.Itoa(s.display.Density))
	s.brandIn.Reset(s.phone.Brand)
	s.modelIn.Reset(s.phone.Model)
	s.imeiIn.Reset(s.phone.IMEI)
	s.macIn.Reset(s.settings.MACAddress)
	s.androidIn.Reset(s.settings.AndroidID)
}

func (s *deviceSettingsUI) registerTextHandlers() {
	reg := func(st *scene.InputState) uint64 {
		return s.app.cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
			if ch := ev.GetTextChanged(); ch != nil {
				st.Accept(ch)
			}
			return nil
		})
	}
	s.hCores = reg(&s.coresIn)
	s.hRAM = reg(&s.ramIn)
	s.hW = reg(&s.widthIn)
	s.hH = reg(&s.heightIn)
	s.hDPI = reg(&s.dpiIn)
	s.hBrand = reg(&s.brandIn)
	s.hModel = reg(&s.modelIn)
	s.hIMEI = reg(&s.imeiIn)
	s.hMAC = reg(&s.macIn)
	s.hAndID = reg(&s.androidIn)
}

func (s *deviceSettingsUI) handleEvent(ev *client.Event) bool {
	if s.win == nil || ev.GetWindow().GetId() != s.win.Handle().GetId() {
		return false
	}
	if ev.GetWindowClosed() != nil {
		delete(s.app.settingsWins, independentViewerKey(s.name))
		return true
	}
	if fn, ok := s.handlers[ev.GetHandlerId()]; ok && fn != nil {
		fn(ev)
		return true
	}
	return true
}

func (s *deviceSettingsUI) click(fn func()) uint64 {
	id := s.app.cli.OnClick(func(_ context.Context, _ *client.Event) error { return nil })
	s.handlers[id] = func(_ *client.Event) {
		if fn != nil {
			fn()
		}
	}
	return id
}

func (s *deviceSettingsUI) render() {
	if s.win == nil {
		return
	}
	s.handlers = make(map[uint64]func(*client.Event))
	_ = s.win.Set(s.app.ctx, s.buildView())
}

func (s *deviceSettingsUI) saveAll() {
	if c, err := strconv.Atoi(strings.TrimSpace(s.coresIn.Value)); err == nil {
		s.resources.Cores = c
	}
	if r, err := strconv.Atoi(strings.TrimSpace(s.ramIn.Value)); err == nil {
		s.resources.RAMMiB = r
	}
	if w, err := strconv.Atoi(strings.TrimSpace(s.widthIn.Value)); err == nil {
		s.display.Width = w
	}
	if h, err := strconv.Atoi(strings.TrimSpace(s.heightIn.Value)); err == nil {
		s.display.Height = h
	}
	if d, err := strconv.Atoi(strings.TrimSpace(s.dpiIn.Value)); err == nil {
		s.display.Density = d
	}
	s.phone.Brand = strings.TrimSpace(s.brandIn.Value)
	s.phone.Manufacturer = s.phone.Brand
	s.phone.Model = strings.TrimSpace(s.modelIn.Value)
	s.phone.IMEI = strings.TrimSpace(s.imeiIn.Value)
	s.settings.MACAddress = strings.TrimSpace(s.macIn.Value)
	s.settings.AndroidID = strings.TrimSpace(s.androidIn.Value)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m, _ := avd.DiscoverManagerOrOffline()
		m, err := m.WithName(s.name)
		if err == nil {
			err = m.SetInstanceSettings(s.settings)
		}
		if err == nil {
			_ = m.SetPhoneIdentity(s.phone)
		}
		if err == nil {
			if rErr := m.SetResources(ctx, s.resources); rErr != nil {
				err = rErr
			}
		}
		if err == nil {
			if dErr := m.SetDisplaySettings(ctx, s.display); dErr != nil {
				err = dErr
			}
		}
		s.app.post(func() {
			if err != nil {
				s.message = "保存部分或全部失败：" + err.Error()
			} else {
				s.message = "设置已保存（资源与分辨率在下次重启后生效）"
				s.app.refresh()
			}
			s.render()
		})
	}()
}

func (s *deviceSettingsUI) buildView() *scene.Node {
	tabs := []string{"性能", "显示", "机型", "系统"}
	tabNodes := make([]*scene.Node, 0, len(tabs))
	for _, t := range tabs {
		tabName := t
		active := s.tab == tabName
		bg, fg := uint32(colBgSidebar), uint32(colTextMuted)
		if active {
			bg, fg = colBgHover, colPrimaryBlue
		}
		tabNodes = append(tabNodes, pillButton(tabName, s.click(func() {
			s.tab = tabName
			s.render()
		}), bg, colBgHover, fg, 0))
	}

	var content *scene.Node
	switch s.tab {
	case "显示":
		content = s.buildDisplayTab()
	case "机型":
		content = s.buildPhoneTab()
	case "系统":
		content = s.buildSystemTab()
	default:
		content = s.buildPerfTab()
	}

	return scene.Div(1,
		scene.Div(eid(), tabNodes...).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexColumn.Enum(),
			Width:         scene.Pixels(112),
			Height:        scene.Percent(100),
			Padding:       scene.Padding(12),
			Gap:           scene.Pixels(8),
			Background:    scene.RGB(colBgSidebar),
			BorderWidth:   scene.Ptr[float32](1),
			BorderColor:   scene.RGB(colBorderLight),
		}),
		scene.Div(eid(),
			scene.Div(eid(), content).Style(&scene.Style{FlexGrow: scene.Ptr[float32](1), OverflowY: scene.OverflowScroll.Enum()}),
			scene.Div(eid(),
				labelText(s.message, 12, colTextMuted, false),
				pillButton("保存全部设置", s.click(s.saveAll), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			).Style(&scene.Style{
				Display:        scene.DisplayFlex.Enum(),
				FlexDirection:  scene.FlexRow.Enum(),
				AlignItems:     scene.AlignCenter.Enum(),
				JustifyContent: scene.JustifySpaceBetween.Enum(),
				Padding:        &scene.Edges{Top: scene.Pixels(10)},
				BorderWidth:    scene.Ptr[float32](1),
				BorderColor:    scene.RGB(colBorderLight),
			}),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexColumn.Enum(),
			FlexGrow:      scene.Ptr[float32](1),
			Height:        scene.Percent(100),
			Padding:       scene.Padding(18),
			Gap:           scene.Pixels(12),
			Background:    scene.RGB(colBgCard),
		}),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexRow.Enum(),
		Width:         scene.Percent(100),
		Height:        scene.Percent(100),
		FontFamily:    scene.Ptr[string](uiFontFamily),
	})
}

func (s *deviceSettingsUI) buildPerfTab() *scene.Node {
	tierBtn := func(mode, title string) *scene.Node {
		active := s.settings.PerformanceMode == mode
		bg, fg := uint32(colBgSubtle), uint32(colTextPrimary)
		if active {
			bg, fg = colPrimaryBlue, colWhite
		}
		return pillButton(title, s.click(func() {
			s.settings.PerformanceMode = mode
			if r, ok := avd.PerformancePresetResources(mode); ok {
				s.resources = r
				s.coresIn.Reset(strconv.Itoa(r.Cores))
				s.ramIn.Reset(strconv.Itoa(r.RAMMiB))
			}
			s.render()
		}), bg, colBgHover, fg, 0)
	}
	return scene.Div(eid(),
		labelText("性能设置", 16, colTextPrimary, true),
		scene.Div(eid(), tierBtn("low", "低 (1核 1G)"), tierBtn("middle", "中 (4核 6G)"), tierBtn("high", "高 (6核 12G)"), tierBtn("custom", "自定义")).
			Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), Gap: scene.Pixels(8), FlexWrap: scene.Ptr(true)}),
		scene.Div(eid(),
			labelText("CPU 核数:", 13, colTextPrimary, false),
			scene.TextInput(201, s.coresIn, scene.TextInputOptions{OnChange: s.hCores}).Style(&scene.Style{Width: scene.Pixels(80), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
			labelText("内存 (MiB):", 13, colTextPrimary, false),
			scene.TextInput(202, s.ramIn, scene.TextInputOptions{OnChange: s.hRAM}).Style(&scene.Style{Width: scene.Pixels(100), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(10)}),
		scene.Div(eid(),
			pillButton(fmt.Sprintf("内存优化：%v", s.settings.ForceDedicatedGPU), s.click(func() { s.settings.ForceDedicatedGPU = !s.settings.ForceDedicatedGPU; s.render() }), colBgSubtle, colBgHover, colTextPrimary, 0),
			labelText("渲染模式：Vulkan 硬件加速", 13, colTextMuted, false),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(12)}),
	).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(14)})
}

func (s *deviceSettingsUI) buildDisplayTab() *scene.Node {
	resBtn := func(mode, title string) *scene.Node {
		active := s.settings.ResolutionMode == mode
		bg, fg := uint32(colBgSubtle), uint32(colTextPrimary)
		if active {
			bg, fg = colPrimaryBlue, colWhite
		}
		return pillButton(title, s.click(func() {
			s.settings.ResolutionMode = mode
			if d, ok := avd.DisplayPresetSettings(mode); ok {
				s.display = d
				s.widthIn.Reset(strconv.Itoa(d.Width))
				s.heightIn.Reset(strconv.Itoa(d.Height))
				s.dpiIn.Reset(strconv.Itoa(d.Density))
			}
			s.render()
		}), bg, colBgHover, fg, 0)
	}
	fpsBtn := func(fps int) *scene.Node {
		active := s.settings.FPSLimit == fps
		bg, fg := uint32(colBgSubtle), uint32(colTextPrimary)
		if active {
			bg, fg = colPrimaryBlue, colWhite
		}
		return pillButton(fmt.Sprintf("%d FPS", fps), s.click(func() { s.settings.FPSLimit = fps; s.render() }), bg, colBgHover, fg, 0)
	}
	return scene.Div(eid(),
		labelText("显示设置", 16, colTextPrimary, true),
		scene.Div(eid(), resBtn("phone", "手机竖屏 1080×1920"), resBtn("tablet", "平板横屏 1920×1080"), resBtn("widescreen", "宽屏 2560×1440"), resBtn("custom", "自定义")).
			Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), Gap: scene.Pixels(8), FlexWrap: scene.Ptr(true)}),
		scene.Div(eid(),
			labelText("宽:", 13, colTextPrimary, false),
			scene.TextInput(203, s.widthIn, scene.TextInputOptions{OnChange: s.hW}).Style(&scene.Style{Width: scene.Pixels(75), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
			labelText("高:", 13, colTextPrimary, false),
			scene.TextInput(204, s.heightIn, scene.TextInputOptions{OnChange: s.hH}).Style(&scene.Style{Width: scene.Pixels(75), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
			labelText("DPI:", 13, colTextPrimary, false),
			scene.TextInput(205, s.dpiIn, scene.TextInputOptions{OnChange: s.hDPI}).Style(&scene.Style{Width: scene.Pixels(65), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(8)}),
		labelText("帧率上限", 13, colTextPrimary, true),
		scene.Div(eid(), fpsBtn(15), fpsBtn(30), fpsBtn(60), fpsBtn(120)).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), Gap: scene.Pixels(8)}),
		scene.Div(eid(),
			pillButton(fmt.Sprintf("显示帧率：%v", s.settings.ShowFPS), s.click(func() { s.settings.ShowFPS = !s.settings.ShowFPS; s.render() }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton(fmt.Sprintf("垂直同步：%v", s.settings.VSync), s.click(func() { s.settings.VSync = !s.settings.VSync; s.render() }), colBgSubtle, colBgHover, colTextPrimary, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), Gap: scene.Pixels(8)}),
	).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(12)})
}

func (s *deviceSettingsUI) buildPhoneTab() *scene.Node {
	presetBtns := []*scene.Node{}
	for _, p := range avd.PhonePresets() {
		preset := p
		presetBtns = append(presetBtns, pillButton(preset.Label, s.click(func() {
			s.phone = avd.RandomPhoneIdentity(&preset)
			s.brandIn.Reset(s.phone.Brand)
			s.modelIn.Reset(s.phone.Model)
			s.imeiIn.Reset(s.phone.IMEI)
			s.render()
		}), colBgSubtle, colBgHover, colTextPrimary, 0))
	}
	return scene.Div(eid(),
		labelText("机型与设备属性伪装", 16, colTextPrimary, true),
		scene.Div(eid(), presetBtns...).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), Gap: scene.Pixels(6), FlexWrap: scene.Ptr(true)}),
		scene.Div(eid(),
			labelText("品牌:", 13, colTextPrimary, false),
			scene.TextInput(206, s.brandIn, scene.TextInputOptions{OnChange: s.hBrand}).Style(&scene.Style{Width: scene.Pixels(120), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
			labelText("型号:", 13, colTextPrimary, false),
			scene.TextInput(207, s.modelIn, scene.TextInputOptions{OnChange: s.hModel}).Style(&scene.Style{Width: scene.Pixels(150), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(8)}),
		scene.Div(eid(),
			labelText("IMEI:", 13, colTextPrimary, false),
			scene.TextInput(208, s.imeiIn, scene.TextInputOptions{OnChange: s.hIMEI}).Style(&scene.Style{Width: scene.Pixels(180), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
			pillButton("一键随机全套标识", s.click(func() {
				s.phone = avd.RandomPhoneIdentity(nil)
				s.settings.MACAddress = avd.RandomMACAddress()
				s.settings.AndroidID = avd.RandomAndroidID()
				s.syncInputs()
				s.render()
			}), colPrimaryBlue, colPrimaryHover, colWhite, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(8)}),
		scene.Div(eid(),
			labelText("MAC:", 13, colTextPrimary, false),
			scene.TextInput(209, s.macIn, scene.TextInputOptions{OnChange: s.hMAC}).Style(&scene.Style{Width: scene.Pixels(160), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
			labelText("Android ID:", 13, colTextPrimary, false),
			scene.TextInput(210, s.androidIn, scene.TextInputOptions{OnChange: s.hAndID}).Style(&scene.Style{Width: scene.Pixels(160), Padding: scene.Padding(6), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](13)}),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(8)}),
	).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(12)})
}

func (s *deviceSettingsUI) buildSystemTab() *scene.Node {
	toggle := func(label string, val *bool) *scene.Node {
		st := "关闭"
		bg, fg := uint32(colBgSubtle), uint32(colTextPrimary)
		if *val {
			st = "开启"
			bg, fg = colGreenBadgeBg, colGreenText
		}
		return pillButton(label+"："+st, s.click(func() { *val = !*val; s.render() }), bg, colBgHover, fg, 0)
	}
	return scene.Div(eid(),
		labelText("系统与高级策略", 16, colTextPrimary, true),
		scene.Div(eid(),
			toggle("Root 权限", &s.settings.RootMode),
			toggle("右键返回", &s.settings.RightClickBack),
			toggle("最小化静音", &s.settings.WindowMinimizedMute),
			toggle("自动旋转", &s.settings.AutoRotate),
			toggle("后台保活", &s.settings.KeepAlive),
			toggle("退出确认", &s.settings.QuitConfirm),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), FlexWrap: scene.Ptr(true), Gap: scene.Pixels(8)}),
		labelText(fmt.Sprintf("当前磁盘占用：%s（%d 个文件）", s.disk.Formatted, s.disk.Files), 13, colTextMuted, false),
	).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(14)})
}
