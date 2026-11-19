package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/previewinput"
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
	previewError  string
	preview       *vulkanPreview
	held          bool
	previewHeld   bool
	previewKeys   map[uint16]bool
	keys          map[uint16]bool
	screen        ui.Handle
	events        chan pointerEvent
	done          chan struct{}
	once          sync.Once
	streamMu      sync.Mutex
	streamCancel  context.CancelFunc
	captureRate   atomic.Int32
	rateChanged   chan struct{}
	visibilityCh  chan struct{}
	visEpoch      atomic.Uint64
	clearEpoch    atomic.Uint64
	previewEpoch  uint64 // owned by the capture goroutine
	previewShown  atomic.Bool
	previewReady  bool // UI thread only; set after the preview presents a frame
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
	fitMode       display.FitMode
	audioSerial   string
	instanceName  string
}

func openViewer(name string, source display.Session, bindingsPath, audioSerial string, onClosed func()) *viewer {
	v := &viewer{source: source, events: make(chan pointerEvent, 64), done: make(chan struct{}), rateChanged: make(chan struct{}, 1), visibilityCh: make(chan struct{}, 1), message: "正在读取画面…", fitMode: display.FitCover, audioSerial: audioSerial, instanceName: name}
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
	geometry, hasGeometry := loadViewerGeometry(name)
	width, height := 1000, 650
	if hasGeometry {
		width, height = geometry.Width, geometry.Height
	} else if strings.EqualFold(name, avd.DefaultName) {
		geometry, hasGeometry = defaultViewerGeometry(), true
		width, height = geometry.Width, geometry.Height
	}
	v.win = mygo.NewWindow(mygo.WindowOptions{Title: name + " - NekoSimulator233", Width: width, Height: height, Content: ui.View(v.view)})
	if hasGeometry {
		v.win.SetBounds(mygo.Rectangle{X: geometry.Left, Y: geometry.Top, Width: geometry.Width, Height: geometry.Height})
		if geometry.Maximized {
			v.win.Maximize()
		}
	}
	v.win.OnClose(func(*mygo.CloseEvent) {
		// Capture bounds before teardown: Bounds() can be empty in OnClosed.
		bounds := v.win.Bounds()
		if bounds.Width >= 320 && bounds.Height >= 240 {
			_ = saveViewerGeometry(name, viewerGeometry{
				Left: int(bounds.X), Top: int(bounds.Y), Width: int(bounds.Width), Height: int(bounds.Height),
				Maximized: v.win.IsMaximized(),
			})
		}
	})
	v.win.OnClosed(func() {
		v.stop()
		if onClosed != nil {
			onClosed()
		}
	})
	v.win.OnBlur(v.releaseHostInput)
	v.win.OnHide(func() { v.setMainVisibility(&v.hidden, true) })
	v.win.OnShow(func() { v.setMainVisibility(&v.hidden, false) })
	v.win.OnMinimize(func() { v.setMainVisibility(&v.minimized, true) })
	v.win.OnRestore(func() { v.setMainVisibility(&v.minimized, false) })
	go v.run()
	return v
}

func (v *viewer) stop() { v.once.Do(func() { close(v.done); v.cancelStream() }) }

func (v *viewer) captureVisible() bool {
	return (!v.hidden.Load() && !v.minimized.Load()) || v.previewShown.Load()
}

// The main window and the preview can independently keep capture active.
// These transitions run on the UI thread.
func (v *viewer) setMainVisibility(flag *atomic.Bool, hidden bool) {
	wasVisible := v.captureVisible()
	if flag.Swap(hidden) == hidden {
		return
	}
	if hidden {
		v.releaseHostInput()
	}
	if wasVisible && !v.captureVisible() {
		v.suspendPresentation()
	} else {
		v.notifyVisibility()
	}
	v.applyAudioMutePolicy()
}

