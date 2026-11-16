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

// ErrInactive is a control message indicating that no guest frame is visible.
var ErrInactive = errors.New("画面暂不可见")

func valid(w, h int) bool { return w > 0 && h > 0 && w <= 8192 && h <= 8192 && w*h <= maxPixels }

type frameReader struct {
	header      [16]byte
	headerRead  int
	frame       *image.RGBA
	row, offset int
}

func (r *frameReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := copy(p, r.header[r.headerRead:])
	r.headerRead += n
	for n < len(p) && r.row < r.frame.Rect.Dy() {
		width := r.frame.Rect.Dx() * 4
		start := r.row*r.frame.Stride + r.offset
		count := copy(p[n:], r.frame.Pix[start:r.row*r.frame.Stride+width])
		n += count
		r.offset += count
		if r.offset == width {
			r.row++
			r.offset = 0
		}
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// Reader sends visible row data only; padding and unrelated parent image pixels stay private.
func Reader(frame *image.RGBA) (io.Reader, error) {
	if frame == nil || !valid(frame.Rect.Dx(), frame.Rect.Dy()) {
		return nil, errors.New("画面尺寸无效")
	}
	w, h := frame.Rect.Dx(), frame.Rect.Dy()
	if frame.Stride < w*4 || (h > 1 && frame.Stride > (int(^uint(0)>>1)-w*4)/(h-1)) || (h-1)*frame.Stride+w*4 > len(frame.Pix) {
		return nil, errors.New("画面像素数据无效")
	}
	r := &frameReader{frame: frame}
	copy(r.header[:], magic)
	binary.LittleEndian.PutUint32(r.header[8:], uint32(w))
	binary.LittleEndian.PutUint32(r.header[12:], uint32(h))
	return r, nil
}

// InactiveReader emits a control message without guest pixels.
func InactiveReader() io.Reader {
	var header [16]byte
	copy(header[:], magic)
	return bytes.NewReader(header[:])
}

func Read(r io.Reader) (*image.RGBA, error) {
	frame, err := ReadNext(r, nil)
	if err != nil {
		return nil, err
	}
	var extra [1]byte
	if n, err := io.ReadFull(r, extra[:]); n != 0 || err != io.EOF {
		return nil, errors.New("画面管道包含额外数据")
	}
	return frame, nil
}

// ReadNext reads one self-delimiting frame from a stream, reusing dst when
// dimensions match. The caller retains exclusive ownership until returned.
func ReadNext(r io.Reader, dst *image.RGBA) (*image.RGBA, error) {
	var header [16]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	w, h := int(binary.LittleEndian.Uint32(header[8:])), int(binary.LittleEndian.Uint32(header[12:]))
	if string(header[:8]) == magic && w == 0 && h == 0 {
		return nil, ErrInactive
	}
	if string(header[:8]) != magic || !valid(w, h) {
		return nil, errors.New("画面管道格式无效")
	}
	frame := dst
	if frame == nil || frame.Rect != image.Rect(0, 0, w, h) || frame.Stride != w*4 || len(frame.Pix) != w*h*4 {
		frame = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	if _, err := io.ReadFull(r, frame.Pix); err != nil {
		return nil, err
	}
	return frame, nil
}
