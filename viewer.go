package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/display"
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

type viewer struct {
	win           *mygo.Window
	source        display.Session
	bitmap        *ui.Bitmap
	width, height int
	message       string
	inputError    string
	held          bool
	keys          map[uint16]bool
	screen        ui.Handle
	events        chan pointerEvent
	done          chan struct{}
	once          sync.Once
	streamMu      sync.Mutex
	streamCancel  context.CancelFunc
	captureRate   atomic.Int32
	rateChanged   chan struct{}
	hidden        atomic.Bool
	minimized     atomic.Bool
	bindings      map[uint16]mappedPoint
	joystick      *joystickConfig
	keymap        *keymapStore
	baseKeymap    string
	profile       string
	profiles      []string
	profileName   string
	editing       int // 0 normal, 1 bind, 2 remove, 3 joystick position
	pendingPoint  *mappedPoint
	mappingError  string
}

func openViewer(name string, source display.Session, bindingsPath string, onClosed func()) *viewer {
	v := &viewer{source: source, events: make(chan pointerEvent, 64), done: make(chan struct{}), rateChanged: make(chan struct{}, 1), message: "正在读取画面…"}
	if _, ok := source.(display.MultiTouch); ok {
		v.baseKeymap = bindingsPath
		v.profile = defaultKeymapProfile
		v.keymap = &keymapStore{path: bindingsPath}
		var err error
		v.bindings, err = v.keymap.read()
		v.joystick = v.keymap.joystick
		if err != nil {
			v.mappingError = err.Error()
			v.keymap = nil
		} else if v.profiles, err = listKeymapProfiles(bindingsPath); err != nil {
			v.mappingError = err.Error()
		}
	}
	v.win = mygo.NewWindow(mygo.WindowOptions{Title: name + " - NekoSimulator233", Width: 1000, Height: 650, Content: ui.View(v.view)})
	v.win.OnClosed(func() {
		v.stop()
		if onClosed != nil {
			onClosed()
		}
	})
	v.win.OnBlur(v.releaseHostInput)
	v.win.OnHide(func() { v.hidden.Store(true); v.cancelStream(); v.releaseHostInput() })
	v.win.OnShow(func() { v.hidden.Store(false) })
	v.win.OnMinimize(func() { v.minimized.Store(true); v.cancelStream(); v.releaseHostInput() })
	v.win.OnRestore(func() { v.minimized.Store(false) })
	go v.run()
	return v
}

func (v *viewer) stop() { v.once.Do(func() { close(v.done); v.cancelStream() }) }

func (v *viewer) cancelStream() {
	v.streamMu.Lock()
	defer v.streamMu.Unlock()
	if v.streamCancel != nil {
		v.streamCancel()
	}
}

// The setting limits polling requests rather than promising displayed FPS.
// A single notification suffices because the consumer always reads the latest
// atomic value, even if the user changes the setting several times quickly.
func (v *viewer) setCaptureRate(rate int) bool {
	if rate != 15 && rate != 30 && rate != 60 && rate != 120 {
		return false
	}
	v.captureRate.Store(int32(rate))
	select {
	case v.rateChanged <- struct{}{}:
	default:
	}
	return true
}

func (v *viewer) currentCaptureRate() int {
	if rate := int(v.captureRate.Load()); rate != 0 {
		return rate
	}
	return 30
}

func (v *viewer) run() {
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		input := newViewerInput(v.source)
		defer input.release()
		for {
			select {
			case <-v.done:
				return
			case ev := <-v.events:
				if err := input.handle(ev); err != nil {
					v.reportInput(err)
				}
			}
		}
	}()
	defer func() {
		<-inputDone
		_ = v.source.Close()
	}()
	if source, ok := v.source.(display.FrameStreamer); ok && os.Getenv("NEKO_EXPERIMENTAL_STREAM") == "1" {
		if v.runStream(source) {
			return
		}
	}
	ticker := time.NewTicker(time.Second / time.Duration(v.currentCaptureRate()))
	defer ticker.Stop()
	var frames [2]*image.RGBA
	frameIndex := 0
	for {
		select {
		case <-v.done:
			return
		case <-v.rateChanged:
			ticker.Reset(time.Second / time.Duration(v.currentCaptureRate()))
		case <-ticker.C:
			if v.hidden.Load() || v.minimized.Load() {
				continue
			}
			frame, err := v.source.Capture(frames[frameIndex])
			select {
			case <-v.done:
				return
			default:
			}
			if err != nil {
				v.report(err)
				continue
			}
			frames[frameIndex] = frame
			if !v.presentFrame(frame) {
				return
			}
			frameIndex = 1 - frameIndex
		}
	}
}