// applyAudioMutePolicy mirrors MuMu's volume_setting.window_minimized_mute:
// mute guest media audio when no viewer surface is visible, and unmute when
// one becomes visible again. It runs off the UI thread.
func (v *viewer) applyAudioMutePolicy() {
	if v.audioSerial == "" || v.instanceName == "" {
		return
	}
	mute := !v.captureVisible()
	serial, name := v.audioSerial, v.instanceName
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		manager, err := avd.DiscoverManager()
		if err != nil {
			return
		}
		manager, err = manager.WithName(name)
		if err != nil {
			return
		}
		_ = manager.SetMusicMute(ctx, serial, mute)
	}()
}

func (v *viewer) setPreviewVisibility(shown bool) {
	wasVisible := v.captureVisible()
	if v.previewShown.Swap(shown) == shown {
		return
	}
	v.previewReady = false
	if !shown {
		v.applyPreviewInput(previewinput.Event{Kind: "release"})
		v.clearEpoch.Add(1)
	}
	if wasVisible && !v.captureVisible() {
		v.suspendPresentation()
	} else {
		v.notifyVisibility()
	}
}

// Called on the UI thread. Incrementing the epoch also invalidates a capture
// that was already in flight when the window was hidden.
func (v *viewer) suspendPresentation() {
	v.visEpoch.Add(1)
	v.clearEpoch.Add(1)
	v.previewReady = false
	v.setFrameSize(0, 0)
	v.bitmap = nil
	v.message = "画面采集已暂停"
	v.releaseHostInput()
	v.cancelStream()
	v.notifyVisibility()
}

func (v *viewer) notifyVisibility() {
	select {
	case v.visibilityCh <- struct{}{}:
	default:
	}
}

func (v *viewer) frameCurrent(epoch uint64) bool {
	return epoch == v.visEpoch.Load() && v.captureVisible()
}

// Only the capture goroutine writes to the preview pipe.
func (v *viewer) syncPreviewVisibility() {
	epoch := v.clearEpoch.Load()
	if epoch == v.previewEpoch {
		return
	}
	v.previewEpoch = epoch
	if v.preview != nil {
		if err := v.preview.inactive(); err != nil {
			v.preview.close()
			v.preview = nil
			v.reportPreview(err)
		}
	}
}

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
	if os.Getenv("NEKO_EXPERIMENTAL_VULKAN_VIEWER") == "1" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-v.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		preview, err := startVulkanPreview(ctx, func(event previewinput.Event) { v.deliverPreviewInput(ctx, event) })
		if err != nil {
			v.reportPreview(err)
		} else {
			v.preview = preview
			defer preview.close()
		}
	}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		runViewerInput(v.done, v.events, newViewerInput(v.source), v.reportInput)
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
	v.runPolling()
}

type captureRequest struct {
	buffer *image.RGBA
	epoch  uint64
}

type captureResult struct {
	frame *image.RGBA
	epoch uint64
	err   error
}

// Keep pipe control responsive even when a screenshot request is in flight.
// A single worker preserves buffer ownership and prevents overlapping captures.
func (v *viewer) runPolling() {
	requests := make(chan captureRequest)
	results := make(chan captureResult)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-v.done:
				return
			case req := <-requests:
				frame, err := v.source.Capture(req.buffer)
				select {
				case results <- captureResult{frame: frame, epoch: req.epoch, err: err}:
				case <-v.done:
					return
				}
			}
		}
	}()
	defer func() { <-workerDone }()
	ticker := time.NewTicker(time.Second / time.Duration(v.currentCaptureRate()))
	defer ticker.Stop()
	var frames [2]*image.RGBA
	frameIndex := 0
	capturing := false
	for {
		select {
		case <-v.done:
			return
		case <-v.rateChanged:
			ticker.Reset(time.Second / time.Duration(v.currentCaptureRate()))
		case <-v.visibilityCh:
			v.syncPreviewVisibility()
		case <-ticker.C:
			v.syncPreviewVisibility()
			if capturing || !v.captureVisible() {
				continue
			}
			select {
			case requests <- captureRequest{buffer: frames[frameIndex], epoch: v.visEpoch.Load()}:
				capturing = true
			default:
			}
		case result := <-results:
			capturing = false
			if !v.frameCurrent(result.epoch) {
				continue
			}
			if result.err != nil {
				v.report(result.err)
				continue
			}
			frames[frameIndex] = result.frame
			if !v.presentFrame(result.frame, result.epoch) {
				return
			}
			frameIndex = 1 - frameIndex
		}
	}
}

