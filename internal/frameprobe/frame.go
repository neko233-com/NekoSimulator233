// Package frameprobe carries one bounded RGBA frame over a private process pipe.
package frameprobe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"io"
)

const magic = "NEKOFRM1"
const maxPixels = 32 * 1024 * 1024

func valid(w, h int) bool { return w > 0 && h > 0 && w <= 8192 && h <= 8192 && w*h <= maxPixels }

// Reader sends visible row data only; padding and unrelated parent image pixels stay private.
func Reader(frame *image.RGBA) (io.Reader, error) {
	if frame == nil || !valid(frame.Rect.Dx(), frame.Rect.Dy()) {
		return nil, errors.New("画面尺寸无效")
	}
	w, h := frame.Rect.Dx(), frame.Rect.Dy()
	if frame.Stride < w*4 || int64(h-1)*int64(frame.Stride)+int64(w*4) > int64(len(frame.Pix)) {
		return nil, errors.New("画面像素数据无效")
	}
	header := make([]byte, 16)
	copy(header, magic)
	binary.LittleEndian.PutUint32(header[8:], uint32(w))
	binary.LittleEndian.PutUint32(header[12:], uint32(h))
	rows := make([]io.Reader, 0, h+1)
	rows = append(rows, bytes.NewReader(header))
	for y := 0; y < h; y++ {
		rows = append(rows, bytes.NewReader(frame.Pix[y*frame.Stride:y*frame.Stride+w*4]))
	}
	return io.MultiReader(rows...), nil
}

func Read(r io.Reader) (*image.RGBA, error) {
	var header [16]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	w, h := int(binary.LittleEndian.Uint32(header[8:])), int(binary.LittleEndian.Uint32(header[12:]))
	if string(header[:8]) != magic || !valid(w, h) {
		return nil, errors.New("画面管道格式无效")
	}
	frame := image.NewRGBA(image.Rect(0, 0, w, h))
	if _, err := io.ReadFull(r, frame.Pix); err != nil {
		return nil, err
	}
	var extra [1]byte
	if n, err := io.ReadFull(r, extra[:]); n != 0 || err != io.EOF {
		return nil, errors.New("画面管道包含额外数据")
	}
	return frame, nil
}
