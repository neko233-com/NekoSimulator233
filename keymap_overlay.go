package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/egoist/mygo/ui"
)

func keyLabel(code uint16) string {
	if code >= 0x04 && code <= 0x1d {
		return string(rune('A' + code - 0x04))
	}
	if code >= 0x1e && code <= 0x26 {
		return string(rune('1' + code - 0x1e))
	}
	if code == 0x27 {
		return "0"
	}
	if name, ok := map[uint16]string{0x28: "Enter", 0x29: "Esc", 0x2b: "Tab", 0x2c: "Space", 0x4f: "→", 0x50: "←", 0x51: "↓", 0x52: "↑"}[code]; ok {
		return name
	}
	return fmt.Sprintf("%02X", code)
}

// The overlay shares the same contain geometry as fitPoint and is shown only
// while editing; it never modifies the guest framebuffer.
func drawKeymapOverlay(p *ui.Painter, r ui.Rect, width, height int, bindings map[uint16]mappedPoint, stick *joystickConfig, pending *mappedPoint) {
	if width <= 0 || height <= 0 {
		return
	}
	scale := math.Min(float64(r.W)/float64(width), float64(r.H)/float64(height))
	left := float64(r.X) + (float64(r.W)-float64(width)*scale)/2
	top := float64(r.Y) + (float64(r.H)-float64(height)*scale)/2
	point := func(pos mappedPoint) (float32, float32) {
		return float32(left + pos.X*float64(width-1)*scale), float32(top + pos.Y*float64(height-1)*scale)
	}
	p.Clip(r, 0, func() {
		if stick != nil {
			x, y := point(stick.Center)
			radius := float32(stick.Radius * float64(min(width-1, height-1)) * scale)
			circle := ui.Rect{X: x - radius, Y: y - radius, W: radius * 2, H: radius * 2}
			p.Fill(circle, ui.RGBA(20, 32, 46, 0.52), radius)
			p.Stroke(circle, ui.RGB(78, 221, 238), radius, 2)
			p.Text(x-20, y-8, "WASD", 13, ui.RGB(255, 255, 255))
		}
		keys := make([]int, 0, len(bindings))
		for key := range bindings {
			keys = append(keys, int(key))
		}
		sort.Ints(keys)
		for _, key := range keys {
			x, y := point(bindings[uint16(key)])
			box := ui.Rect{X: x - 15, Y: y - 15, W: 30, H: 30}
			p.Fill(box, ui.RGBA(20, 32, 46, 0.82), 15)
			p.Stroke(box, ui.RGB(78, 221, 238), 15, 2)
			p.Text(x-9, y-8, keyLabel(uint16(key)), 13, ui.RGB(255, 255, 255))
		}
		if pending != nil {
			x, y := point(*pending)
			p.Stroke(ui.Rect{X: x - 12, Y: y - 12, W: 24, H: 24}, ui.RGB(255, 210, 80), 12, 3)
		}
	})
}
