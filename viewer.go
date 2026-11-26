package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moriyoshi/gpui-go/client"
	"github.com/moriyoshi/gpui-go/scene"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/macro"
)

const (
	topBarHeight      float32 = 42
	bottomBarHeight   float32 = 34
	drawerBarHeight   float32 = 176
	idViewerViewport  uint64  = 500
	idMacroNameInput  uint64  = 501
	idProfileInput    uint64  = 502
)

type pointerEvent struct {
	kind       int
	x, y       int
	radius     int
	maxX, maxY int
	usage      uint16
	action     display.Action
}

const (
	pointerDown = iota
	pointerMove
	pointerUp
	keyDown
	keyUp
	releaseKeys
	navigate
	mappedDown
	mappedUp
	stickDown
	stickUp
)

// emulatorViewer implements Figure 2: Chrome-style multi-app top tab bar +
// compact toolbar + pure black GPU-rendered Android viewport.
type emulatorViewer struct {
	app          *app
	name         string
	title        string
	serial       string
	win          *client.Window
	source       display.Session
	frameRes     *client.Resource
	width        int
	height       int
	viewW        float32
	viewH        float32
	fitMode      display.FitMode
	captureRate  atomic.Int32
	rateChanged  chan struct{}
	events       chan pointerEvent
	done         chan struct{}
	once         sync.Once
	held         bool
	keys         map[uint16]bool
	muted        bool
	topmost      bool
	landscape    bool
	drawerOpen   bool
	drawerTab    string // "tools" | "keymap" | "macro" | "sync" | "sensor"
	message      string
	handlers     map[uint64]func(*client.Event)
	runningApps   []avd.RunningApp
	activeAppPkg  string
	installedApps []string
	settings      avd.InstanceSettings
	liveFPS       float64
	fpsFrames     int
	fpsStart      time.Time

	// Keymap
	keymap       *keymapStore
	baseKeymap   string
	profile      string
	profiles     []string
	profileIn    scene.InputState
	hProfileIn   uint64
	bindings     map[uint16]mappedPoint
	joystick     *joystickConfig
	editing      int // 0=none, 1=bind, 2=remove, 3=joystick
	pendingPoint *mappedPoint

	// Macro recording & replay
	recorder     macro.Recorder
	macroNameIn  scene.InputState
	hMacroIn     uint64
	macros       []macro.Script
	macroLoops   int
	macroSpeed   float64
	replayCancel context.CancelFunc
}

func openEmulatorViewer(a *app, name string, source display.Session, bindingsPath string, inst avd.Instance) *emulatorViewer {
	v := &emulatorViewer{
		app:         a,
		name:        name,
		title:       name + " - NekoSimulator233",
		serial:      inst.Serial,
		source:      source,
		fitMode:     display.FitContain,
		rateChanged: make(chan struct{}, 1),
		events:      make(chan pointerEvent, 128),
		done:        make(chan struct{}),
		keys:        make(map[uint16]bool),
		handlers:    make(map[uint64]func(*client.Event)),
		drawerTab:   "tools",
		message:     "正在连接画面…",
		macroLoops:  1,
		macroSpeed:  1.0,
		fpsStart:    time.Now(),
	}
	v.captureRate.Store(30)

	if m, err := selectedManager(name); err == nil {
		if cfg, err := m.InstanceSettings(); err == nil {
			v.settings = cfg
			if cfg.FPSLimit > 0 && cfg.FPSLimit <= 120 {
				v.captureRate.Store(int32(cfg.FPSLimit))
			}
		}
	} else {
		v.settings = avd.DefaultInstanceSettings()
	}

	if _, ok := source.(display.MultiTouch); ok && bindingsPath != "" {
		v.baseKeymap = bindingsPath
		v.profile = defaultKeymapProfile
		v.keymap = &keymapStore{path: bindingsPath}
		if b, err := v.keymap.read(); err == nil {
			v.bindings = b
			v.joystick = v.keymap.joystick
			v.profiles, _ = listKeymapProfiles(bindingsPath)
		}
	}
	v.macros, _ = a.macroStore.List()

	v.hProfileIn = a.cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
		if ch := ev.GetTextChanged(); ch != nil {
			v.profileIn.Accept(ch)
		}
		return nil
	})
	v.hMacroIn = a.cli.OnTextChange(func(_ context.Context, ev *client.Event) error {
		if ch := ev.GetTextChanged(); ch != nil {
			v.macroNameIn.Accept(ch)
		}
		return nil
	})

	geom, hasGeom := loadViewerGeometry(name)
	w, h := float32(520), float32(880)
	if hasGeom {
		w, h = float32(geom.Width), float32(geom.Height)
	}
	v.viewW, v.viewH = w, h

	win, err := a.cli.OpenWindow(a.ctx, client.WindowOptions{
		Title:  v.title,
		Width:  w,
		Height: h,
	}, v.buildView())
	if err != nil {
		_ = source.Close()
		a.message = "打开模拟器窗口失败：" + err.Error()
		return nil
	}
	v.win = win
	makeFramelessTitlebarByTitle(v.title)
	if hasGeom && geom.Left > -4000 && geom.Top > -4000 {
		setWindowBoundsByTitle(v.title, geom.Left, geom.Top, geom.Width, geom.Height)
	}
	win.OnResized(func(_ context.Context, sz client.WindowSize) error {
		v.viewW, v.viewH = sz.Width, sz.Height
		v.render()
		return nil
	})

	a.syncHub.Register(name, v)
	go v.runCaptureAndInput()
	go v.pollRunningApps()
	return v
}

func (v *emulatorViewer) closeWindow() {
	if v.win != nil {
		if bounds, ok := getWindowBoundsByTitle(v.title); ok {
			_ = saveViewerGeometry(v.name, bounds)
		}
		_ = v.win.Close(v.app.ctx)
	}
	v.stop()
}

func (v *emulatorViewer) stop() {
	v.once.Do(func() {
		close(v.done)
		if v.replayCancel != nil {
			v.replayCancel()
		}
		v.app.syncHub.Unregister(v.name)
		delete(v.app.viewers, independentViewerKey(v.name))
		if v.frameRes != nil {
			_ = v.frameRes.Release(v.app.ctx)
			v.frameRes = nil
		}
	})
}

func (v *emulatorViewer) render() {
	if v.win == nil {
		return
	}
	v.handlers = make(map[uint64]func(*client.Event))
	_ = v.win.Set(v.app.ctx, v.buildView())
}

func (v *emulatorViewer) click(fn func()) uint64 {
	id := v.app.cli.OnClick(func(_ context.Context, _ *client.Event) error { return nil })
	v.handlers[id] = func(_ *client.Event) {
		if fn != nil {
			fn()
		}
	}
	return id
}

func (v *emulatorViewer) interact(fn func(*client.Event)) uint64 {
	id := v.app.cli.OnEvent(func(_ context.Context, _ *client.Event) error { return nil })
	v.handlers[id] = fn
	return id
}