// presentFrame waits for the UI to accept the frame before reusing buffers.
func (v *viewer) presentFrame(frame *image.RGBA, epoch uint64) bool {
	v.syncPreviewVisibility()
	ack := make(chan struct{})
	accepted := false
	v.win.Update(func() {
		defer close(ack)
		select {
		case <-v.done:
			return
		default:
		}
		if !v.frameCurrent(epoch) {
			return
		}
		if !v.hidden.Load() && !v.minimized.Load() {
			if v.bitmap == nil {
				v.bitmap = ui.NewBitmap(frame)
			} else {
				v.bitmap.Update(frame)
			}
		} else {
			v.bitmap = nil
		}
		v.setFrameSize(frame.Bounds().Dx(), frame.Bounds().Dy())
		v.message = ""
		accepted = true
	})
	select {
	case <-ack:
		if accepted && v.frameCurrent(epoch) && v.previewShown.Load() && v.preview != nil {
			if err := v.preview.send(frame); err != nil {
				v.preview.close()
				v.preview = nil
				v.reportPreview(err)
			}
		}
		return true
	case <-v.done:
		return false
	}
}

func (v *viewer) reportPreview(err error) {
	v.win.Update(func() {
		select {
		case <-v.done:
			return
		default:
		}
		v.previewError = "实验 Vulkan 画面不可用：" + err.Error()
	})
}

// runStream returns false on a stream failure, allowing the existing polling
// transport to take over. Capture pauses when both windows are hidden.
func (v *viewer) runStream(source display.FrameStreamer) bool {
	for {
		v.syncPreviewVisibility()
		select {
		case <-v.done:
			return true
		default:
		}
		if !v.captureVisible() {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-v.done:
				timer.Stop()
				return true
			case <-v.visibilityCh:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		epoch := v.visEpoch.Load()
		ctx, cancel := context.WithCancel(context.Background())
		v.streamMu.Lock()
		select {
		case <-v.done:
			v.streamMu.Unlock()
			cancel()
			return true
		default:
		}
		if !v.frameCurrent(epoch) {
			v.streamMu.Unlock()
			cancel()
			continue
		}
		v.streamCancel = cancel
		v.streamMu.Unlock()
		var startTimedOut atomic.Bool
		startTimer := time.AfterFunc(5*time.Second, func() { startTimedOut.Store(true); cancel() })
		stream, err := source.OpenFrameStream(ctx)
		var pumpDone <-chan struct{}
		var ready <-chan pumpedFrame
		var frames <-chan pumpedFrame
		var available chan<- *image.RGBA
		var ticker *time.Ticker
		if err == nil {
			frames, available, pumpDone = pumpFrames(ctx, stream)
			ticker = time.NewTicker(time.Second / time.Duration(v.currentCaptureRate()))
		}
		for err == nil {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-v.done:
				cancel()
				err = context.Canceled
			case <-v.rateChanged:
				ticker.Reset(time.Second / time.Duration(v.currentCaptureRate()))
			case <-v.visibilityCh:
				v.syncPreviewVisibility()
			case <-ticker.C:
				ready = frames
			case next, ok := <-ready:
				ready = nil
				startTimer.Stop()
				if !ok {
					err = errors.New("画面流已结束")
					break
				}
				if errors.Is(next.err, display.ErrDisplayInactive) {
					if !v.presentInactive() {
						cancel()
						err = context.Canceled
					}
				} else if next.err != nil {
					err = next.err
				} else {
					presented := v.presentFrame(next.frame, epoch)
					if !presented {
						cancel()
						err = context.Canceled
					} else {
						select {
						case available <- next.frame:
						case <-ctx.Done():
						}
					}
				}
			}
		}
		if ticker != nil {
			ticker.Stop()
		}
		v.streamMu.Lock()
		v.streamCancel = nil
		v.streamMu.Unlock()
		startTimer.Stop()
		interrupted := ctx.Err() != nil
		cancel()
		if pumpDone != nil {
			<-pumpDone
		}
		select {
		case <-v.done:
			return true
		default:
		}
		if startTimedOut.Load() {
			v.report(errors.New("画面流启动超时，已切换截图模式"))
			return false
		}
		if interrupted || !v.captureVisible() {
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
		v.previewReady = false
		v.message = display.ErrDisplayInactive.Error()
	})
	select {
	case <-ack:
		if v.preview != nil {
			if err := v.preview.inactive(); err != nil {
				v.preview.close()
				v.preview = nil
				v.reportPreview(err)
			}
		}
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

// saveScreenshot writes the current guest frame to the user's Pictures folder,
// matching MuMu's one-click screenshot action. Capture runs off the UI thread.
func (v *viewer) saveScreenshot() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		frame, err := v.source.Capture(nil)
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			v.reportInput(err)
			return
		}
		dir := filepath.Join(os.Getenv("USERPROFILE"), "Pictures", "NekoSimulator233")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			v.reportInput(err)
			return
		}
		name := filepath.Join(dir, fmt.Sprintf("neko-%s.png", time.Now().Format("20060102-150405")))
		file, err := os.Create(name)
		if err != nil {
			v.reportInput(err)
			return
		}
		err = png.Encode(file, frame)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			v.reportInput(err)
			return
		}
		v.win.Update(func() { v.message = "截图已保存：" + name })
	}()
}

