package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/moriyoshi/gpui-go/scene"
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
	if name, ok := map[uint16]string{
		0x28: "Enter", 0x29: "Esc", 0x2b: "Tab", 0x2c: "Space",
		0x4f: "→", 0x50: "←", 0x51: "↓", 0x52: "↑",
	}[code]; ok {
		return name
	}
	return fmt.Sprintf("%02X", code)
}

// buildKeymapOverlayNodes returns GPUI overlay nodes (a Canvas for circles/joystick
// plus positioned keycap labels) over the emulator viewport.
func buildKeymapOverlayNodes(viewW, viewH float32, frameW, frameH int, bindings map[uint16]mappedPoint, stick *joystickConfig, pending *mappedPoint) []*scene.Node {
	if frameW <= 0 || frameH <= 0 || viewW <= 0 || viewH <= 0 {
		return nil
	}
	scale := math.Min(float64(viewW)/float64(frameW), float64(viewH)/float64(frameH))
	left := (float64(viewW) - float64(frameW)*scale) / 2
	top := (float64(viewH) - float64(frameH)*scale) / 2

	point := func(pos mappedPoint) (float32, float32) {
		return float32(left + pos.X*float64(frameW-1)*scale),
			float32(top + pos.Y*float64(frameH-1)*scale)
	}

	var cmds []*scene.DrawCommand
	var labels []*scene.Node
	if stick != nil {
		cx, cy := point(stick.Center)
		r := float32(stick.Radius * float64(min(frameW-1, frameH-1)) * scale)
		cmds = append(cmds, &scene.DrawCommand{
			Kind: &scene.DrawCommandRect{
				Rect: &scene.DrawRect{
					Bounds:      &scene.Bounds{Origin: &scene.Point{X: cx - r, Y: cy - r}, Size: &scene.Size{Width: r * 2, Height: r * 2}},
					Fill:        &scene.Color{Red: 0.08, Green: 0.13, Blue: 0.18, Alpha: 0.45},
					Border:      scene.RGB(0x4EDDEE),
					BorderWidth: 2,
					Radius:      r,
				},
			},
		})
		labels = append(labels, scene.Div(eid(), labelText("WASD", 10, 0x4EDDEE, true)).Style(&scene.Style{
			Position: scene.PositionAbsolute.Enum(),
			Inset:    &scene.Edges{Left: scene.Pixels(cx - 14), Top: scene.Pixels(cy - 7)},
		}))
	}

	keys := make([]int, 0, len(bindings))
	for k := range bindings {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	for _, k := range keys {
		cx, cy := point(bindings[uint16(k)])
		cmds = append(cmds, &scene.DrawCommand{
			Kind: &scene.DrawCommandRect{
				Rect: &scene.DrawRect{
					Bounds:      &scene.Bounds{Origin: &scene.Point{X: cx - 15, Y: cy - 15}, Size: &scene.Size{Width: 30, Height: 30}},
					Fill:        &scene.Color{Red: 0.08, Green: 0.13, Blue: 0.18, Alpha: 0.82},
					Border:      scene.RGB(0x4EDDEE),
					BorderWidth: 2,
					Radius:      15,
				},
			},
		})
		lbl := keyLabel(uint16(k))
		labels = append(labels, scene.Div(eid(), labelText(lbl, 10, colWhite, true)).Style(&scene.Style{
			Position: scene.PositionAbsolute.Enum(),
			Inset:    &scene.Edges{Left: scene.Pixels(cx - float32(len(lbl)*3)), Top: scene.Pixels(cy - 7)},
		}))
	}

	if pending != nil {
		cx, cy := point(*pending)
		cmds = append(cmds, &scene.DrawCommand{
			Kind: &scene.DrawCommandRect{
				Rect: &scene.DrawRect{
					Bounds:      &scene.Bounds{Origin: &scene.Point{X: cx - 14, Y: cy - 14}, Size: &scene.Size{Width: 28, Height: 28}},
					Border:      scene.RGB(0xFFD250),
					BorderWidth: 3,
					Radius:      14,
				},
			},
		})
	}

	if len(cmds) == 0 {
		return nil
	}
	canvas := scene.Canvas(eid(), cmds...).Style(&scene.Style{
		Position: scene.PositionAbsolute.Enum(),
		Inset:    &scene.Edges{Top: scene.Pixels(0), Left: scene.Pixels(0), Right: scene.Pixels(0), Bottom: scene.Pixels(0)},
	})
	return append([]*scene.Node{canvas}, labels...)
}