func (v *emulatorViewer) handleEvent(ev *client.Event) bool {
	if v.win == nil || ev.GetWindow().GetId() != v.win.Handle().GetId() {
		return false
	}
	if ev.GetWindowClosed() != nil {
		if bounds, ok := getWindowBoundsByTitle(v.title); ok {
			_ = saveViewerGeometry(v.name, bounds)
		}
		v.stop()
		v.app.render()
		return true
	}
	if fc := ev.GetFocusChanged(); fc != nil && !fc.Focused {
		v.releaseHostInput()
	}
	if fn, ok := v.handlers[ev.GetHandlerId()]; ok && fn != nil {
		fn(ev)
		return true
	}
	return true
}

func (v *emulatorViewer) runCaptureAndInput() {
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		runViewerInput(v.done, v.events, newViewerInput(v.source), func(err error) {
			v.app.post(func() { v.message = err.Error(); v.render() })
		})
	}()
	defer func() {
		<-inputDone
		_ = v.source.Close()
	}()

	rate := int(v.captureRate.Load())
	if rate <= 0 {
		rate = 30
	}
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()

	var buf *image.RGBA
	var jpegBuf bytes.Buffer
	jpegBuf.Grow(256 * 1024)
	var lastSig uint64
	for {
		select {
		case <-v.done:
			return
		case <-v.rateChanged:
			r := int(v.captureRate.Load())
			if r <= 0 {
				r = 30
			}
			ticker.Reset(time.Second / time.Duration(r))
		case <-ticker.C:
			frame, err := v.source.Capture(buf)
			if err != nil {
				if v.frameRes == nil {
					v.app.post(func() {
						v.message = "等待画面帧：" + err.Error()
						v.render()
					})
				}
				continue
			}
			buf = frame
			fw, fh := frame.Bounds().Dx(), frame.Bounds().Dy()
			sig := frameSignature(frame)
			if sig == lastSig && fw == v.width && fh == v.height && v.frameRes != nil {
				continue
			}
			lastSig = sig
			jpgBytes := encodeRGBAAsFastJPEGInto(&jpegBuf, frame)
			if len(jpgBytes) == 0 {
				continue
			}
			payload := make([]byte, len(jpgBytes))
			copy(payload, jpgBytes)
			ack := make(chan struct{})
			v.app.post(func() {
				defer close(ack)
				select {
				case <-v.done:
					return
				default:
				}
				res, upErr := v.app.cli.UploadResource(v.app.ctx, client.ResourceImage, "image/jpeg", payload)
				if upErr != nil {
					if v.frameRes == nil {
						v.message = "画面上传失败：" + upErr.Error()
						v.render()
					}
					return
				}
				old := v.frameRes
				firstOrRotated := v.width == 0 || (fw > fh) != (v.width > v.height)
				v.frameRes = res
				v.width, v.height = fw, fh
				v.message = ""
				if firstOrRotated {
					setWindowAspectByTitle(v.title, fw, fh, v.chromeHeight(), true)
				}
				v.fpsFrames++
				if elapsed := time.Since(v.fpsStart); elapsed >= time.Second {
					v.liveFPS = float64(v.fpsFrames) / elapsed.Seconds()
					v.fpsFrames = 0
					v.fpsStart = time.Now()
				}
				v.render()
				if old != nil {
					_ = old.Release(v.app.ctx)
				}
			})
			select {
			case <-ack:
			case <-v.done:
				return
			}
		}
	}
}

func (v *emulatorViewer) pollRunningApps() {
	ticker := time.NewTicker(2500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-v.done:
			return
		case <-ticker.C:
			m, err := selectedManager(v.name)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			apps, err := m.ListRunningApps(ctx, avd.Instance{Serial: v.serial, Name: v.name})
			cancel()
			if err != nil {
				continue
			}
			v.app.post(func() {
				newFg := ""
				for _, a := range apps {
					if a.Foreground {
						newFg = a.Package
						break
					}
				}
				if runningAppsEqual(v.runningApps, apps) && newFg == v.activeAppPkg {
					return
				}
				v.runningApps = apps
				v.activeAppPkg = newFg
				v.render()
			})
		}
	}
}

func (v *emulatorViewer) enqueue(e pointerEvent) {
	select {
	case v.events <- e:
	default:
	}
}

func (v *emulatorViewer) releaseHostInput() {
	if v.held {
		v.held = false
		v.enqueue(pointerEvent{kind: pointerUp})
	}
	if len(v.keys) > 0 {
		clear(v.keys)
		v.enqueue(pointerEvent{kind: releaseKeys})
	}
}

func (v *emulatorViewer) viewportSize() (float32, float32) {
	vh := v.viewH - topBarHeight - bottomBarHeight
	if v.drawerOpen {
		vh -= drawerBarHeight
	}
	if vh < 60 {
		vh = 60
	}
	vw := v.viewW
	if vw < 60 {
		vw = 60
	}
	return vw, vh
}

func (v *emulatorViewer) mapPointer(pos *scene.Point, clamp bool) (int, int, float64, float64, bool) {
	if pos == nil || v.width <= 0 || v.height <= 0 {
		return 0, 0, 0, 0, false
	}
	topOffset := topBarHeight
	if v.drawerOpen {
		topOffset += drawerBarHeight
	}
	localY := pos.Y - topOffset
	if localY < 0 {
		if !clamp {
			return 0, 0, 0, 0, false
		}
		localY = 0
	}
	vw, vh := v.viewportSize()
	gx, gy, ok := display.MapPoint(v.fitMode, pos.X, localY, vw, vh, v.width, v.height, clamp)
	if !ok {
		return 0, 0, 0, 0, false
	}
	nx := float64(gx) / float64(max(1, v.width-1))
	ny := float64(gy) / float64(max(1, v.height-1))
	return gx, gy, nx, ny, true
}

