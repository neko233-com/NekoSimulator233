package main

import (
	"context"
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
			fmt.Println(`{"kind":"up"}`)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestVulkanPreviewInputPipeAndExitRelease(t *testing.T) {
	t.Setenv("NEKO_TEST_PREVIEW_HELPER", "events")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan previewinput.Event, 3)
	p, err := startVulkanPreview(ctx, func(e previewinput.Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, want := range []string{"down", "up", "release"} {
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
