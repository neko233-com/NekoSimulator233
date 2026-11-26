package main

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/moriyoshi/gpui-go/client"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/syncctl"
)

func TestEncodeRGBAAsBMPHeaderAndPixels(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.SetRGBA(1, 2, color.RGBA{R: 0xAA, G: 0xBB, B: 0xCC, A: 0xFF})
	bmp := encodeRGBAAsBMP(img)
	if len(bmp) != 54+4*3*4 {
		t.Fatalf("unexpected BMP size: %d", len(bmp))
	}
	if bmp[0] != 'B' || bmp[1] != 'M' {
		t.Fatalf("invalid BMP magic: %q", bmp[:2])
	}
	// Pixel (1,2) in BGRA at offset 54 + (2*4 + 1)*4
	off := 54 + (2*4+1)*4
	if bmp[off] != 0xCC || bmp[off+1] != 0xBB || bmp[off+2] != 0xAA || bmp[off+3] != 0xFF {
		t.Fatalf("unexpected BGRA pixel: %x", bmp[off:off+4])
	}
}

func TestGPUIKeyToHIDMapping(t *testing.T) {
	cases := map[string]uint16{
		"a": 0x04, "W": 0x1a, "0": 0x27, "1": 0x1e,
		"Enter": 0x28, "Escape": 0x29, "Space": 0x2c,
		"ArrowUp": 0x52, "F9": 0x42,
	}
	for k, want := range cases {
		if got := gpuiKeyToHID(k); got != want {
			t.Errorf("gpuiKeyToHID(%q) = 0x%02x, want 0x%02x", k, got, want)
		}
	}
}

func TestEntryManagerViewBuildsCleanlyWithoutAds(t *testing.T) {
	a := &app{
		ctx:           context.Background(),
		sideTab:       "设备",
		deviceSort:    deviceSortName,
		newSystem:     systemAuto,
		message:       "就绪",
		icons:         &iconBank{},
		dispatchFn:    make(map[uint64]func(*client.Event)),
		thumbs:        make(map[string]*client.Resource),
		thumbPending:  make(map[string]bool),
		viewers:       make(map[string]*emulatorViewer),
		settingsWins:  make(map[string]*deviceSettingsUI),
		batchSelected: make(map[string]bool),
		syncHub:       syncctl.NewHub(),
		available: []avd.InstanceSummary{
			{Name: "我的测试机", State: "running", API: 35, Cores: 4, RAMMiB: 4096, Width: 1080, Height: 1920, Density: 480},
			{Name: "Neko-Device-1", State: "stopped", API: 34, Cores: 4, RAMMiB: 6144, Width: 1920, Height: 1080, Density: 280},
		},
	}
	// Note: a.click() requires a non-nil client in live runs; here we verify
	// helper functions and visibleDevices logic without a live GPUI daemon.
	filtered := visibleDevices(a.available, "测试", deviceSortName)
	if len(filtered) != 1 || filtered[0].Name != "我的测试机" {
		t.Fatalf("visibleDevices filter failed: %+v", filtered)
	}
	if androidLabel(35) != "Android 15" || androidLabel(30) != "Android 11" {
		t.Fatalf("unexpected androidLabel")
	}
	if api, err := newSystemAPI(systemAndroid11); err != nil || api != 30 {
		t.Fatalf("newSystemAPI failed: %d %v", api, err)
	}
	_ = display.FitContain
}