// presentFrame waits for the UI to accept the frame before reusing buffers.
func (v *viewer) presentFrame(frame *image.RGBA) bool {
	ack := make(chan struct{})
	v.win.Update(func() {
		defer close(ack)
		select {
		case <-v.done:
			return
		default:
		}
		if v.bitmap == nil {
			v.bitmap = ui.NewBitmap(frame)
		} else {
			v.bitmap.Update(frame)
		}
		v.setFrameSize(frame.Bounds().Dx(), frame.Bounds().Dy())
		v.message = ""
	})
	select {
	case <-ack:
		return true
	case <-v.done:
		return false
	}
}

// runStream returns false on a stream failure, allowing the existing polling
// transport to take over. Hidden/minimized windows cancel the stream.
func (v *viewer) runStream(source display.FrameStreamer) bool {
	var frames [2]*image.RGBA
	index := 0
	for {
		select {
		case <-v.done:
			return true
		default:
		}
		if v.hidden.Load() || v.minimized.Load() {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-v.done:
				timer.Stop()
				return true
			case <-timer.C:
			}
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		v.streamMu.Lock()
		select {
		case <-v.done:
			v.streamMu.Unlock()
			cancel()
			return true
		default:
		}
		if v.hidden.Load() || v.minimized.Load() {
			v.streamMu.Unlock()
			cancel()
			continue
		}
		v.streamCancel = cancel
		v.streamMu.Unlock()
		var startTimedOut atomic.Bool
		startTimer := time.AfterFunc(5*time.Second, func() { startTimedOut.Store(true); cancel() })
		stream, err := source.OpenFrameStream(ctx)
		for err == nil {
			var frame *image.RGBA
			frame, err = stream.Next(frames[index])
			startTimer.Stop()
			if ctx.Err() != nil {
				break
			}
			if errors.Is(err, display.ErrDisplayInactive) {
				if !v.presentInactive() {
					cancel()
					return true
				}
				err = nil
				continue
			}
			if err != nil || ctx.Err() != nil {
				break
			}
			frames[index] = frame
			if !v.presentFrame(frame) {
				cancel()
				return true
			}
			index = 1 - index
		}
		v.streamMu.Lock()
		v.streamCancel = nil
		v.streamMu.Unlock()
		startTimer.Stop()
		interrupted := ctx.Err() != nil
		cancel()
		select {
		case <-v.done:
			return true
		default:
		}
		if startTimedOut.Load() {
			v.report(errors.New("画面流启动超时，已切换截图模式"))
			return false
		}
		if interrupted || v.hidden.Load() || v.minimized.Load() {
			continue
		}
		if err != nil {
			v.report(err)
		}
		return false
	}
}

func (v *viewer) presentInactive() bool {
	ack := make(chan struct{})
	v.win.Update(func() {
		defer close(ack)
		select {
		case <-v.done:
			return
		default:
		}
		v.setFrameSize(0, 0)
		v.bitmap = nil
		v.message = display.ErrDisplayInactive.Error()
	})
	select {
	case <-ack:
		return true
	case <-v.done:
		return false
	}
}

func (v *viewer) report(err error) {
	v.win.Update(func() {
		select {
		case <-v.done:
			return
		default:
		}
		v.message = err.Error()
	})
}

func (v *viewer) reportInput(err error) {
	v.win.Update(func() {
		select {
		case <-v.done:
			return
		default:
		}
		v.inputError = err.Error()
	})
}

