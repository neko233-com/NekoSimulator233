//go:build windows && amd64

package agent

import (
	"context"
	"fmt"
	"image"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/frameprobe"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && (os.Args[1] == "--vulkan-present-probe" || os.Args[1] == "--vulkan-pixel-probe" || os.Args[1] == "--vulkan-guest-frame-probe") {
		switch os.Getenv("NEKO_TEST_PROBE_HELPER") {
		case "valid":
			if os.Args[1] == "--vulkan-guest-frame-probe" {
				frame, err := frameprobe.Read(os.Stdin)
				if err != nil || frame.Rect.Dx() != 2 || frame.Rect.Dy() != 2 || frame.Pix[0] != 42 || frame.Pix[8] != 99 {
					os.Exit(2)
				}
				fmt.Print(`{"name":"fake","test_surface":{"width":2,"height":2,"format":44,"image_count":3,"pixels_verified":true}}`)
				os.Exit(0)
			}
			fmt.Print(`{"name":"fake","test_surface":{"width":64,"height":64,"format":44,"image_count":3,"clear_submitted":true,"pixels_verified":true}}`)
			os.Exit(0)
		case "invalid":
			fmt.Print(`{"test_surface":{"clear_submitted":false}}`)
			os.Exit(0)
		case "large":
			fmt.Print(strings.Repeat("x", 65537))
			os.Exit(0)
		case "wait":
			time.Sleep(30 * time.Second)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestGuestFrameProbeSubprocess(t *testing.T) {
	t.Setenv("NEKO_TEST_PROBE_HELPER", "valid")
	parent := image.NewRGBA(image.Rect(0, 0, 4, 4))
	frame := parent.SubImage(image.Rect(1, 1, 3, 3)).(*image.RGBA)
	frame.Pix[0], frame.Pix[frame.Stride] = 42, 99
	info, err := runGuestFrameProbe(context.Background(), frame)
	if err != nil || info.Name != "fake" || !info.TestSurface.PixelsVerified {
		t.Fatalf("probe: %+v %v", info, err)
	}
	t.Setenv("NEKO_TEST_PROBE_HELPER", "invalid")
	if _, err := runGuestFrameProbe(context.Background(), frame); err == nil {
		t.Fatal("accepted invalid child response")
	}
	t.Setenv("NEKO_TEST_PROBE_HELPER", "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := runGuestFrameProbe(ctx, frame); err == nil || ctx.Err() == nil {
		t.Fatalf("cancellation did not stop probe: %v", err)
	}
}

func TestPresentProbeSubprocessValidation(t *testing.T) {
	t.Setenv("NEKO_TEST_PROBE_HELPER", "valid")
	got, err := runPresentProbe(context.Background(), true)
	if err != nil || got.Name != "fake" || got.TestSurface == nil || !got.TestSurface.ClearSubmitted {
		t.Fatalf("valid child response %+v: %v", got, err)
	}
	for _, mode := range []string{"invalid", "large"} {
		t.Setenv("NEKO_TEST_PROBE_HELPER", mode)
		if _, err := runPresentProbe(context.Background(), false); err == nil {
			t.Fatalf("accepted %s child output", mode)
		}
	}
	t.Setenv("NEKO_TEST_PROBE_HELPER", "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := runPresentProbe(ctx, false); err == nil || ctx.Err() == nil {
		t.Fatalf("cancellation did not stop probe: %v", err)
	}
}
