package previewinput

import "testing"

func TestPointMatchesFittedFrame(t *testing.T) {
	e := Event{Kind: "down", Width: 4, Height: 4, FrameWidth: 4, FrameHeight: 2, X: 2, Y: 0}
	if _, _, ok := e.Point(false); ok {
		t.Fatal("black bar accepted")
	}
	e.Y = 1
	if x, y, ok := e.Point(false); !ok || x != 2 || y != 0 {
		t.Fatalf("wrong point %d %d %v", x, y, ok)
	}
	e.Kind, e.X, e.Y = "move", -10, 10
	if x, y, ok := e.Point(true); !ok || x != 0 || y != 1 {
		t.Fatalf("wrong drag %d %d %v", x, y, ok)
	}
	e.Width = 8193
	if e.Valid() {
		t.Fatal("oversized window accepted")
	}
}

func TestVisibilityEvents(t *testing.T) {
	for _, kind := range []string{"visible", "hidden", "ready"} {
		if !(Event{Kind: kind}).Valid() {
			t.Fatalf("%s rejected", kind)
		}
	}
	if (Event{Kind: "visibility"}).Valid() {
		t.Fatal("unknown visibility accepted")
	}
}

func TestGPUAndCPUScaledPointUseTheirOwnSampling(t *testing.T) {
	e := Event{Kind: "down", X: 3, Y: 3, Width: 5, Height: 5, FrameWidth: 3, FrameHeight: 3}
	if x, y, ok := e.Point(false); !ok || x != 1 || y != 1 {
		t.Fatalf("CPU sample %d,%d %v", x, y, ok)
	}
	e.GPUScaled = true
	if x, y, ok := e.Point(false); !ok || x != 2 || y != 2 {
		t.Fatalf("GPU sample %d,%d %v", x, y, ok)
	}
}