// fitPoint rejects letterboxing on down and clamps a drag to the display.
func fitPoint(x, y, w, h float32, fw, fh int, clamp bool) (int, int, bool) {
	if w <= 0 || h <= 0 || fw <= 0 || fh <= 0 {
		return 0, 0, false
	}
	s := math.Min(float64(w)/float64(fw), float64(h)/float64(fh))
	dw, dh := float64(fw)*s, float64(fh)*s
	x1 := (float64(w) - dw) / 2
	y1 := (float64(h) - dh) / 2
	px, py := (float64(x)-x1)/s, (float64(y)-y1)/s
	if !clamp && (px < 0 || py < 0 || px >= float64(fw) || py >= float64(fh)) {
		return 0, 0, false
	}
	px = math.Max(0, math.Min(float64(fw-1), px))
	py = math.Max(0, math.Min(float64(fh-1), py))
	return int(px), int(py), true
}

func (v *viewer) enqueue(e pointerEvent) {
	select {
	case v.events <- e:
	default:
		// A dropped release could leave a virtual finger pressed. Closing the
		// session causes the worker to release it instead of guessing state.
		if e.kind != pointerMove {
			v.inputError = "输入队列繁忙，已关闭画面连接"
			v.stop()
		}
	}
}

func (v *viewer) releaseHostKeys() {
	if len(v.keys) == 0 {
		return
	}
	clear(v.keys)
	v.enqueue(pointerEvent{kind: releaseKeys})
}

func (v *viewer) releaseHostInput() {
	if v.held {
		v.held = false
		v.enqueue(pointerEvent{kind: pointerUp})
	}
	v.releaseHostKeys()
}

func (v *viewer) setFrameSize(width, height int) {
	if v.width > 0 && v.height > 0 && (v.width != width || v.height != height) {
		v.releaseHostInput()
		v.inputError = "画面尺寸已变化，已释放按住的输入"
	}
	v.width, v.height = width, height
}