func (v *emulatorViewer) handleViewportEvent(ev *client.Event) {
	switch ev.Kind.(type) {
	case *client.EventMouseDown:
		p := ev.GetMouseDown()
		if p == nil {
			return
		}
		if p.Button == client.MouseRight && v.settings.RightClickBack {
			v.enqueue(pointerEvent{kind: navigate, action: display.Back})
			return
		}
		if p.Button != client.MouseLeft {
			return
		}
		gx, gy, nx, ny, ok := v.mapPointer(p.Position, false)
		if !ok {
			return
		}
		if v.editing != 0 {
			if v.editing == 1 {
				v.pendingPoint = &mappedPoint{X: nx, Y: ny}
			} else if v.editing == 3 {
				rad := 0.12
				if v.joystick != nil {
					rad = v.joystick.Radius
				}
				_ = v.setJoystick(&joystickConfig{Center: mappedPoint{X: nx, Y: ny}, Radius: rad})
				v.editing = 1
			}
			v.render()
			return
		}
		v.held = true
		v.enqueue(pointerEvent{kind: pointerDown, x: gx, y: gy})
		v.recorder.Record(macro.Event{Kind: macro.EventTouchDown, NX: nx, NY: ny})
		v.app.syncHub.BroadcastTouchDown(v.name, nx, ny)

	case *client.EventMouseMove:
		if !v.held {
			return
		}
		p := ev.GetMouseMove()
		if p == nil {
			return
		}
		gx, gy, nx, ny, ok := v.mapPointer(p.Position, true)
		if ok {
			v.enqueue(pointerEvent{kind: pointerMove, x: gx, y: gy})
			v.recorder.Record(macro.Event{Kind: macro.EventTouchMove, NX: nx, NY: ny})
			v.app.syncHub.BroadcastTouchMove(v.name, nx, ny)
		}

	case *client.EventMouseUp:
		if !v.held {
			return
		}
		v.held = false
		v.enqueue(pointerEvent{kind: pointerUp})
		v.recorder.Record(macro.Event{Kind: macro.EventTouchUp})
		v.app.syncHub.BroadcastTouchUp(v.name)

	case *client.EventScroll:
		sc := ev.GetScroll()
		if sc == nil || sc.Delta == nil || v.width <= 0 || v.height <= 0 {
			return
		}
		gx, gy, _, _, ok := v.mapPointer(sc.Position, true)
		if !ok {
			return
		}
		dy := int(math.Round(float64(sc.Delta.Y) * 2.5))
		if dy == 0 {
			return
		}
		y2 := max(0, min(v.height-1, gy+dy))
		v.enqueue(pointerEvent{kind: pointerDown, x: gx, y: gy})
		v.enqueue(pointerEvent{kind: pointerMove, x: gx, y: y2})
		v.enqueue(pointerEvent{kind: pointerUp})

	case *client.EventKeyDown:
		ke := ev.GetKeyDown()
		if ke == nil {
			return
		}
		if strings.EqualFold(ke.Key, "f11") {
			_ = v.win.ToggleFullscreen(v.app.ctx)
			return
		}
		if strings.EqualFold(ke.Key, "escape") && v.editing == 0 {
			if _, bound := v.bindings[0x29]; !bound {
				v.enqueue(pointerEvent{kind: navigate, action: display.Back})
				v.app.syncHub.BroadcastNavigate(v.name, display.Back)
				return
			}
		}
		usage := gpuiKeyToHID(ke.Key)
		if usage == 0 {
			return
		}
		v.handleHostKey(usage, true)

	case *client.EventKeyUp:
		ke := ev.GetKeyUp()
		if ke == nil {
			return
		}
		usage := gpuiKeyToHID(ke.Key)
		if usage == 0 {
			return
		}
		v.handleHostKey(usage, false)

	case *client.EventFileDrop:
		fd := ev.GetFileDrop()
		if fd == nil || len(fd.Paths) == 0 {
			return
		}
		v.handleDroppedFiles(fd.Paths)
	}
}

func (v *emulatorViewer) handleHostKey(usage uint16, down bool) {
	if !display.ValidKeyboardUsage(usage) {
		return
	}
	if down && v.editing != 0 {
		if v.editing == 1 && v.pendingPoint != nil {
			_ = v.setBinding(usage, v.pendingPoint)
			v.pendingPoint = nil
			v.render()
		} else if v.editing == 2 {
			_ = v.setBinding(usage, nil)
			v.render()
		}
		return
	}
	if !down {
		if !v.keys[usage] {
			return
		}
		delete(v.keys, usage)
		if v.joystick != nil && joystickKey(usage) {
			v.enqueue(pointerEvent{kind: stickUp, usage: usage})
		} else if _, mapped := v.bindings[usage]; mapped {
			v.enqueue(pointerEvent{kind: mappedUp, usage: usage})
		} else {
			v.enqueue(pointerEvent{kind: keyUp, usage: usage})
		}
		v.recorder.Record(macro.Event{Kind: macro.EventKeyUp, Usage: usage})
		v.app.syncHub.BroadcastKey(v.name, usage, false)
		return
	}
	if v.keys[usage] {
		return
	}
	v.keys[usage] = true
	if v.joystick != nil && joystickKey(usage) && v.width > 0 && v.height > 0 {
		p := v.joystick
		v.enqueue(pointerEvent{
			kind: stickDown, usage: usage,
			x:      int(p.Center.X*float64(v.width-1) + 0.5),
			y:      int(p.Center.Y*float64(v.height-1) + 0.5),
			radius: max(1, int(p.Radius*float64(min(v.width-1, v.height-1))+0.5)),
			maxX:   v.width - 1, maxY: v.height - 1,
		})
	} else if p, ok := v.bindings[usage]; ok && v.width > 0 && v.height > 0 {
		v.enqueue(pointerEvent{
			kind: mappedDown, usage: usage,
			x: int(p.X*float64(v.width-1) + 0.5),
			y: int(p.Y*float64(v.height-1) + 0.5),
		})
	} else {
		v.enqueue(pointerEvent{kind: keyDown, usage: usage})
	}
	v.recorder.Record(macro.Event{Kind: macro.EventKeyDown, Usage: usage})
	v.app.syncHub.BroadcastKey(v.name, usage, true)
}

func (v *emulatorViewer) setBinding(usage uint16, point *mappedPoint) error {
	if v.keymap == nil {
		return errors.New("键位配置不可用")
	}
	next := make(map[uint16]mappedPoint, len(v.bindings)+1)
	for k, p := range v.bindings {
		next[k] = p
	}
	if point == nil {
		delete(next, usage)
	} else {
		next[usage] = *point
	}
	if err := v.keymap.save(next); err != nil {
		return err
	}
	v.bindings = next
	return nil
}

func (v *emulatorViewer) setJoystick(stick *joystickConfig) error {
	if v.keymap == nil {
		return errors.New("键位配置不可用")
	}
	if err := v.keymap.saveWithJoystick(v.bindings, stick); err != nil {
		return err
	}
	v.joystick = v.keymap.joystick
	return nil
}

// syncctl.Target implementation
func (v *emulatorViewer) SyncTouchDown(nx, ny float64) {
	if v.width <= 0 || v.height <= 0 {
		return
	}
	x := max(0, min(v.width-1, int(nx*float64(v.width-1)+0.5)))
	y := max(0, min(v.height-1, int(ny*float64(v.height-1)+0.5)))
	v.enqueue(pointerEvent{kind: pointerDown, x: x, y: y})
}
func (v *emulatorViewer) SyncTouchMove(nx, ny float64) {
	if v.width <= 0 || v.height <= 0 {
		return
	}
	x := max(0, min(v.width-1, int(nx*float64(v.width-1)+0.5)))
	y := max(0, min(v.height-1, int(ny*float64(v.height-1)+0.5)))
	v.enqueue(pointerEvent{kind: pointerMove, x: x, y: y})
}
func (v *emulatorViewer) SyncTouchUp()                    { v.enqueue(pointerEvent{kind: pointerUp}) }
func (v *emulatorViewer) SyncKey(usage uint16, down bool) { v.handleHostKey(usage, down) }
func (v *emulatorViewer) SyncNavigate(action display.Action) {
	v.enqueue(pointerEvent{kind: navigate, action: action})
}

