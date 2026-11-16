// Package previewinput defines the bounded, screen-content-free input protocol
// between the experimental Vulkan window and its parent process.
package previewinput

type Event struct {
	Kind        string `json:"kind"`
	X           int    `json:"x,omitempty"`
	Y           int    `json:"y,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	FrameWidth  int    `json:"frame_width,omitempty"`
	FrameHeight int    `json:"frame_height,omitempty"`
	Usage       uint16 `json:"usage,omitempty"`
}

func (e Event) Valid() bool {
	if e.Kind == "key_down" || e.Kind == "key_up" {
		return e.Usage >= 0x04 && e.Usage <= 0xa4 || e.Usage >= 0xe0 && e.Usage <= 0xe7
	}
	if e.Kind == "release" || e.Kind == "up" {
		return true
	}
	if e.Kind != "down" && e.Kind != "move" {
		return false
	}
	return e.Width > 0 && e.Height > 0 && e.Width <= 8192 && e.Height <= 8192 && e.Width*e.Height <= 32*1024*1024 && e.FrameWidth > 0 && e.FrameHeight > 0 && e.FrameWidth <= 8192 && e.FrameHeight <= 8192 && e.FrameWidth*e.FrameHeight <= 32*1024*1024 && e.X >= -32768 && e.X <= 32767 && e.Y >= -32768 && e.Y <= 32767
}

// Point matches the integer contain geometry and nearest-neighbor source
// selection used by the Vulkan staging path. Drags clamp to the picture.
func (e Event) Point(clamp bool) (int, int, bool) {
	if !e.Valid() || (e.Kind != "down" && e.Kind != "move") {
		return 0, 0, false
	}
	w, h, sw, sh := e.Width, e.Height, e.FrameWidth, e.FrameHeight
	fw, fh := w, h
	if w*sh <= h*sw {
		fh = max(1, w*sh/sw)
	} else {
		fw = max(1, h*sw/sh)
	}
	left, top := (w-fw)/2, (h-fh)/2
	x, y := e.X-left, e.Y-top
	if !clamp && (x < 0 || y < 0 || x >= fw || y >= fh) {
		return 0, 0, false
	}
	x, y = max(0, min(fw-1, x)), max(0, min(fh-1, y))
	return x * sw / fw, y * sh / fh, true
}
