package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// generateNekoLogoPNG renders a crisp rounded-square blue-gradient Neko mascot
// icon with white cat ears, eyes, and nose/smile at the given square size.
func generateNekoLogoPNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	fSize := float64(size)
	rad := fSize * 0.24

	// Draw rounded square background with diagonal blue gradient.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(0, math.Abs(fx-fSize/2)-(fSize/2-rad))
			dy := math.Max(0, math.Abs(fy-fSize/2)-(fSize/2-rad))
			dist := math.Hypot(dx, dy) - rad
			alpha := clamp01(0.5 - dist)
			if alpha <= 0 {
				continue
			}
			t := (fx + fy) / (2 * fSize)
			r := lerpU8(0x38, 0x16, t)
			g := lerpU8(0x98, 0x5B, t)
			b := lerpU8(0xFF, 0xDB, t)
			img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: uint8(math.Round(alpha * 255))})
		}
	}

	blendWhite := func(x, y int, a float64) {
		if x < 0 || y < 0 || x >= size || y >= size || a <= 0 {
			return
		}
		bg := img.NRGBAAt(x, y)
		if bg.A == 0 {
			return
		}
		ia := 1 - a
		img.SetNRGBA(x, y, color.NRGBA{
			R: uint8(math.Round(float64(bg.R)*ia + 255*a)),
			G: uint8(math.Round(float64(bg.G)*ia + 255*a)),
			B: uint8(math.Round(float64(bg.B)*ia + 255*a)),
			A: bg.A,
		})
	}
	blendBlue := func(x, y int, a float64) {
		if x < 0 || y < 0 || x >= size || y >= size || a <= 0 {
			return
		}
		bg := img.NRGBAAt(x, y)
		ia := 1 - a
		img.SetNRGBA(x, y, color.NRGBA{
			R: uint8(math.Round(float64(bg.R)*ia + 0x1B*a)),
			G: uint8(math.Round(float64(bg.G)*ia + 0x5E*a)),
			B: uint8(math.Round(float64(bg.B)*ia + 0xD8*a)),
			A: bg.A,
		})
	}

	// Cat head rounded pill + two triangular ears in white.
	headCX, headCY := fSize*0.50, fSize*0.57
	headRX, headRY := fSize*0.30, fSize*0.23
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// Ellipse SDF for cat head
			nx := (fx - headCX) / headRX
			ny := (fy - headCY) / headRY
			headDist := (math.Hypot(nx, ny) - 1.0) * math.Min(headRX, headRY)
			// Left ear triangle: (0.24, 0.48), (0.27, 0.22), (0.43, 0.37)
			dLeft := triDist(fx, fy, fSize*0.23, fSize*0.49, fSize*0.26, fSize*0.21, fSize*0.44, fSize*0.38)
			// Right ear triangle: (0.76, 0.48), (0.73, 0.22), (0.57, 0.37)
			dRight := triDist(fx, fy, fSize*0.77, fSize*0.49, fSize*0.74, fSize*0.21, fSize*0.56, fSize*0.38)
			d := math.Min(headDist, math.Min(dLeft, dRight))
			a := clamp01(0.5 - d)
			if a > 0 {
				blendWhite(x, y, a)
			}
		}
	}

	// Eyes & small nose/smile in deep blue.
	eyeR := fSize * 0.048
	leftEyeX, rightEyeX, eyeY := fSize*0.39, fSize*0.61, fSize*0.56
	noseX, noseY, noseR := fSize*0.50, fSize*0.65, fSize*0.032
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			d1 := math.Hypot(fx-leftEyeX, fy-eyeY) - eyeR
			d2 := math.Hypot(fx-rightEyeX, fy-eyeY) - eyeR
			d3 := math.Hypot(fx-noseX, fy-noseY) - noseR
			d := math.Min(d1, math.Min(d2, d3))
			a := clamp01(0.5 - d)
			if a > 0 {
				blendBlue(x, y, a)
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func lerpU8(a, b uint8, t float64) uint8 {
	return uint8(math.Round(float64(a)*(1-t) + float64(b)*t))
}

func triDist(px, py, ax, ay, bx, by, cx, cy float64) float64 {
	// Signed distance to triangle (negative inside).
	ab := segDist(px, py, ax, ay, bx, by)
	bc := segDist(px, py, bx, by, cx, cy)
	ca := segDist(px, py, cx, cy, ax, ay)
	d := math.Min(ab, math.Min(bc, ca))
	s1 := (bx-ax)*(py-ay) - (by-ay)*(px-ax)
	s2 := (cx-bx)*(py-by) - (cy-by)*(px-bx)
	s3 := (ax-cx)*(py-cy) - (ay-cy)*(px-cx)
	if (s1 >= 0 && s2 >= 0 && s3 >= 0) || (s1 <= 0 && s2 <= 0 && s3 <= 0) {
		return -d
	}
	return d
}

func segDist(px, py, ax, ay, bx, by float64) float64 {
	vx, vy := bx-ax, by-ay
	wx, wy := px-ax, py-ay
	c1 := vx*wx + vy*wy
	if c1 <= 0 {
		return math.Hypot(px-ax, py-ay)
	}
	c2 := vx*vx + vy*vy
	if c2 <= c1 {
		return math.Hypot(px-bx, py-by)
	}
	t := c1 / c2
	return math.Hypot(px-(ax+t*vx), py-(ay+t*vy))
}

// buildICO packages PNG icons (16, 32, 48, 256) into a Windows .ico file.
func buildICO(sizes []int) []byte {
	type entry struct {
		size int
		png  []byte
	}
	entries := make([]entry, 0, len(sizes))
	for _, sz := range sizes {
		entries = append(entries, entry{size: sz, png: generateNekoLogoPNG(sz)})
	}
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))            // reserved
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))            // type = 1 (ICO)
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(entries))) // count
	offset := uint32(6 + 16*len(entries))
	for _, e := range entries {
		dim := uint8(e.size)
		if e.size >= 256 {
			dim = 0
		}
		out.WriteByte(dim)
		out.WriteByte(dim)
		out.WriteByte(0)
		out.WriteByte(0)
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))
		_ = binary.Write(&out, binary.LittleEndian, uint16(32))
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(e.png)))
		_ = binary.Write(&out, binary.LittleEndian, offset)
		offset += uint32(len(e.png))
	}
	for _, e := range entries {
		out.Write(e.png)
	}
	return out.Bytes()
}