func (v *emulatorViewer) handleDroppedFiles(paths []string) {
	v.message = fmt.Sprintf("正在处理拖入的 %d 个文件…", len(paths))
	v.render()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		m, err := selectedManager(v.name)
		var inst avd.Instance
		if err == nil {
			inst, err = m.Ready(ctx)
		}
		if err == nil {
			var apks []string
			var others []string
			for _, p := range paths {
				if strings.EqualFold(filepath.Ext(p), ".apk") {
					apks = append(apks, p)
				} else {
					others = append(others, p)
				}
			}
			if len(apks) > 0 {
				err = m.InstallAPKs(ctx, inst, apks, true)
			}
			for _, f := range others {
				if err == nil {
					_, err = m.ImportFile(ctx, inst, f)
				}
			}
		}
		v.app.post(func() {
			if err != nil {
				v.message = "拖入处理失败：" + err.Error()
			} else {
				v.message = "拖入文件处理完成"
			}
			v.render()
		})
	}()
}

func (v *emulatorViewer) saveScreenshot() {
	go func() {
		frame, err := v.source.Capture(nil)
		if err != nil {
			return
		}
		dir := filepath.Join(os.Getenv("USERPROFILE"), "Pictures", "NekoSimulator233")
		_ = os.MkdirAll(dir, 0o700)
		name := filepath.Join(dir, fmt.Sprintf("neko-%s.png", time.Now().Format("20060102-150405")))
		if f, err := os.Create(name); err == nil {
			_ = png.Encode(f, frame)
			_ = f.Close()
			v.app.post(func() { v.message = "截图已保存：" + name; v.render() })
		}
	}()
}

func (v *emulatorViewer) runADBTask(label string, fn func(context.Context, avd.Manager, avd.Instance) error) {
	v.message = label + "…"
	v.render()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		m, err := selectedManager(v.name)
		var inst avd.Instance
		if err == nil {
			inst, err = m.Ready(ctx)
		}
		if err == nil {
			err = fn(ctx, m, inst)
		}
		v.app.post(func() {
			if err != nil {
				v.message = label + "失败：" + err.Error()
			} else {
				v.message = label + "完成"
			}
			v.render()
		})
	}()
}

func (v *emulatorViewer) buildView() *scene.Node {
	children := []*scene.Node{v.buildTopChromeBar()}
	if v.drawerOpen {
		children = append(children, v.buildDrawer())
	}
	children = append(children, v.buildViewport(), v.buildBottomNavBar())

	return scene.Div(1, children...).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		Width:         scene.Percent(100),
		Height:        scene.Percent(100),
		Background:    scene.RGB(colBlack),
		FontFamily:    scene.Ptr[string](uiFontFamily),
	})
}

func (v *emulatorViewer) buildTopChromeBar() *scene.Node {
	// Left: Chrome-style tab strip (Home tab + dynamic running Android app tabs)
	homeActive := v.activeAppPkg == ""
	homeBg := uint32(colTabStripBg)
	if homeActive {
		homeBg = colTabActiveBg
	}
	tabs := []*scene.Node{
		scene.Div(eid(),
			rasterIcon(v.app.icons.Logo, 18),
			labelText(v.name, 12, colTextPrimary, homeActive),
		).Focusable(0).OnClick(v.click(func() {
			v.activeAppPkg = ""
			v.enqueue(pointerEvent{kind: navigate, action: display.Home})
			v.app.syncHub.BroadcastNavigate(v.name, display.Home)
			v.render()
		})).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(6),
			Height:        scene.Pixels(32),
			Padding:       &scene.Edges{Left: scene.Pixels(10), Right: scene.Pixels(12)},
			Background:    scene.RGB(homeBg),
			CornerRadius:  scene.Ptr[float32](8),
			Cursor:        scene.CursorPointer.Enum(),
		}),
	}

	for _, ra := range v.runningApps {
		appItem := ra
		active := v.activeAppPkg == appItem.Package
		bg := uint32(colTabStripBg)
		if active {
			bg = colTabActiveBg
		}
		tabs = append(tabs, scene.Div(eid(),
			scene.Div(eid(),
				labelText(appItem.Label, 12, colTextPrimary, active),
			).Focusable(0).OnClick(v.click(func() {
				v.activeAppPkg = appItem.Package
				v.runADBTask("切换 "+appItem.Label, func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
					return m.LaunchApp(ctx, inst, appItem.Package)
				})
			})).Style(&scene.Style{Cursor: scene.CursorPointer.Enum()}),
			scene.Div(eid(), svgIcon(v.app.icons.Close, 11, colTextMuted)).
				Focusable(0).
				OnClick(v.click(func() {
					v.runADBTask("关闭 "+appItem.Label, func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
						return m.CloseApp(ctx, inst, appItem.Package)
					})
				})).
				Style(&scene.Style{Padding: scene.Padding(2), CornerRadius: scene.Ptr[float32](4), Cursor: scene.CursorPointer.Enum()}).
				Hover(&scene.Style{Background: scene.RGB(colBgHover)}),
		).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(6),
			Height:        scene.Pixels(32),
			Padding:       &scene.Edges{Left: scene.Pixels(10), Right: scene.Pixels(6)},
			Background:    scene.RGB(bg),
			CornerRadius:  scene.Ptr[float32](8),
		}))
	}

	toolIcon := func(icon *client.Resource, active bool, tip string, fn func()) *scene.Node {
		fg := uint32(colTextPrimary)
		bg := uint32(colTabStripBg)
		if active {
			fg = colPrimaryBlue
			bg = colBgHover
		}
		return scene.Div(eid(), svgIcon(icon, 16, fg)).
			Focusable(0).
			OnClick(v.click(fn)).
			Accessible(tip, &scene.Accessibility{Role: scene.RoleButton}).
			Style(&scene.Style{
				Display:        scene.DisplayFlex.Enum(),
				AlignItems:     scene.AlignCenter.Enum(),
				JustifyContent: scene.JustifyCenter.Enum(),
				Width:          scene.Pixels(30),
				Height:         scene.Pixels(30),
				Background:     scene.RGB(bg),
				CornerRadius:   scene.Ptr[float32](6),
				Cursor:         scene.CursorPointer.Enum(),
			}).Hover(&scene.Style{Background: scene.RGB(colBgHover)})
	}

	volIcon := v.app.icons.Volume
	if v.muted {
		volIcon = v.app.icons.Mute
	}

	winCtrlBtn := func(symbol string, isClose bool, fn func()) *scene.Node {
		hoverBg := uint32(colBgHover)
		if isClose {
			hoverBg = colRedBg
		}
		return scene.Div(eid(), labelText(symbol, 12, colTextPrimary, false)).
			Focusable(0).
			OnClick(v.click(fn)).
			Style(&scene.Style{
				Display:        scene.DisplayFlex.Enum(),
				AlignItems:     scene.AlignCenter.Enum(),
				JustifyContent: scene.JustifyCenter.Enum(),
				Width:          scene.Pixels(32),
				Height:         scene.Pixels(32),
				CornerRadius:   scene.Ptr[float32](6),
				Cursor:         scene.CursorPointer.Enum(),
			}).Hover(&scene.Style{Background: scene.RGB(hoverBg)})
	}

	rightTools := scene.Div(eid(),
		toolIcon(v.app.icons.Sync, v.app.syncHub.ActiveFor(v.name), "多开同步", func() {
			if v.app.syncHub.ActiveFor(v.name) {
				v.app.syncHub.Stop()
			} else {
				v.app.syncHub.Start(v.name)
			}
			v.render()
		}),
		toolIcon(v.app.icons.Keymap, v.editing != 0, "键鼠映射", func() {
			if v.editing != 0 {
				v.editing = 0
			} else {
				v.editing = 1
				v.drawerOpen = true
				v.drawerTab = "keymap"
			}
			v.render()
		}),
		toolIcon(volIcon, v.muted, "静音/取消静音", func() {
			v.muted = !v.muted
			mute := v.muted
			v.runADBTask("音量设置", func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
				return m.SetMusicMute(ctx, inst.Serial, mute)
			})
		}),
		toolIcon(v.app.icons.Back, false, "返回", func() {
			v.enqueue(pointerEvent{kind: navigate, action: display.Back})
			v.app.syncHub.BroadcastNavigate(v.name, display.Back)
		}),
		toolIcon(v.app.icons.More, v.drawerOpen, "更多工具", func() {
			v.drawerOpen = !v.drawerOpen
			setWindowAspectByTitle(v.title, v.width, v.height, v.chromeHeight(), true)
			v.render()
		}),
		scene.Div(eid()).Style(&scene.Style{
			Width:      scene.Pixels(1),
			Height:     scene.Pixels(18),
			Background: scene.RGB(colBorderLight),
			Margin:     &scene.Edges{Left: scene.Pixels(4), Right: scene.Pixels(2)},
		}),
		winCtrlBtn("─", false, func() { minimizeWindowByTitle(v.title) }),
		winCtrlBtn("□", false, func() { toggleMaximizeWindowByTitle(v.title) }),
		winCtrlBtn("✕", true, func() { v.closeWindow() }),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexRow.Enum(),
		AlignItems:    scene.AlignCenter.Enum(),
		Gap:           scene.Pixels(2),
		FlexShrink:    scene.Ptr[float32](0),
	})

	hDrag := v.interact(func(ev *client.Event) {
		if md := ev.GetMouseDown(); md != nil && md.Button == client.MouseLeft {
			dragWindowByTitle(v.title)
		}
	})

	return scene.Div(eid(),
		scene.Div(eid(), tabs...).Style(&scene.Style{
			Display:       scene.DisplayFlex.Enum(),
			FlexDirection: scene.FlexRow.Enum(),
			AlignItems:    scene.AlignCenter.Enum(),
			Gap:           scene.Pixels(4),
			OverflowX:     scene.OverflowHidden.Enum(),
		}),
		scene.Div(eid()).
			Interact(&scene.Interaction{MouseDown: hDrag}).
			Style(&scene.Style{
				FlexGrow: scene.Ptr[float32](1),
				Height:   scene.Percent(100),
			}),
		rightTools,
	).Style(&scene.Style{
		Display:        scene.DisplayFlex.Enum(),
		FlexDirection:  scene.FlexRow.Enum(),
		AlignItems:     scene.AlignCenter.Enum(),
		JustifyContent: scene.JustifySpaceBetween.Enum(),
		Width:          scene.Percent(100),
		Height:         scene.Pixels(topBarHeight),
		Padding:        &scene.Edges{Left: scene.Pixels(8), Right: scene.Pixels(8)},
		Background:     scene.RGB(colTabStripBg),
		BorderWidth:    scene.Ptr[float32](1),
		BorderColor:    scene.RGB(colBorderLight),
		FlexShrink:     scene.Ptr[float32](0),
	})
}

