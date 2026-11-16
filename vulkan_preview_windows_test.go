package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/frameprobe"
	"github.com/neko233-com/NekoSimulator233/internal/previewinput"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--vulkan-preview-worker" {
		switch os.Getenv("NEKO_TEST_PREVIEW_HELPER") {
		case "frames":
			var previous *image.RGBA
			for _, value := range []byte{11, 22} {
				frame, err := frameprobe.ReadNext(os.Stdin, previous)
				if err != nil || frame.Pix[0] != value || previous != nil && frame != previous {
					os.Exit(2)
				}
				previous = frame
			}
			os.Exit(0)
		case "wait":
			time.Sleep(30 * time.Second)
			os.Exit(0)
		case "events":
			fmt.Println(`{"kind":"down","x":1,"y":1,"width":2,"height":2,"frame_width":2,"frame_height":2}`)
			fmt.Println(`{"kind":"key_down","usage":26}`)
			fmt.Println(`{"kind":"key_up","usage":26}`)
			fmt.Println(`{"kind":"up"}`)
			os.Exit(0)
		case "window-state":
			fmt.Println(`{"kind":"visible"}`)
			fmt.Println(`{"kind":"hidden"}`)
			fmt.Println(`{"kind":"visible"}`)
			os.Exit(0)
		case "inactive":
			if _, err := frameprobe.ReadNext(os.Stdin, nil); !errors.Is(err, frameprobe.ErrInactive) {
				os.Exit(2)
			}
			frame, err := frameprobe.ReadNext(os.Stdin, nil)
			if err != nil || frame.Pix[0] != 42 {
				os.Exit(3)
			}
			os.Exit(0)
		case "visibility":
			if _, err := frameprobe.ReadNext(os.Stdin, nil); !errors.Is(err, frameprobe.ErrInactive) {
				os.Exit(2)
			}
			fmt.Println(`{"kind":"release"}`)
			_, _ = frameprobe.ReadNext(os.Stdin, nil)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestVulkanPreviewVisibilityPipeAndExit(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "window-state")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan previewinput.Event, 5)
	p, err := startVulkanPreview(ctx, func(e previewinput.Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, want := range []string{"visible", "hidden", "visible", "release", "hidden"} {
		select {
		case event := <-events:
			if event.Kind != want {
				t.Fatalf("got %q want %q", event.Kind, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("missing %s event", want)
		}
	}
}

func TestSlowCaptureDoesNotDelayPreviewClear(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "visibility")
	t.Setenv("NEKO_EXPERIMENTAL_VULKAN_VIEWER", "")
	t.Setenv("NEKO_EXPERIMENTAL_STREAM", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan previewinput.Event, 2)
	p, err := startVulkanPreview(ctx, func(e previewinput.Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	s := &slowCaptureSession{capturing: make(chan struct{}), unblock: make(chan struct{}), input: make(chan struct{}), released: make(chan struct{}), closed: make(chan struct{})}
	v := &viewer{source: s, preview: p, width: 4, height: 2, events: make(chan pointerEvent, 8), done: make(chan struct{}), visibilityCh: make(chan struct{}, 1)}
	go v.run()
	select {
	case <-s.capturing:
	case <-time.After(5 * time.Second):
		v.stop()
		close(s.unblock)
		t.Fatal("capture did not start")
	}
	v.hidden.Store(true)
	v.suspendPresentation()
	select {
	case event := <-events:
		if event.Kind != "release" {
			t.Fatalf("unexpected preview event: %+v", event)
		}
	case <-time.After(5 * time.Second):
		v.stop()
		close(s.unblock)
		t.Fatal("preview clear waited for blocked capture")
	}
	v.stop()
	close(s.unblock)
	select {
	case <-s.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("viewer did not release capture before closing source")
	}
}

func TestVulkanPreviewInactiveThenResume(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "inactive")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := startVulkanPreview(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err = p.inactive(); err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	frame.Pix[0] = 42
	if err = p.send(frame); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit")
	}
}

func TestHiddenViewerInvalidatesInFlightFrameAndClearsPreview(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "inactive")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := startVulkanPreview(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	v := &viewer{preview: p, width: 4, height: 2, done: make(chan struct{}), events: make(chan pointerEvent, 8), visibilityCh: make(chan struct{}, 1)}
	v.previewShown.Store(true)
	v.previewReady = true
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	<-v.events
	oldEpoch := v.visEpoch.Load()
	v.hidden.Store(true)
	v.suspendPresentation()
	if e := <-v.events; e.kind != releaseKeys {
		t.Fatalf("hiding did not release input: %+v", e)
	}
	// A rapid show must not make the in-flight frame from before hiding valid.
	v.hidden.Store(false)
	v.notifyVisibility()
	if v.frameCurrent(oldEpoch) || v.width != 0 || v.height != 0 {
		t.Fatal("stale frame survived hide and show")
	}
	v.applyPreviewInput(previewinput.Event{Kind: "key_down", Usage: 0x04})
	if len(v.events) != 0 {
		t.Fatal("preview accepted input before a fresh frame")
	}
	v.syncPreviewVisibility()
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	frame.Pix[0] = 42
	if err := p.send(frame); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preview did not receive clear followed by frame")
	}
}

func TestVulkanPreviewInputPipeAndExitRelease(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "events")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan previewinput.Event, 6)
	p, err := startVulkanPreview(ctx, func(e previewinput.Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, want := range []string{"down", "key_down", "key_up", "up", "release", "hidden"} {
		select {
		case event := <-events:
			if event.Kind != want {
				t.Fatalf("got %q want %q", event.Kind, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing input event")
		}
	}
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit")
	}
}

func TestVulkanPreviewPipeMultipleFrames(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "frames")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := startVulkanPreview(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for _, value := range []byte{11, 22} {
		frame.Pix[0] = value
		if err := p.send(frame); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit")
	}
}

func TestVulkanPreviewCancellationUnblocksPipe(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "wait")
	ctx, cancel := context.WithCancel(context.Background())
	p, err := startVulkanPreview(ctx, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer p.close()
	finished := make(chan error, 1)
	go func() { finished <- p.send(image.NewRGBA(image.Rect(0, 0, 512, 512))) }()
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("stalled pipe succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not unblock pipe")
	}
}
