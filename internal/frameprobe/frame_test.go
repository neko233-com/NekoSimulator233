package frameprobe

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
	"testing"
)

func TestRoundTripSubimageAndRejectMalformed(t *testing.T) {
	parent := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range parent.Pix {
		parent.Pix[i] = byte(i)
	}
	frame := parent.SubImage(image.Rect(1, 1, 3, 3)).(*image.RGBA)
	r, err := Reader(frame)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte{}, frame.Pix[:8]...), frame.Pix[frame.Stride:frame.Stride+8]...)
	if !bytes.Equal(got.Pix, want) || len(data) != 16+len(want) {
		t.Fatal("incorrect pixels or padding leaked")
	}
	badSize := append([]byte{}, data...)
	binary.LittleEndian.PutUint32(badSize[8:], 8193)
	for _, invalid := range [][]byte{nil, data[:15], data[:len(data)-1], append(append([]byte{}, data...), 0), badSize} {
		if _, err := Read(bytes.NewReader(invalid)); err == nil {
			t.Fatal("accepted malformed frame")
		}
	}
	if _, err := Reader(&image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 8)}); err == nil {
		t.Fatal("accepted truncated image")
	}
}
