//go:build windows

package mumu

import (
	"os"
	"testing"
)

func TestLiveRendererCapture(t *testing.T) {
	if os.Getenv("NEKO_TEST_LIVE_RENDERER") != "1" {
		t.Skip("set NEKO_TEST_LIVE_RENDERER=1 for read-only MuMu capture")
	}
	root, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	instances, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if !instance.AndroidStarted {
			continue
		}
		r, err := OpenRenderer(root, instance)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := r.Capture(nil)
		if err != nil {
			r.Close()
			t.Fatal(err)
		}
		if frame.Bounds().Dx() <= 0 || frame.Bounds().Dy() <= 0 || len(frame.Pix) != frame.Stride*frame.Bounds().Dy() {
			t.Fatal("invalid frame")
		}
		t.Logf("captured %dx%d from instance %s", frame.Bounds().Dx(), frame.Bounds().Dy(), instance.Index)
		var aMin, aMax byte = 255, 0
		for i := 3; i < len(frame.Pix); i += 4 {
			a := frame.Pix[i]
			if a < aMin {
				aMin = a
			}
			if a > aMax {
				aMax = a
			}
		}
		t.Logf("frame alpha range %d..%d", aMin, aMax)
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Capture(nil); err == nil {
			t.Fatal("capture after close succeeded")
		}
		return
	}
	t.Fatal("no running Android instance")
}

func TestKeyboardUsageMapping(t *testing.T) {
	for usage, want := range map[uint16]uint16{0x04: 30, 0x1a: 17, 0x2c: 57, 0x4f: 106, 0xe1: 42, 0xff: 0} {
		if got := linuxKey(usage); got != want {
			t.Errorf("USB usage %x: got %d, want %d", usage, got, want)
		}
	}
}

func BenchmarkLiveRendererCapture(b *testing.B) {
	if os.Getenv("NEKO_TEST_LIVE_RENDERER") != "1" {
		b.Skip("requires a running MuMu instance")
	}
	root, err := Discover()
	if err != nil {
		b.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		b.Fatal(err)
	}
	instances, err := client.List()
	if err != nil {
		b.Fatal(err)
	}
	var r *Renderer
	for _, instance := range instances {
		if instance.AndroidStarted {
			r, err = OpenRenderer(root, instance)
			break
		}
	}
	if err != nil {
		b.Fatal(err)
	}
	if r == nil {
		b.Fatal("no running instance")
	}
	defer r.Close()
	b.ReportAllocs()
	frame, err := r.Capture(nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame, err = r.Capture(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}
