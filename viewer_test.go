package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/previewinput"
)

func TestPreviewMouseMappingAndRelease(t *testing.T) {
	v := &viewer{width: 4, height: 2, events: make(chan pointerEvent, 8), done: make(chan struct{})}
	e := previewinput.Event{Kind: "down", Width: 4, Height: 4, FrameWidth: 4, FrameHeight: 2, X: 2, Y: 0}
	v.applyPreviewInput(e)
	if len(v.events) != 0 {
		t.Fatal("bar accepted")
	}
	e.Y = 1
	v.applyPreviewInput(e)
	down := <-v.events
	if down.kind != pointerDown || down.x != 2 || down.y != 0 || !v.previewHeld {
		t.Fatalf("down: %+v", down)
	}
	e.Kind, e.X, e.Y = "move", -10, 10
	v.applyPreviewInput(e)
	move := <-v.events
	if move.kind != pointerMove || move.x != 0 || move.y != 1 {
		t.Fatalf("move: %+v", move)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "release"})
	if up := <-v.events; up.kind != pointerUp || v.held || v.previewHeld {
		t.Fatal("focus loss did not release")
	}
	e.Kind, e.X, e.Y = "down", 2, 1
	v.applyPreviewInput(e)
	<-v.events
	e.Kind, e.FrameWidth = "move", 8
	v.applyPreviewInput(e)
	if up := <-v.events; up.kind != pointerUp || v.held {
		t.Fatal("changed frame did not release")
	}
}

func TestPreviewKeysShareMappingsAndReleaseOnlyTheirOwner(t *testing.T) {
	v := &viewer{width: 100, height: 200, events: make(chan pointerEvent, 16), done: make(chan struct{}), bindings: map[uint16]mappedPoint{0x04: {X: 0.5, Y: 0.5}}}
	v.handleHostKey(0x06, true) // main window owns C
	if e := <-v.events; e.kind != keyDown || e.usage != 0x06 {
		t.Fatalf("main down: %+v", e)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if e := <-v.events; e.kind != mappedDown || e.usage != 0x04 || e.x != 50 || e.y != 100 {
		t.Fatalf("mapped down: %+v", e)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if len(v.events) != 0 {
		t.Fatal("repeat sent twice")
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x05})
	if e := <-v.events; e.kind != keyDown || e.usage != 0x05 {
		t.Fatalf("preview down: %+v", e)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "release"})
	got := map[int]bool{}
	for range 2 {
		e := <-v.events
		got[e.kind] = true
	}
	if !got[mappedUp] || !got[keyUp] || !v.keys[0x06] || len(v.previewKeys) != 0 {
		t.Fatalf("release mismatch: %+v", v.keys)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_up", Usage: 0x06})
	if len(v.events) != 0 || !v.keys[0x06] {
		t.Fatal("preview released main-window key")
	}
}

func TestPreviewJoystickAndResizeRelease(t *testing.T) {
	v := &viewer{width: 100, height: 100, events: make(chan pointerEvent, 8), done: make(chan struct{}), joystick: &joystickConfig{Center: mappedPoint{X: 0.5, Y: 0.5}, Radius: 0.1}}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x1a})
	if e := <-v.events; e.kind != stickDown || e.usage != 0x1a {
		t.Fatalf("stick down: %+v", e)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "release"})
	if e := <-v.events; e.kind != stickUp || e.usage != 0x1a {
		t.Fatalf("stick up: %+v", e)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x1a})
	<-v.events
	v.setFrameSize(200, 100)
	if e := <-v.events; e.kind != releaseKeys || len(v.previewKeys) != 0 || len(v.keys) != 0 {
		t.Fatalf("resize release: %+v", e)
	}
	v.applyPreviewInput(previewinput.Event{Kind: "release"})
	if len(v.events) != 0 {
		t.Fatal("duplicate release")
	}
}

type slowCaptureSession struct {
	capturing chan struct{}
	unblock   chan struct{}
	input     chan struct{}
	released  chan struct{}
	closed    chan struct{}
}

func (s *slowCaptureSession) Capture(*image.RGBA) (*image.RGBA, error) {
	close(s.capturing)
	<-s.unblock
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}
func (s *slowCaptureSession) TouchDown(int, int) error { close(s.input); return nil }
func (s *slowCaptureSession) TouchUp() error           { close(s.released); return nil }
func (s *slowCaptureSession) Close() error             { close(s.closed); return nil }