// painterFit keeps MyGo's drawing and our input mapping on the same mode.
func painterFit(mode display.FitMode) ui.Fit {
	switch mode {
	case display.FitCover:
		return ui.Cover
	case display.FitStretch:
		return ui.FillBox
	default:
		return ui.Contain
	}
}

// fitPoint maps viewer pixels to guest pixels using the active scaling mode.
func fitPoint(mode display.FitMode, x, y, w, h float32, fw, fh int, clamp bool) (int, int, bool) {
	return display.MapPoint(mode, x, y, w, h, fw, fh, clamp)
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
	clear(v.previewKeys)
	if len(v.keys) == 0 {
		return
	}
	clear(v.keys)
	v.enqueue(pointerEvent{kind: releaseKeys})
}

func (v *viewer) releaseHostInput() {
	v.previewHeld = false
	if v.held {
		v.held = false
		v.enqueue(pointerEvent{kind: pointerUp})
	}
	v.releaseHostKeys()
}

func (v *viewer) deliverPreviewInput(ctx context.Context, event previewinput.Event) {
	ack := make(chan struct{})
	v.win.Update(func() {
		defer close(ack)
		select {
		case <-v.done:
			return
		default:
		}
		v.applyPreviewInput(event)
	})
	select {
	case <-ack:
	case <-ctx.Done():
	case <-v.done:
	}
}

// applyPreviewInput runs on the UI thread, sharing held state with the main
// viewer so focus changes, resizing and queue overflow release touches safely.
func (v *viewer) applyPreviewInput(event previewinput.Event) {
	if !event.Valid() {
		return
	}
	if event.Kind == "visible" || event.Kind == "hidden" {
		v.setPreviewVisibility(event.Kind == "visible")
		return
	}
	if event.Kind == "ready" {
		if v.previewShown.Load() {
			v.previewReady = true
		}
		return
	}
	release := func() {
		if v.previewHeld {
			v.previewHeld = false
			if v.held {
				v.held = false
				v.enqueue(pointerEvent{kind: pointerUp})
			}
		}
	}
	if event.Kind == "release" {
		for usage := range v.previewKeys {
			v.releaseHostKey(usage)
			delete(v.previewKeys, usage)
		}
		release()
		return
	}
	if event.Kind == "up" {
		release()
		return
	}
	if !v.previewShown.Load() || !v.previewReady {
		return
	}
	if event.Kind == "key_down" || event.Kind == "key_up" {
		if event.Kind == "key_up" {
			if v.previewKeys[event.Usage] {
				v.releaseHostKey(event.Usage)
				delete(v.previewKeys, event.Usage)
			}
			return
		}
		if v.editing != 0 || v.width <= 0 || v.height <= 0 {
			return
		}
		if event.Kind == "key_down" {
			if v.previewKeys == nil {
				v.previewKeys = make(map[uint16]bool)
			}
			if v.previewKeys[event.Usage] {
				return
			}
			if v.keys[event.Usage] {
				return
			}
			if v.handleHostKey(event.Usage, true) && v.keys[event.Usage] {
				v.previewKeys[event.Usage] = true
			}
		}
		return
	}
	if v.editing != 0 || event.FrameWidth != v.width || event.FrameHeight != v.height {
		release()
		return
	}
	if event.Kind == "down" {
		x, y, ok := event.Point(false)
		if !ok {
			return
		}
		if v.held {
			v.held = false
			v.enqueue(pointerEvent{kind: pointerUp})
		}
		v.inputError = ""
		v.held, v.previewHeld = true, true
		v.enqueue(pointerEvent{kind: pointerDown, x: x, y: y})
	} else if event.Kind == "move" && v.previewHeld && v.held {
		if x, y, ok := event.Point(true); ok {
			v.enqueue(pointerEvent{kind: pointerMove, x: x, y: y})
		}
	}
}

