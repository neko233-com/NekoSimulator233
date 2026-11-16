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

func TestReadNextReusesBufferAndRejectsTruncation(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 1))
	b := image.NewRGBA(image.Rect(0, 0, 2, 1))
	a.Pix[0], b.Pix[0] = 11, 22
	ar, _ := Reader(a)
	br, _ := Reader(b)
	stream := io.MultiReader(ar, br)
	first, err := ReadNext(stream, nil)
	if err != nil || first.Pix[0] != 11 {
		t.Fatalf("first frame: %v", err)
	}
	second, err := ReadNext(stream, first)
	if err != nil || second != first || second.Pix[0] != 22 {
		t.Fatalf("frame reuse: %v", err)
	}
	if _, err = ReadNext(stream, second); err != io.EOF {
		t.Fatalf("stream end: %v", err)
	}
	if _, err = Reader(&image.RGBA{Rect: image.Rect(0, 0, 1, 3), Stride: int(^uint(0) >> 1), Pix: make([]byte, 4)}); err == nil {
		t.Fatal("accepted overflowing stride")
	}
}