func TestViewerProcessesAndReleasesInputDuringSlowCapture(t *testing.T) {
	s := &slowCaptureSession{capturing: make(chan struct{}), unblock: make(chan struct{}), input: make(chan struct{}), released: make(chan struct{}), closed: make(chan struct{})}
	v := &viewer{source: s, events: make(chan pointerEvent, 1), done: make(chan struct{})}
	go v.run()
	wait := func(ch <-chan struct{}, message string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal(message)
		}
	}
	defer func() {
		v.stop()
		close(s.unblock)
		wait(s.closed, "session did not close after capture finished")
	}()
	wait(s.capturing, "capture did not start")
	v.events <- pointerEvent{kind: pointerDown, x: 1, y: 1}
	wait(s.input, "input was blocked by the pending capture")
	v.stop()
	wait(s.released, "input was not released while capture was pending")
	select {
	case <-s.closed:
		t.Fatal("session closed before capture finished")
	default:
	}
}

func TestJoystickDirectionsAndRelease(t *testing.T) {
	s := &fakeMultiSession{}
	in := newViewerInput(s)
	base := pointerEvent{kind: stickDown, usage: 0x1a, x: 300, y: 500, radius: 100, maxX: 719, maxY: 1279}
	if err := in.handle(base); err != nil {
		t.Fatal(err)
	}
	base.usage = 0x07
	if err := in.handle(base); err != nil {
		t.Fatal(err)
	}
	if err := in.handle(pointerEvent{kind: stickDown, usage: 0x16}); err != nil {
		t.Fatal(err)
	}
	if err := in.handle(pointerEvent{kind: stickUp, usage: 0x1a}); err != nil {
		t.Fatal(err)
	}
	if err := in.handle(pointerEvent{kind: stickUp, usage: 0x07}); err != nil {
		t.Fatal(err)
	}
	if err := in.handle(pointerEvent{kind: stickUp, usage: 0x16}); err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{300, 500}, {300, 400}, {371, 429}, {400, 500}, {371, 571}, {300, 600}}
	if len(s.frames) != len(want)+1 {
		t.Fatalf("got %d frames", len(s.frames))
	}
	for i, p := range want {
		if len(s.frames[i]) != 1 || s.frames[i][0].X != p[0] || s.frames[i][0].Y != p[1] {
			t.Fatalf("frame %d = %+v, want %v", i, s.frames[i], p)
		}
	}
	if len(s.frames[len(want)]) != 0 {
		t.Fatal("last joystick key did not release touch")
	}
}

func TestJoystickEditingPersistenceAndConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keymap.json")
	legacy := `{"version":1,"keys":{"8":{"X":0.4,"Y":0.6}}}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store := &keymapStore{path: path}
	bindings, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	v := &viewer{keymap: store, bindings: bindings, width: 100, height: 100, events: make(chan pointerEvent, 16), done: make(chan struct{})}
	tester := ui.NewTester(v.view, 800, 500)
	if err = tester.Click("编辑键位"); err != nil {
		t.Fatal(err)
	}
	if err = tester.Click("设置 WASD 摇杆"); err != nil {
		t.Fatal(err)
	}
	tester.ClickAt(400, 310)
	if v.joystick == nil || v.editing != 1 {
		t.Fatal("joystick position was not saved")
	}
	if len(v.bindings) != 1 {
		t.Fatal("existing key mapping was lost")
	}
	if err := v.setBinding(uint16(ui.CodeKeyW), &mappedPoint{X: 0.5, Y: 0.5}); err == nil {
		t.Fatal("overlapping WASD binding accepted")
	}
	other := &keymapStore{path: path}
	loaded, err := other.read()
	if err != nil || len(loaded) != 1 || other.joystick == nil || *other.joystick != *v.joystick {
		t.Fatalf("configuration not preserved: %v %+v", err, other.joystick)
	}
	if err = tester.Click("完成键位"); err != nil {
		t.Fatal(err)
	}
	tester.ClickAt(400, 310)
	for len(v.events) != 0 {
		<-v.events
	}
	tester.CodeDown(0, ui.CodeKeyW)
	tester.CodeDown(0, ui.CodeKeyD)
	tester.CodeUp(0, ui.CodeKeyW)
	v.releaseHostKeys()
	if len(v.events) != 4 {
		t.Fatalf("got %d joystick events", len(v.events))
	}
	if (<-v.events).kind != stickDown || (<-v.events).kind != stickDown || (<-v.events).kind != stickUp || (<-v.events).kind != releaseKeys {
		t.Fatal("unexpected joystick input or blur release")
	}
	if err := v.setJoystick(nil); err != nil {
		t.Fatal(err)
	}
	if err := v.setBinding(uint16(ui.CodeKeyW), &mappedPoint{X: 0.5, Y: 0.5}); err != nil {
		t.Fatal(err)
	}
	if err := v.setJoystick(&joystickConfig{Center: mappedPoint{X: 0.5, Y: 0.5}, Radius: 0.12}); err == nil {
		t.Fatal("joystick overwrote existing WASD mapping")
	}
}

func TestKeymapOverlayVisibleWhileEditing(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 220, G: 20, B: 20, A: 255})
		}
	}
	v := &viewer{bitmap: ui.NewBitmap(frame), width: 100, height: 100, editing: 1,
		keymap: &keymapStore{path: filepath.Join(t.TempDir(), "keymap.json")}, bindings: map[uint16]mappedPoint{4: {X: 0.5, Y: 0.5}},
		joystick: &joystickConfig{Center: mappedPoint{X: 0.3, Y: 0.5}, Radius: 0.12}, events: make(chan pointerEvent, 8), done: make(chan struct{})}
	tester := ui.NewTester(v.view, 700, 500)
	img := tester.Image()
	if path := os.Getenv("NEKO_TEST_KEYMAP_PREVIEW"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(f, img); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	cyan := 0
	for y := 100; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r < 30000 && g > 44000 && b > 44000 {
				cyan++
			}
		}
	}
	if cyan < 10 {
		t.Fatal("editing overlay was not rendered over the synthetic frame")
	}
}

type fakeMultiSession struct{ frames [][]display.TouchPoint }

func (*fakeMultiSession) Capture(dst *image.RGBA) (*image.RGBA, error) { return dst, nil }
func (*fakeMultiSession) TouchDown(int, int) error                     { return nil }
func (*fakeMultiSession) TouchUp() error                               { return nil }
func (*fakeMultiSession) Close() error                                 { return nil }
func (s *fakeMultiSession) SetTouches(p []display.TouchPoint) error {
	s.frames = append(s.frames, append([]display.TouchPoint(nil), p...))
	return nil
}

func TestMappedKeysAndMouseRemainIndependent(t *testing.T) {
	source := &fakeMultiSession{}
	input := newViewerInput(source)
	for _, e := range []pointerEvent{
		{kind: mappedDown, usage: 4, x: 100, y: 200},
		{kind: mappedDown, usage: 5, x: 300, y: 400},
		{kind: pointerDown, x: 500, y: 600},
		{kind: mappedUp, usage: 4},
		{kind: releaseKeys},
		{kind: pointerUp},
	} {
		if err := input.handle(e); err != nil {
			t.Fatal(err)
		}
	}
	want := []int{1, 2, 3, 2, 1, 0}
	if len(source.frames) != len(want) {
		t.Fatalf("got %d touch updates", len(source.frames))
	}
	for i, n := range want {
		if len(source.frames[i]) != n {
			t.Fatalf("frame %d has %d fingers, want %d", i, len(source.frames[i]), n)
		}
	}
	if source.frames[4][0].ID != 0 {
		t.Fatal("releasing mapped keys also released the mouse")
	}
}

func TestKeymapEditingAndPersistence(t *testing.T) {
	store := &keymapStore{path: filepath.Join(t.TempDir(), "keymap.json")}
	bindings, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	v := &viewer{keymap: store, bindings: bindings, width: 100, height: 100, events: make(chan pointerEvent, 16), done: make(chan struct{})}
	tester := ui.NewTester(v.view, 700, 500)
	if err = tester.Click("编辑键位"); err != nil {
		t.Fatal(err)
	}
	tester.ClickAt(350, 300)
	if v.pendingPoint == nil {
		t.Fatal("screen position was not selected")
	}
	tester.CodeDown(0, ui.CodeKeyA)
	tester.CodeUp(0, ui.CodeKeyA)
	point, ok := v.bindings[uint16(ui.CodeKeyA)]
	if !ok || point.X < 0 || point.X > 1 || point.Y < 0 || point.Y > 1 {
		t.Fatalf("binding not saved: %+v", v.bindings)
	}
	other := &keymapStore{path: store.path}
	loaded, err := other.read()
	if err != nil || loaded[uint16(ui.CodeKeyA)] != point {
		t.Fatalf("binding not persisted: %+v %v", loaded, err)
	}
	if err = tester.Click("完成键位"); err != nil {
		t.Fatal(err)
	}
	tester.ClickAt(350, 300)
	for len(v.events) != 0 {
		<-v.events
	}
	tester.CodeDown(0, ui.CodeKeyA)
	tester.CodeUp(0, ui.CodeKeyA)
	if len(v.events) != 2 {
		t.Fatalf("expected mapped down and up, got %d", len(v.events))
	}
	down, up := <-v.events, <-v.events
	if down.kind != mappedDown || up.kind != mappedUp || down.x != int(point.X*99+0.5) {
		t.Fatalf("unexpected mapped events: %+v %+v", down, up)
	}
	if err = other.save(map[uint16]mappedPoint{}); err != nil {
		t.Fatal(err)
	}
	if err = store.save(v.bindings); err == nil {
		t.Fatal("overwrote another window's changes")
	}
}

func TestFitPoint(t *testing.T) {
	if _, _, ok := fitPoint(10, 50, 200, 100, 100, 100, false); ok {
		t.Fatal("letterbox accepted")
	}
	x, y, ok := fitPoint(50, 0, 200, 100, 100, 100, false)
	if !ok || x != 0 || y != 0 {
		t.Fatalf("unexpected origin: %d,%d %v", x, y, ok)
	}
	x, y, ok = fitPoint(300, 100, 200, 100, 100, 100, true)
	if !ok || x != 99 || y != 99 {
		t.Fatalf("unexpected clamp: %d,%d %v", x, y, ok)
	}
}

func TestViewerPointerSendsDownAndUp(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 230, G: 40, B: 20, A: 255})
		}
	}
	v := &viewer{bitmap: ui.NewBitmap(frame), width: 100, height: 100, events: make(chan pointerEvent, 8), done: make(chan struct{})}
	tester := ui.NewTester(v.view, 300, 300)
	r, g, b, _ := tester.Image().At(150, 150).RGBA()
	if r < 50000 || g > 18000 || b > 12000 {
		t.Fatalf("frame was not rendered at center: %d,%d,%d", r, g, b)
	}
	tester.ClickAt(150, 180)
	if len(v.events) != 2 {
		t.Fatalf("expected down and up, got %d", len(v.events))
	}
	down, up := <-v.events, <-v.events
	if down.kind != pointerDown || up.kind != pointerUp || down.x < 40 || down.x > 60 || down.y < 40 || down.y > 60 {
		t.Fatalf("unexpected events: %+v %+v", down, up)
	}
}

func TestViewerNavigationQueuesHome(t *testing.T) {
	v := &viewer{events: make(chan pointerEvent, 8), done: make(chan struct{})}
	tester := ui.NewTester(v.view, 600, 400)
	if err := tester.Click("主页"); err != nil {
		t.Fatal(err)
	}
	if len(v.events) != 1 {
		t.Fatalf("expected one navigation event, got %d", len(v.events))
	}
	e := <-v.events
	if e.kind != navigate || e.action != display.Home {
		t.Fatalf("unexpected navigation event: %+v", e)
	}
}

func TestViewerKeyboardAndFocusLoss(t *testing.T) {
	v := &viewer{events: make(chan pointerEvent, 8), done: make(chan struct{})}
	tester := ui.NewTester(v.view, 300, 300)
	tester.CodeDown(0, ui.CodeKeyA)
	tester.CodeDown(0, ui.CodeKeyA)
	tester.CodeUp(0, ui.CodeKeyA)
	if len(v.events) != 2 {
		t.Fatalf("expected one down and up, got %d", len(v.events))
	}
	down, up := <-v.events, <-v.events
	if down.kind != keyDown || up.kind != keyUp || down.usage != uint16(ui.CodeKeyA) {
		t.Fatalf("unexpected events: %+v %+v", down, up)
	}
	tester.CodeDown(0, ui.CodeKeyW)
	v.releaseHostKeys()
	if len(v.events) != 2 {
		t.Fatalf("expected held key and release-all, got %d", len(v.events))
	}
	down, up = <-v.events, <-v.events
	if down.kind != keyDown || up.kind != releaseKeys || len(v.keys) != 0 {
		t.Fatalf("keys not cleared on blur: %+v %+v", down, up)
	}
}

func TestViewerReleasesPointerAndKeysOnBlur(t *testing.T) {
	v := &viewer{held: true, keys: map[uint16]bool{4: true}, events: make(chan pointerEvent, 4), done: make(chan struct{})}
	v.releaseHostInput()
	v.releaseHostInput()
	if v.held || len(v.keys) != 0 || len(v.events) != 2 {
		t.Fatalf("input not cleared: %+v", v)
	}
	if (<-v.events).kind != pointerUp || (<-v.events).kind != releaseKeys {
		t.Fatal("incorrect release ordering")
	}
}

func TestViewerReleasesInputOnFrameRotation(t *testing.T) {
	v := &viewer{held: true, keys: map[uint16]bool{0x1a: true}, width: 720, height: 1280, events: make(chan pointerEvent, 4), done: make(chan struct{})}
	v.setFrameSize(1280, 720)
	if v.held || len(v.keys) != 0 || len(v.events) != 2 {
		t.Fatal("rotation did not release host input")
	}
	if (<-v.events).kind != pointerUp || (<-v.events).kind != releaseKeys {
		t.Fatal("rotation did not queue both releases")
	}
	if v.width != 1280 || v.height != 720 {
		t.Fatal("new frame size not recorded")
	}
}
