package display

import "math"

// FitMode controls how the guest frame fills the viewer area.
type FitMode int

const (
	// FitContain keeps the whole frame visible and letterboxes the rest.
	FitContain FitMode = iota
	// FitCover keeps the aspect ratio but fills the area, cropping the edges.
	// This is the MuMu-like "no black bars" default.
	FitCover
	// FitStretch fills the area ignoring aspect ratio.
	FitStretch
)

func (m FitMode) String() string {
	switch m {
	case FitCover:
		return "铺满"
	case FitStretch:
		return "拉伸"
	default:
		return "适应"
	}
}

// MapPoint converts a point in the viewer area (w x h) to guest coordinates
// (fw x fh) for the given fit mode. clamp=false rejects points outside the
// drawn frame (used for taps); clamp=true clamps them (used for drags).
func MapPoint(mode FitMode, x, y, w, h float32, fw, fh int, clamp bool) (int, int, bool) {
	if w <= 0 || h <= 0 || fw <= 0 || fh <= 0 {
		return 0, 0, false
	}
	var px, py float64
	switch mode {
	case FitStretch:
		px, py = float64(x)*float64(fw)/float64(w), float64(y)*float64(fh)/float64(h)
	case FitCover:
		s := math.Max(float64(w)/float64(fw), float64(h)/float64(fh))
		ox, oy := (float64(w)-float64(fw)*s)/2, (float64(h)-float64(fh)*s)/2
		px, py = (float64(x)-ox)/s, (float64(y)-oy)/s
	default: // FitContain
		s := math.Min(float64(w)/float64(fw), float64(h)/float64(fh))
		ox, oy := (float64(w)-float64(fw)*s)/2, (float64(h)-float64(fh)*s)/2
		px, py = (float64(x)-ox)/s, (float64(y)-oy)/s
	}
	if !clamp && (px < 0 || py < 0 || px >= float64(fw) || py >= float64(fh)) {
		return 0, 0, false
	}
	px = math.Max(0, math.Min(float64(fw-1), px))
	py = math.Max(0, math.Min(float64(fh-1), py))
	return int(px), int(py), true
}
