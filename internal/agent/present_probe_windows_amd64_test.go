//go:build windows && amd64

package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--vulkan-present-probe" {
		switch os.Getenv("NEKO_TEST_PROBE_HELPER") {
		case "valid":
			fmt.Print(`{"name":"fake","test_surface":{"width":64,"height":64,"format":44,"image_count":3,"clear_submitted":true}}`)
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

func TestPresentProbeSubprocessValidation(t *testing.T) {
	t.Setenv("NEKO_TEST_PROBE_HELPER", "valid")
	got, err := runPresentProbe(context.Background())
	if err != nil || got.Name != "fake" || got.TestSurface == nil || !got.TestSurface.ClearSubmitted {
		t.Fatalf("valid child response %+v: %v", got, err)
	}
	for _, mode := range []string{"invalid", "large"} {
		t.Setenv("NEKO_TEST_PROBE_HELPER", mode)
		if _, err := runPresentProbe(context.Background()); err == nil {
			t.Fatalf("accepted %s child output", mode)
		}
	}
	t.Setenv("NEKO_TEST_PROBE_HELPER", "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := runPresentProbe(ctx); err == nil || ctx.Err() == nil {
		t.Fatalf("cancellation did not stop probe: %v", err)
	}
}