func (v *viewer) setBinding(usage uint16, point *mappedPoint) error {
	if v.keymap == nil {
		return fmt.Errorf("键位配置不可用")
	}
	if point != nil && v.joystick != nil && joystickKey(usage) {
		return fmt.Errorf("WASD 已用于方向摇杆，请先删除摇杆")
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

func (v *viewer) setJoystick(stick *joystickConfig) error {
	if v.keymap == nil {
		return fmt.Errorf("键位配置不可用")
	}
	if stick != nil {
		for key := range v.bindings {
			if joystickKey(key) {
				return fmt.Errorf("WASD 已有单点键位，请先删除这些键位")
			}
		}
	}
	if err := v.keymap.saveWithJoystick(v.bindings, stick); err != nil {
		return err
	}
	v.joystick = v.keymap.joystick
	return nil
}

func (v *viewer) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Gap(4).Children(func() {
		message := v.message
		if v.inputError != "" {
			message = v.inputError
		}
		ui.Text(c, message).FontSize(12).TextColor(t.TextMuted)
		if v.mappingError != "" {
			ui.Text(c, v.mappingError).FontSize(12).TextColor(t.TextMuted)
		}
		if v.keymap != nil {
			text := fmt.Sprintf("游戏键位：%d 个", len(v.bindings))
			if v.joystick != nil {
				text += fmt.Sprintf(" · WASD 摇杆半径 %.0f%%", v.joystick.Radius*100)
			}
			if v.editing == 1 {
				text = "点击画面位置，再按需要绑定的键；按完成结束"
				if v.pendingPoint != nil {
					text = "已选择位置，请按需要绑定的键"
				}
			}
			if v.editing == 2 {
				text = "点击画面后按要删除的键；按完成结束"
			}
			if v.editing == 3 {
				text = "点击画面中游戏方向摇杆的中心"
			}
			ui.Text(c, text).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(6).Children(func() {
			for _, item := range []struct {
				label  string
				action display.Action
			}{{"返回", display.Back}, {"主页", display.Home}, {"最近任务", display.Overview}, {"音量−", display.VolumeDown}, {"音量+", display.VolumeUp}} {
				item := item
				if ui.Button(c.Key("nav-"+item.label), item.label).Clicked() {
					v.releaseHostInput()
					v.inputError = ""
					v.enqueue(pointerEvent{kind: navigate, action: item.action})
				}
			}
			if ui.Button(c.Key("fullscreen"), "全屏").Clicked() && v.win != nil {
				v.releaseHostInput()
				v.win.ToggleFullScreen()
			}
			if v.keymap != nil {
				label := "编辑键位"
				if v.editing != 0 {
					label = "完成键位"
				}
				if ui.Button(c.Key("edit-keymap"), label).Clicked() {
					v.releaseHostInput()
					v.pendingPoint = nil
					v.mappingError = ""
					if v.editing != 0 {
						v.editing = 0
					} else {
						v.editing = 1
					}
				}
				if v.editing != 0 && ui.Button(c.Key("remove-keymap"), "删除键位").Clicked() {
					v.pendingPoint = nil
					v.editing = 2
					v.screen.Focus(c)
				}
			}
		})
		ui.Row(c).Gap(6).Children(func() {
			selected := fmt.Sprintf("%d FPS", v.currentCaptureRate())
			if ui.Select(c.Key("capture-rate"), &selected, []string{"15 FPS", "30 FPS", "60 FPS", "120 FPS"}).Label("截图采样上限").Changed() {
				var rate int
				if _, err := fmt.Sscanf(selected, "%d FPS", &rate); err == nil {
					v.setCaptureRate(rate)
				}
			}
			if _, ok := v.source.(display.FrameStreamer); ok && os.Getenv("NEKO_EXPERIMENTAL_STREAM") == "1" {
				ui.Text(c, "实验画面流不受此设置限制；回退轮询后生效").FontSize(12).TextColor(t.TextMuted)
			}
		})
		if v.keymap != nil && v.editing != 0 {
			ui.Row(c).Gap(6).Children(func() {
				selected := v.activeProfile()
				if ui.Select(c.Key("keymap-profile"), &selected, v.profileChoices()).Label("键位方案").Changed() && selected != v.activeProfile() {
					if err := v.switchKeymapProfile(selected); err != nil {
						v.mappingError = err.Error()
					} else {
						v.mappingError = ""
					}
				}
				ui.TextInput(c.Key("keymap-profile-name"), &v.profileName).Placeholder("新方案名称").Label("新方案名称").Width(150)
				if ui.Button(c.Key("create-keymap-profile"), "保存为新方案").Clicked() {
					if err := v.createKeymapProfile(v.profileName); err != nil {
						v.mappingError = err.Error()
					} else {
						v.mappingError = ""
						v.profileName = ""
					}
				}
			})
			ui.Row(c).Gap(6).Children(func() {
				if ui.Button(c.Key("set-joystick"), "设置 WASD 摇杆").Clicked() {
					v.editing = 3
					v.pendingPoint = nil
					v.mappingError = ""
				}
				if v.joystick != nil {
					if ui.Button(c.Key("remove-joystick"), "删除摇杆").Clicked() {
						if err := v.setJoystick(nil); err != nil {
							v.mappingError = err.Error()
						} else {
							v.mappingError = ""
							v.editing = 1
						}
					}
					for _, option := range []struct {
						label string
						delta float64
					}{{"缩小摇杆", -0.02}, {"放大摇杆", 0.02}} {
						option := option
						if ui.Button(c.Key(option.label), option.label).Clicked() {
							stick := *v.joystick
							stick.Radius = math.Max(0.04, math.Min(0.25, stick.Radius+option.delta))
							if err := v.setJoystick(&stick); err != nil {
								v.mappingError = err.Error()
							} else {
								v.mappingError = ""
							}
						}
					}
				}
			})
		}
		canvas := ui.Box(c.Key("screen")).WidthPercent(100).Grow(1).Focusable().AutoFocus().Bind(&v.screen)
		canvas.Draw(func(p *ui.Painter, r ui.Rect) {
			if v.bitmap != nil {
				p.Image(v.bitmap, r, ui.Contain)
			}
			if v.editing != 0 {
				drawKeymapOverlay(p, r, v.width, v.height, v.bindings, v.joystick, v.pendingPoint)
			}
		})
		canvas.HandleInput(func(ev ui.InputEvent) bool {
			bounds := v.screen.Bounds(c)
			switch ev.Kind {
			case ui.InputKeyDown:
				usage := uint16(ev.Code)
				if !display.ValidKeyboardUsage(usage) {
					return false
				}
				if v.editing != 0 {
					if v.editing == 3 {
						return true
					}
					if v.editing == 1 && v.pendingPoint == nil {
						return true
					}
					point := v.pendingPoint
					if v.editing == 2 {
						point = nil
					}
					if err := v.setBinding(usage, point); err != nil {
						v.mappingError = err.Error()
					} else {
						v.mappingError = ""
						v.pendingPoint = nil
					}
					return true
				}
				if v.keys == nil {
					v.keys = make(map[uint16]bool)
				}
				v.inputError = ""
				if !v.keys[usage] {
					v.keys[usage] = true
					if v.joystick != nil && joystickKey(usage) {
						if v.width <= 0 || v.height <= 0 {
							delete(v.keys, usage)
							v.inputError = "画面尚未就绪，无法使用方向摇杆"
							return true
						}
						p := v.joystick
						v.enqueue(pointerEvent{kind: stickDown, usage: usage, x: int(p.Center.X*float64(v.width-1) + 0.5), y: int(p.Center.Y*float64(v.height-1) + 0.5), radius: max(1, int(p.Radius*float64(min(v.width-1, v.height-1))+0.5)), maxX: v.width - 1, maxY: v.height - 1})
					} else if p, ok := v.bindings[usage]; ok {
						if v.width <= 0 || v.height <= 0 {
							delete(v.keys, usage)
							v.inputError = "画面尚未就绪，无法使用游戏键位"
							return true
						}
						v.enqueue(pointerEvent{kind: mappedDown, usage: usage, x: int(p.X*float64(v.width-1) + 0.5), y: int(p.Y*float64(v.height-1) + 0.5)})
					} else {
						v.enqueue(pointerEvent{kind: keyDown, usage: usage})
					}
				}
				return true
			case ui.InputKeyUp:
				usage := uint16(ev.Code)
				if v.editing != 0 {
					return true
				}
				if !v.keys[usage] {
					return false
				}
				delete(v.keys, usage)
				if v.joystick != nil && joystickKey(usage) {
					v.enqueue(pointerEvent{kind: stickUp, usage: usage})
				} else if _, mapped := v.bindings[usage]; mapped {
					v.enqueue(pointerEvent{kind: mappedUp, usage: usage})
				} else {
					v.enqueue(pointerEvent{kind: keyUp, usage: usage})
				}
				return true
			case ui.InputPointerDown:
				if ev.Button != 0 {
					return false
				}
				x, y, ok := fitPoint(ev.X, ev.Y, bounds.W, bounds.H, v.width, v.height, false)
				if !ok {
					return false
				}
				if v.editing != 0 {
					if v.editing == 1 {
						v.pendingPoint = &mappedPoint{X: float64(x) / float64(max(1, v.width-1)), Y: float64(y) / float64(max(1, v.height-1))}
					}
					if v.editing == 3 {
						radius := 0.12
						if v.joystick != nil {
							radius = v.joystick.Radius
						}
						stick := &joystickConfig{Center: mappedPoint{X: float64(x) / float64(max(1, v.width-1)), Y: float64(y) / float64(max(1, v.height-1))}, Radius: radius}
						if err := v.setJoystick(stick); err != nil {
							v.mappingError = err.Error()
						} else {
							v.mappingError = ""
							v.editing = 1
						}
					}
					v.screen.Focus(c)
					return true
				}
				v.inputError = ""
				v.held = true
				v.enqueue(pointerEvent{kind: pointerDown, x: x, y: y})
				return true
			case ui.InputPointerMove:
				if !v.held {
					return false
				}
				x, y, ok := fitPoint(ev.X, ev.Y, bounds.W, bounds.H, v.width, v.height, true)
				if ok {
					v.enqueue(pointerEvent{kind: pointerMove, x: x, y: y})
				}
				return true
			case ui.InputPointerUp:
				if !v.held {
					return false
				}
				v.held = false
				v.enqueue(pointerEvent{kind: pointerUp})
				return true
			}
			return false
		})
	})
}
