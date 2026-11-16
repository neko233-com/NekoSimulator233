package main

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/previewinput"
)

type waitingFrameSource struct{ opened chan struct{} }
type waitingFrameStream struct{ ctx context.Context }

type observedFrameSource struct{ opened chan context.Context }

func (s *observedFrameSource) OpenFrameStream(ctx context.Context) (display.FrameStream, error) {
	s.opened <- ctx
	return &waitingFrameStream{ctx: ctx}, nil
}

func (s *waitingFrameSource) OpenFrameStream(ctx context.Context) (display.FrameStream, error) {
	s.opened <- struct{}{}
	return &waitingFrameStream{ctx: ctx}, nil
}
func (s *waitingFrameStream) Next(*image.RGBA) (*image.RGBA, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func TestViewerStreamPauseResumeAndStop(t *testing.T) {
	v := &viewer{done: make(chan struct{})}
	source := &waitingFrameSource{opened: make(chan struct{}, 2)}
	done := make(chan bool, 1)
	go func() { done <- v.runStream(source) }()
	waitOpen := func() {
		t.Helper()
		select {
		case <-source.opened:
		case <-time.After(3 * time.Second):
			t.Fatal("stream did not open")
		}
	}
	waitOpen()
	v.hidden.Store(true)
	v.cancelStream()
	v.hidden.Store(false)
	waitOpen()
	v.stop()
	select {
	case stopped := <-done:
		if !stopped {
			t.Fatal("cancellation incorrectly fell back to polling")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop")
	}
}

func TestPreviewKeepsStreamActiveWhenMainMinimized(t *testing.T) {
	v := &viewer{width: 4, height: 2, done: make(chan struct{}), events: make(chan pointerEvent, 8), visibilityCh: make(chan struct{}, 1)}
	v.setPreviewVisibility(true)
	v.applyPreviewInput(previewinput.Event{Kind: "ready"})
	source := &observedFrameSource{opened: make(chan context.Context, 2)}
	done := make(chan bool, 1)
	go func() { done <- v.runStream(source) }()
	var first context.Context
	select {
	case first = <-source.opened:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not open")
	}
	v.setMainVisibility(&v.minimized, true)
	select {
	case <-first.Done():
		t.Fatal("minimizing main window stopped visible preview")
	default:
	}
	if !v.captureVisible() || !v.frameCurrent(v.visEpoch.Load()) {
		t.Fatal("visible preview did not keep capture active")
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if e := <-v.events; e.kind != keyDown {
		t.Fatalf("preview input: %+v", e)
	}
	oldEpoch := v.visEpoch.Load()
	v.setPreviewVisibility(false)
	if e := <-v.events; e.kind != keyUp {
		t.Fatalf("preview hide did not release key: %+v", e)
	}
	select {
	case <-first.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stream remained active with both windows hidden")
	}
	if v.frameCurrent(oldEpoch) || v.width != 0 || v.height != 0 {
		t.Fatal("in-flight frame remained valid after both windows hid")
	}
	v.setPreviewVisibility(true)
	v.applyPreviewInput(previewinput.Event{Kind: "ready"})
	select {
	case <-source.opened:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not resume for preview")
	}
	v.stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop")
	}
}

func TestHiddenPreviewDoesNotPauseVisibleMain(t *testing.T) {
	v := &viewer{width: 4, height: 2, done: make(chan struct{}), events: make(chan pointerEvent, 8), visibilityCh: make(chan struct{}, 1)}
	v.setPreviewVisibility(true)
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if len(v.events) != 0 {
		t.Fatal("preview accepted input before its first presented frame")
	}
	v.applyPreviewInput(previewinput.Event{Kind: "ready"})
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	<-v.events
	epoch := v.visEpoch.Load()
	clear := v.clearEpoch.Load()
	v.setPreviewVisibility(false)
	if e := <-v.events; e.kind != keyUp {
		t.Fatalf("hidden preview did not release key: %+v", e)
	}
	if !v.captureVisible() || !v.frameCurrent(epoch) || v.clearEpoch.Load() == clear {
		t.Fatal("visible main window lost capture or preview was not invalidated")
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if len(v.events) != 0 {
		t.Fatal("hidden preview accepted input")
	}
	v.setPreviewVisibility(true)
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if len(v.events) != 0 {
		t.Fatal("restored preview accepted input before its first presented frame")
	}
	v.applyPreviewInput(previewinput.Event{Kind: "ready"})
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if e := <-v.events; e.kind != keyDown {
		t.Fatalf("restored preview did not accept input: %+v", e)
	}
}