func (v *emulatorViewer) buildDrawer() *scene.Node {
	tabBtn := func(id, title string) *scene.Node {
		active := v.drawerTab == id
		bg, fg := uint32(colBgCard), uint32(colTextMuted)
		if active {
			bg, fg = colPrimaryBlue, colWhite
		}
		return pillButton(title, v.click(func() { v.drawerTab = id; v.render() }), bg, colBgHover, fg, colBorderLight)
	}
	header := scene.Div(eid(),
		tabBtn("tools", "常用工具"),
		tabBtn("apps", "应用管理"),
		tabBtn("keymap", "键鼠映射"),
		tabBtn("macro", "操作宏"),
		tabBtn("sync", "多开同步"),
		tabBtn("sensor", "定位/传感器"),
		tabBtn("network", "网络/ADB"),
		labelText(v.message, 12, colTextMuted, false),
	).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexRow.Enum(),
		AlignItems:    scene.AlignCenter.Enum(),
		Gap:           scene.Pixels(6),
		FlexWrap:      scene.Ptr(true),
	})

	var body *scene.Node
	switch v.drawerTab {
	case "apps":
		body = v.buildAppsDrawer()
	case "keymap":
		body = v.buildKeymapDrawer()
	case "macro":
		body = v.buildMacroDrawer()
	case "sync":
		body = v.buildSyncDrawer()
	case "sensor":
		body = v.buildSensorDrawer()
	case "network":
		body = v.buildNetworkDrawer()
	default:
		body = v.buildToolsDrawer()
	}

	return scene.Div(eid(), header, body).Style(&scene.Style{
		Display:       scene.DisplayFlex.Enum(),
		FlexDirection: scene.FlexColumn.Enum(),
		Width:         scene.Percent(100),
		Height:        scene.Pixels(drawerBarHeight),
		Padding:       scene.Padding(10),
		Gap:           scene.Pixels(8),
		Background:    scene.RGB(colBgCard),
		BorderWidth:   scene.Ptr[float32](1),
		BorderColor:   scene.RGB(colBorderLight),
		OverflowY:     scene.OverflowScroll.Enum(),
		FlexShrink:    scene.Ptr[float32](0),
	})
}

