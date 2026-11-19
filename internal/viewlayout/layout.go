// Package viewlayout computes MuMu-style arrangements for emulator viewer
// windows. It is pure geometry so it can be unit-tested without a GUI.
package viewlayout

// Rect is a window rectangle in screen pixels.
type Rect struct {
	X, Y, Width, Height int
}

// Tile lays out n viewer windows inside area, mirroring MuMu's "排列窗口"
// grid. MuMu's window_arrange_settings.json uses grid_custom_num_one_line=3,
// so the grid has up to 3 columns. Unfilled cells stay empty rather than
// stretched, keeping every tile the same size.
func Tile(n int, area Rect) []Rect {
	if n <= 0 || area.Width <= 0 || area.Height <= 0 {
		return nil
	}
	cols := MuMuColumns
	if n < cols {
		cols = n
	}
	rows := (n + cols - 1) / cols
	w, h := area.Width/cols, area.Height/rows
	out := make([]Rect, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Rect{
			X:      area.X + (i%cols)*w,
			Y:      area.Y + (i/cols)*h,
			Width:  w,
			Height: h,
		})
	}
	return out
}

// MuMuColumns is the reverse-engineered default from the local MuMu
// window_arrange_settings.json (grid_custom_num_one_line).
const MuMuColumns = 3
