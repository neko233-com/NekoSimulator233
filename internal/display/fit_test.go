package display

import "testing"

func TestMapPointContain(t *testing.T) {
	// 200x100 area, square 100x100 frame: drawn 100x100 centered with bars.
	if _, _, ok := MapPoint(FitContain, 10, 50, 200, 100, 100, 100, false); ok {
		t.Fatal("tap on the letterbox must be rejected")
	}
	x, y, ok := MapPoint(FitContain, 50, 0, 200, 100, 100, 100, false)
	if !ok || x != 0 || y != 0 {
		t.Fatalf("contain top-left = (%d,%d,%v)", x, y, ok)
	}
}

func TestMapPointCover(t *testing.T) {
	// Cover fills 200x100 with a square frame: scale 2, horizontal crop.
	x, y, ok := MapPoint(FitCover, 100, 50, 200, 100, 100, 100, false)
	if !ok || x != 50 || y != 50 {
		t.Fatalf("cover center = (%d,%d,%v)", x, y, ok)
	}
	// Every window point maps into the frame (cropped, never letterboxed).
	if _, _, ok := MapPoint(FitCover, 0, 0, 200, 100, 100, 100, false); !ok {
		t.Fatal("cover must accept corner points")
	}
}

func TestMapPointStretch(t *testing.T) {
	x, y, ok := MapPoint(FitStretch, 100, 50, 200, 100, 100, 100, false)
	if !ok || x != 50 || y != 50 {
		t.Fatalf("stretch center = (%d,%d,%v)", x, y, ok)
	}
	x, y, _ = MapPoint(FitStretch, 200, 100, 200, 100, 100, 100, true)
	if x != 99 || y != 99 {
		t.Fatalf("stretch clamp = (%d,%d)", x, y)
	}
}