// handleHostKey is shared by the main and experimental preview windows.
// It runs on the UI thread and keeps physical keys, mapped taps and joystick
// bindings in the same ordered input queue.
func (v *viewer) handleHostKey(usage uint16, down bool) bool {
	if !display.ValidKeyboardUsage(usage) {
		return false
	}
	if down && v.editing != 0 {
		if v.editing == 3 || v.editing == 1 && v.pendingPoint == nil {
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
	if !down && v.editing != 0 {
		return true
	}
	if !down {
		return v.releaseHostKey(usage)
	}
	if v.keys == nil {
		v.keys = make(map[uint16]bool)
	}
	v.inputError = ""
	if v.keys[usage] {
		return true
	}
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
	return true
}

func (v *viewer) releaseHostKey(usage uint16) bool {
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
		if v.previewError != "" {
			message = v.previewError
		}
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
			if ui.Button(c.Key("save-screenshot"), "截图").Clicked() {
				v.saveScreenshot()
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
			fitSelected := v.fitMode.String()
			if ui.Select(c.Key("fit-mode"), &fitSelected, []string{"适应", "铺满", "拉伸"}).Label("画面缩放").Changed() {
				switch fitSelected {
				case "铺满":
					v.fitMode = display.FitCover
				case "拉伸":
					v.fitMode = display.FitStretch
				default:
					v.fitMode = display.FitContain
				}
			}
			if _, ok := v.source.(display.FrameStreamer); ok && os.Getenv("NEKO_EXPERIMENTAL_STREAM") == "1" {
				ui.Text(c, "实验画面流按此上限呈现；上游仍可能产生更多画面").FontSize(12).TextColor(t.TextMuted)
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
				p.Image(v.bitmap, r, painterFit(v.fitMode))
			}
			if v.editing != 0 {
				drawKeymapOverlay(p, r, v.width, v.height, v.bindings, v.joystick, v.pendingPoint)
			}
		})
		canvas.HandleInput(func(ev ui.InputEvent) bool {
			bounds := v.screen.Bounds(c)
			switch ev.Kind {
			case ui.InputKeyDown:
				delete(v.previewKeys, uint16(ev.Code))
				return v.handleHostKey(uint16(ev.Code), true)
			case ui.InputKeyUp:
				delete(v.previewKeys, uint16(ev.Code))
				return v.handleHostKey(uint16(ev.Code), false)
			case ui.InputPointerDown:
				if ev.Button != 0 {
					return false
				}
				if v.previewHeld {
					v.previewHeld = false
					if v.held {
						v.held = false
						v.enqueue(pointerEvent{kind: pointerUp})
					}
				}
				x, y, ok := fitPoint(v.fitMode, ev.X, ev.Y, bounds.W, bounds.H, v.width, v.height, false)
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
				x, y, ok := fitPoint(v.fitMode, ev.X, ev.Y, bounds.W, bounds.H, v.width, v.height, true)
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