func (v *emulatorViewer) buildToolsDrawer() *scene.Node {
	return scene.Div(eid(),
		pillButton("主页", v.click(func() { v.enqueue(pointerEvent{kind: navigate, action: display.Home}) }), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("最近任务", v.click(func() { v.enqueue(pointerEvent{kind: navigate, action: display.Overview}) }), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("音量+", v.click(func() { v.enqueue(pointerEvent{kind: navigate, action: display.VolumeUp}) }), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("音量−", v.click(func() { v.enqueue(pointerEvent{kind: navigate, action: display.VolumeDown}) }), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("截图", v.click(v.saveScreenshot), colPrimaryBlue, colPrimaryHover, colWhite, 0),
		pillButton("旋转屏幕", v.click(func() {
			v.landscape = !v.landscape
			land := v.landscape
			v.runADBTask("旋转屏幕", func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
				return m.RotateDisplay(ctx, inst, land)
			})
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton(fmt.Sprintf("置顶：%v", v.topmost), v.click(func() {
			v.topmost = !v.topmost
			setWindowTopmostByTitle(v.title, v.topmost)
			v.render()
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("全屏", v.click(func() { _ = v.win.ToggleFullscreen(v.app.ctx) }), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("共享文件夹", v.click(func() { _ = openSharedFolder(v.name) }), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("安装 APK…", v.click(func() {
			_, _ = v.app.cli.PromptPaths(v.app.ctx, &client.PathPrompt{Files: true, Multiple: true, Prompt: "选择 APK"}, func(_ context.Context, res *client.PathResult) error {
				if res != nil && !res.Cancelled && len(res.Paths) > 0 {
					v.handleDroppedFiles(res.Paths)
				}
				return nil
			})
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("导入文件…", v.click(func() {
			_, _ = v.app.cli.PromptPaths(v.app.ctx, &client.PathPrompt{Files: true, Multiple: true, Prompt: "选择要导入的文件"}, func(_ context.Context, res *client.PathResult) error {
				if res != nil && !res.Cancelled && len(res.Paths) > 0 {
					v.handleDroppedFiles(res.Paths)
				}
				return nil
			})
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("缩放: "+v.fitMode.String(), v.click(func() {
			switch v.fitMode {
			case display.FitContain:
				v.fitMode = display.FitCover
			case display.FitCover:
				v.fitMode = display.FitStretch
			default:
				v.fitMode = display.FitContain
			}
			v.render()
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton(fmt.Sprintf("帧率: %d", v.captureRate.Load()), v.click(func() {
			cur := v.captureRate.Load()
			next := int32(60)
			if cur == 15 {
				next = 30
			} else if cur == 30 {
				next = 60
			} else if cur == 60 {
				next = 120
			} else {
				next = 15
			}
			v.captureRate.Store(next)
			select {
			case v.rateChanged <- struct{}{}:
			default:
			}
			v.render()
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("发送宿主机剪贴板", v.click(func() {
			if txt, err := v.app.cli.ClipboardText(v.app.ctx); err == nil {
				if ctrl, ok := v.source.(*avd.Controller); ok {
					_ = ctrl.SetClipboard(txt)
					v.message = "已同步剪贴板到 Android"
					v.render()
				}
			}
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
		pillButton("同步电脑时间/地区", v.click(func() {
			v.runADBTask("同步电脑时间与地区", func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
				return m.SyncHostTimeAndLocale(ctx, inst)
			})
		}), colBgSubtle, colBgHover, colTextPrimary, 0),
	).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), FlexWrap: scene.Ptr(true), Gap: scene.Pixels(6)})
}

func (v *emulatorViewer) buildKeymapDrawer() *scene.Node {
	editLabel := "开启点击绑定"
	if v.editing == 1 {
		editLabel = "退出键位编辑"
	}
	profileRow := []*scene.Node{
		labelText(fmt.Sprintf("方案：%s（%d 键）", v.profile, len(v.bindings)), 12, colTextPrimary, true),
	}
	for _, p := range v.profiles {
		prof := p
		bg, fg := uint32(colBgSubtle), uint32(colTextPrimary)
		if prof == v.profile {
			bg, fg = colPrimaryBlue, colWhite
		}
		profileRow = append(profileRow, pillButton(prof, v.click(func() {
			_ = v.switchKeymapProfile(prof)
			v.render()
		}), bg, colBgHover, fg, 0))
	}
	return scene.Div(eid(),
		scene.Div(eid(), profileRow...).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), FlexWrap: scene.Ptr(true), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(6)}),
		scene.Div(eid(),
			pillButton(editLabel, v.click(func() {
				if v.editing != 0 {
					v.editing = 0
				} else {
					v.editing = 1
				}
				v.pendingPoint = nil
				v.render()
			}), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			pillButton("删除键位模式", v.click(func() { v.editing = 2; v.pendingPoint = nil; v.render() }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("放置 WASD 摇杆", v.click(func() { v.editing = 3; v.pendingPoint = nil; v.message = "点击画面中摇杆中心"; v.render() }), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("FPS 射击预设", v.click(func() {
				_ = v.setJoystick(&joystickConfig{Center: mappedPoint{X: 0.18, Y: 0.72}, Radius: 0.11})
				_ = v.setBinding(0x2c, &mappedPoint{X: 0.85, Y: 0.78}) // Space -> Jump
				_ = v.setBinding(0x15, &mappedPoint{X: 0.78, Y: 0.85}) // R -> Reload
				_ = v.setBinding(0x09, &mappedPoint{X: 0.88, Y: 0.60}) // F -> Fire/Interact
				v.message = "已应用 FPS 射击键位预设"
				v.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("MOBA 预设", v.click(func() {
				_ = v.setJoystick(&joystickConfig{Center: mappedPoint{X: 0.16, Y: 0.74}, Radius: 0.11})
				_ = v.setBinding(0x14, &mappedPoint{X: 0.72, Y: 0.82}) // Q
				_ = v.setBinding(0x08, &mappedPoint{X: 0.80, Y: 0.70}) // E
				_ = v.setBinding(0x15, &mappedPoint{X: 0.88, Y: 0.60}) // R
				v.message = "已应用 MOBA 键位预设"
				v.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("清除摇杆", v.click(func() { _ = v.setJoystick(nil); v.render() }), colRedBg, colRedBg, colRedText, 0),
			scene.TextInput(idProfileInput, v.profileIn, scene.TextInputOptions{Placeholder: "新方案名称", OnChange: v.hProfileIn}).
				Style(&scene.Style{Width: scene.Pixels(110), Padding: scene.Padding(5), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](12)}),
			pillButton("保存为新方案", v.click(func() {
				_ = v.createKeymapProfile(v.profileIn.Value)
				v.profileIn.Reset("")
				v.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), FlexWrap: scene.Ptr(true), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(6)}),
	).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(6)})
}

func (v *emulatorViewer) buildMacroDrawer() *scene.Node {
	recBtn := pillButton("开始录制", v.click(func() { v.recorder.Start(); v.render() }), colPrimaryBlue, colPrimaryHover, colWhite, 0)
	if v.recorder.Active() {
		recBtn = pillButton(fmt.Sprintf("停止并保存 (%d)", v.recorder.Count()), v.click(func() {
			name := strings.TrimSpace(v.macroNameIn.Value)
			if name == "" {
				name = "macro-" + time.Now().Format("150405")
			}
			sc := v.recorder.Stop(name)
			if err := v.app.macroStore.Save(sc); err != nil {
				v.message = err.Error()
			} else {
				v.macros, _ = v.app.macroStore.List()
				v.macroNameIn.Reset("")
				v.message = "宏已保存：" + name
			}
			v.render()
		}), colRedBg, colRedBg, colRedText, 0)
	}
	rows := []*scene.Node{
		scene.Div(eid(),
			scene.TextInput(idMacroNameInput, v.macroNameIn, scene.TextInputOptions{Placeholder: "宏脚本名称", OnChange: v.hMacroIn}).
				Style(&scene.Style{Width: scene.Pixels(140), Padding: scene.Padding(5), Background: scene.RGB(colBgPage), BorderWidth: scene.Ptr[float32](1), BorderColor: scene.RGB(colBorderLight), CornerRadius: scene.Ptr[float32](6), FontSize: scene.Ptr[float32](12)}),
			recBtn,
			pillButton(fmt.Sprintf("循环: %dx", v.macroLoops), v.click(func() {
				if v.macroLoops == 1 {
					v.macroLoops = 3
				} else if v.macroLoops == 3 {
					v.macroLoops = 10
				} else {
					v.macroLoops = 1
				}
				v.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton(fmt.Sprintf("倍速: %.1fx", v.macroSpeed), v.click(func() {
				if v.macroSpeed == 1.0 {
					v.macroSpeed = 2.0
				} else if v.macroSpeed == 2.0 {
					v.macroSpeed = 0.5
				} else {
					v.macroSpeed = 1.0
				}
				v.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(6)}),
	}
	for _, m := range v.macros {
		sc := m
		rows = append(rows, scene.Div(eid(),
			labelText(fmt.Sprintf("%s (%d事件, %.1fs)", sc.Name, len(sc.Events), float64(sc.DurationMS)/1000), 12, colTextPrimary, false),
			pillButton("播放", v.click(func() {
				if v.replayCancel != nil {
					v.replayCancel()
				}
				ctx, cancel := context.WithCancel(v.app.ctx)
				v.replayCancel = cancel
				loops, speed := v.macroLoops, v.macroSpeed
				go func() {
					_ = macro.Replay(ctx, sc, macro.ReplayOptions{Loops: loops, Speed: speed}, func(ev macro.Event) error {
						switch ev.Kind {
						case macro.EventTouchDown:
							v.SyncTouchDown(ev.NX, ev.NY)
						case macro.EventTouchMove:
							v.SyncTouchMove(ev.NX, ev.NY)
						case macro.EventTouchUp:
							v.SyncTouchUp()
						case macro.EventKeyDown:
							v.handleHostKey(ev.Usage, true)
						case macro.EventKeyUp:
							v.handleHostKey(ev.Usage, false)
						}
						return nil
					})
				}()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("删除", v.click(func() {
				_ = v.app.macroStore.Delete(sc.Name)
				v.macros, _ = v.app.macroStore.List()
				v.render()
			}), colRedBg, colRedBg, colRedText, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(6)}))
	}
	return scene.Div(eid(), rows...).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(6)})
}

func (v *emulatorViewer) buildSyncDrawer() *scene.Node {
	peers := v.app.syncHub.Peers(v.name)
	nodes := []*scene.Node{
		pillButton(func() string {
			if v.app.syncHub.ActiveFor(v.name) {
				return "停止以此窗口为主控同步"
			}
			return "开始以此窗口为主控同步"
		}(), v.click(func() {
			if v.app.syncHub.ActiveFor(v.name) {
				v.app.syncHub.Stop()
			} else {
				v.app.syncHub.Start(v.name)
			}
			v.render()
		}), colPrimaryBlue, colPrimaryHover, colWhite, 0),
	}
	if len(peers) == 0 {
		nodes = append(nodes, labelText("当前没有其他已打开的模拟器窗口", 12, colTextMuted, false))
	}
	for _, peer := range peers {
		p := peer
		sel := v.app.syncHub.IsSelected(p)
		lbl := "[ ] " + p
		if sel {
			lbl = "[✓] " + p
		}
		nodes = append(nodes, pillButton(lbl, v.click(func() {
			v.app.syncHub.SetSelected(p, !sel)
			v.render()
		}), colBgSubtle, colBgHover, colTextPrimary, 0))
	}
	return scene.Div(eid(), nodes...).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), FlexWrap: scene.Ptr(true), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(6)})
}

func (v *emulatorViewer) buildSensorDrawer() *scene.Node {
	nodes := []*scene.Node{
		pillButton("摇一摇", v.click(func() {
			v.runADBTask("摇一摇", func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
				return m.Shake(ctx, inst)
			})
		}), colPrimaryBlue, colPrimaryHover, colWhite, 0),
	}
	for _, p := range avd.GPSPresets() {
		preset := p
		nodes = append(nodes, pillButton("定位:"+preset.Name, v.click(func() {
			v.runADBTask("虚拟定位 "+preset.Name, func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
				return m.SetGPSLocation(ctx, inst, preset.Lat, preset.Lon, 10)
			})
		}), colBgSubtle, colBgHover, colTextPrimary, 0))
	}
	for _, br := range []int{25, 50, 80, 100} {
		pct := br
		nodes = append(nodes, pillButton(fmt.Sprintf("亮度 %d%%", pct), v.click(func() {
			v.runADBTask("屏幕亮度", func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
				return m.SetBrightness(ctx, inst, pct)
			})
		}), colBgSubtle, colBgHover, colTextPrimary, 0))
	}
	return scene.Div(eid(), nodes...).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), FlexWrap: scene.Ptr(true), Gap: scene.Pixels(6)})
}

func (v *emulatorViewer) buildAppsDrawer() *scene.Node {
	rows := []*scene.Node{
		scene.Div(eid(),
			labelText(fmt.Sprintf("已运行应用 %d 个 / 第三方包 %d 个", len(v.runningApps), len(v.installedApps)), 12, colTextPrimary, true),
			pillButton("刷新应用列表", v.click(func() {
				go func() {
					m, err := selectedManager(v.name)
					if err != nil {
						return
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					pkgs, _ := m.ListApps(ctx, avd.Instance{Serial: v.serial, Name: v.name})
					cancel()
					v.app.post(func() {
						v.installedApps = pkgs
						v.render()
					})
				}()
			}), colPrimaryBlue, colPrimaryHover, colWhite, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(8)}),
	}
	for _, pkg := range v.installedApps {
		pkgName := pkg
		rows = append(rows, scene.Div(eid(),
			labelText(pkgName, 12, colTextPrimary, false),
			pillButton("启动", v.click(func() {
				v.runADBTask("启动 "+pkgName, func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
					return m.LaunchApp(ctx, inst, pkgName)
				})
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("强行停止", v.click(func() {
				v.runADBTask("停止 "+pkgName, func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
					return m.CloseApp(ctx, inst, pkgName)
				})
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("清数据", v.click(func() {
				v.runADBTask("清数据 "+pkgName, func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
					return m.ClearAppData(ctx, inst, pkgName)
				})
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
			pillButton("卸载", v.click(func() {
				v.runADBTask("卸载 "+pkgName, func(ctx context.Context, m avd.Manager, inst avd.Instance) error {
					return m.UninstallApp(ctx, inst, pkgName)
				})
			}), colRedBg, colRedBg, colRedText, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), AlignItems: scene.AlignCenter.Enum(), Gap: scene.Pixels(6)}))
	}
	return scene.Div(eid(), rows...).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(6)})
}

func (v *emulatorViewer) buildNetworkDrawer() *scene.Node {
	adbMode := v.settings.ADBDebugMode
	if adbMode == "" {
		adbMode = "local_connect"
	}
	renderer := v.settings.RendererMode
	if renderer == "" {
		renderer = "vk"
	}
	var ifaces []string
	if nis, err := net.Interfaces(); err == nil {
		for _, ni := range nis {
			if ni.Flags&net.FlagUp != 0 && ni.Flags&net.FlagLoopback == 0 {
				ifaces = append(ifaces, ni.Name)
			}
		}
	}
	ifaceStr := "无"
	if len(ifaces) > 0 {
		ifaceStr = strings.Join(ifaces, ", ")
	}
	return scene.Div(eid(),
		labelText(fmt.Sprintf("ADB 序列号：%s  |  ADB 调试：%s  |  渲染后端：%s", v.serial, adbMode, renderer), 12, colTextPrimary, true),
		labelText("宿主机可用桥接网卡："+ifaceStr, 12, colTextMuted, false),
		scene.Div(eid(),
			pillButton("打开实例设置（网络/机型/性能）", v.click(func() {
				v.app.openDeviceSettings(v.name)
			}), colPrimaryBlue, colPrimaryHover, colWhite, 0),
			pillButton("切换 Root 模式", v.click(func() {
				v.settings.RootMode = !v.settings.RootMode
				if m, err := selectedManager(v.name); err == nil {
					_ = m.SetInstanceSettings(v.settings)
				}
				v.message = fmt.Sprintf("Root 模式已设为：%v（重启生效）", v.settings.RootMode)
				v.render()
			}), colBgSubtle, colBgHover, colTextPrimary, 0),
		).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexRow.Enum(), Gap: scene.Pixels(6)}),
	).Style(&scene.Style{Display: scene.DisplayFlex.Enum(), FlexDirection: scene.FlexColumn.Enum(), Gap: scene.Pixels(6)})
}

func (v *emulatorViewer) buildViewport() *scene.Node {
	hViewport := v.interact(v.handleViewportEvent)
	children := []*scene.Node{}

	if v.frameRes != nil && v.width > 0 && v.height > 0 {
		vw, vh := v.viewportSize()
		imgW, imgH := vw, vh
		if v.fitMode == display.FitContain {
			scale := math.Min(float64(vw)/float64(v.width), float64(vh)/float64(v.height))
			imgW = float32(float64(v.width) * scale)
			imgH = float32(float64(v.height) * scale)
		}
		children = append(children, scene.Image(eid(), v.frameRes.Handle()).Style(&scene.Style{
			Width:  scene.Pixels(imgW),
			Height: scene.Pixels(imgH),
		}))
	} else {
		children = append(children, labelText(v.message, 14, colTextSubtle, false))
	}

	if v.editing != 0 {
		vw, vh := v.viewportSize()
		children = append(children, buildKeymapOverlayNodes(vw, vh, v.width, v.height, v.bindings, v.joystick, v.pendingPoint)...)
	}

	if v.settings.ShowFPS {
		children = append(children, scene.Div(eid(),
			labelText(fmt.Sprintf("FPS %.0f", v.liveFPS), 11, colGreenText, true),
		).Style(&scene.Style{
			Position:     scene.PositionAbsolute.Enum(),
			Inset:        &scene.Edges{Top: scene.Pixels(8), Right: scene.Pixels(10)},
			Padding:      &scene.Edges{Top: scene.Pixels(2), Bottom: scene.Pixels(2), Left: scene.Pixels(8), Right: scene.Pixels(8)},
			Background:   scene.RGB(0x111827),
			CornerRadius: scene.Ptr[float32](8),
		}))
	}

	return scene.Div(idViewerViewport, children...).
		Focusable(0).
		Interact(&scene.Interaction{
			Focusable:   true,
			MouseDown:   hViewport,
			MouseUp:     hViewport,
			MouseMove:   hViewport,
			Scroll:      hViewport,
			KeyDown:     hViewport,
			KeyUp:       hViewport,
			FileDrop:    hViewport,
		}).
		Style(&scene.Style{
			Position:       scene.PositionRelative.Enum(),
			Display:        scene.DisplayFlex.Enum(),
			AlignItems:     scene.AlignCenter.Enum(),
			JustifyContent: scene.JustifyCenter.Enum(),
			Width:          scene.Percent(100),
			FlexGrow:       scene.Ptr[float32](1),
			Background:     scene.RGB(colBlack),
			OverflowX:      scene.OverflowHidden.Enum(),
			OverflowY:      scene.OverflowHidden.Enum(),
		})
}

func (v *emulatorViewer) chromeHeight() int {
	h := int(topBarHeight + bottomBarHeight)
	if v.drawerOpen {
		h += int(drawerBarHeight)
	}
	return h
}

func runningAppsEqual(a, b []avd.RunningApp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v *emulatorViewer) buildBottomNavBar() *scene.Node {
	navBtn := func(label string, tip string, fn func()) *scene.Node {
		return scene.Div(eid(), labelText(label, 13, 0xE5E7EB, true)).
			Focusable(0).
			OnClick(v.click(fn)).
			Accessible(tip, &scene.Accessibility{Role: scene.RoleButton}).
			Style(&scene.Style{
				Display:        scene.DisplayFlex.Enum(),
				AlignItems:     scene.AlignCenter.Enum(),
				JustifyContent: scene.JustifyCenter.Enum(),
				Height:         scene.Pixels(26),
				Padding:        &scene.Edges{Left: scene.Pixels(18), Right: scene.Pixels(18)},
				Background:     scene.RGB(0x1F2937),
				CornerRadius:   scene.Ptr[float32](13),
				Cursor:         scene.CursorPointer.Enum(),
			}).Hover(&scene.Style{Background: scene.RGB(0x374151)})
	}

	return scene.Div(eid(),
		navBtn("◁  返回", "返回上一步", func() {
			v.enqueue(pointerEvent{kind: navigate, action: display.Back})
			v.app.syncHub.BroadcastNavigate(v.name, display.Back)
		}),
		navBtn("○  主页", "回到桌面 Home", func() {
			v.activeAppPkg = ""
			v.enqueue(pointerEvent{kind: navigate, action: display.Home})
			v.app.syncHub.BroadcastNavigate(v.name, display.Home)
			v.render()
		}),
		navBtn("▢  多任务", "最近任务", func() {
			v.enqueue(pointerEvent{kind: navigate, action: display.Overview})
			v.app.syncHub.BroadcastNavigate(v.name, display.Overview)
		}),
	).Style(&scene.Style{
		Display:        scene.DisplayFlex.Enum(),
		FlexDirection:  scene.FlexRow.Enum(),
		AlignItems:     scene.AlignCenter.Enum(),
		JustifyContent: scene.JustifyCenter.Enum(),
		Width:          scene.Percent(100),
		Height:         scene.Pixels(34),
		Gap:            scene.Pixels(16),
		Background:     scene.RGB(0x111827),
		FlexShrink:     scene.Ptr[float32](0),
	})
}
