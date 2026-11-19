package viewlayout

import "testing"

func TestTileEmpty(t *testing.T) {
	if got := Tile(0, Rect{Width: 100, Height: 100}); len(got) != 0 {
		t.Fatalf("expected no tiles, got %v", got)
	}
	if got := Tile(2, Rect{}); len(got) != 0 {
		t.Fatalf("expected no tiles for empty area, got %v", got)
	}
}

func TestTileCoversAreaWithoutOverlap(t *testing.T) {
	area := Rect{X: 10, Y: 20, Width: 1600, Height: 900}
	for _, n := range []int{1, 2, 3, 4, 5, 6, 8} {
		tiles := Tile(n, area)
		if len(tiles) != n {
			t.Fatalf("n=%d: got %d tiles", n, len(tiles))
		}
		seen := map[Rect]bool{}
		for _, r := range tiles {
			if seen[r] {
				t.Fatalf("n=%d: duplicate tile %+v", n, r)
			}
			seen[r] = true
			if r.X < area.X || r.Y < area.Y || r.X+r.Width > area.X+area.Width || r.Y+r.Height > area.Y+area.Height {
				t.Fatalf("n=%d: tile outside area: %+v", n, r)
			}
			if r.Width <= 0 || r.Height <= 0 {
				t.Fatalf("n=%d: non-positive tile: %+v", n, r)
			}
		}
	}
}

func TestTilePrefersColumnsForMultiple(t *testing.T) {
	tiles := Tile(4, Rect{Width: 1600, Height: 900})
	if len(tiles) != 4 {
		t.Fatalf("got %d tiles", len(tiles))
	}
	// MuMu's default is 3 columns per line (grid_custom_num_one_line=3).
	if MuMuColumns != 3 || tiles[1].X <= tiles[0].X || tiles[2].X <= tiles[1].X || tiles[3].Y <= tiles[0].Y {
		t.Fatalf("expected 3-column grid, got %+v", tiles)
	}
	if tiles[0].Width != tiles[1].Width || tiles[0].Height != tiles[3].Height {
		t.Fatalf("tiles are not uniform: %+v", tiles)
	}
}
