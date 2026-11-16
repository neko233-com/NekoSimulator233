package avd

import (
	"errors"
	"image"

	"google.golang.org/grpc/mem"
	"google.golang.org/protobuf/encoding/protowire"
)

var errFrameWire = errors.New("模拟器画面响应格式无效")

// frameWire reads immutable gRPC fragments without concatenating them. The
// codec consumes the pixels before gRPC releases the input buffers.
type frameWire struct {
	data      mem.BufferSlice
	index     int
	offset    int
	remaining int
}

func (r *frameWire) readByte() (byte, error) {
	if r.remaining == 0 {
		return 0, errFrameWire
	}
	for r.index < len(r.data) {
		buf := r.data[r.index].ReadOnlyData()
		if r.offset < len(buf) {
			b := buf[r.offset]
			r.offset++
			r.remaining--
			return b, nil
		}
		r.index++
		r.offset = 0
	}
	return 0, errFrameWire
}

func (r *frameWire) varint() (uint64, error) {
	var value uint64
	for i := 0; i < 10; i++ {
		b, err := r.readByte()
		if err != nil || i == 9 && b > 1 {
			return 0, errFrameWire
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return value, nil
		}
	}
	return 0, errFrameWire
}

func (r *frameWire) tag() (protowire.Number, protowire.Type, error) {
	v, err := r.varint()
	if err != nil {
		return 0, 0, err
	}
	n, typ := protowire.DecodeTag(v)
	if n < protowire.MinValidNumber {
		return 0, 0, errFrameWire
	}
	return n, typ, nil
}

func (r *frameWire) skip(n int) error {
	if n < 0 || n > r.remaining {
		return errFrameWire
	}
	for n > 0 {
		if r.index >= len(r.data) {
			return errFrameWire
		}
		buf := r.data[r.index].ReadOnlyData()
		if r.offset == len(buf) {
			r.index++
			r.offset = 0
			continue
		}
		step := min(n, len(buf)-r.offset)
		r.offset += step
		r.remaining -= step
		n -= step
	}
	return nil
}

func (r *frameWire) bytesLen() (int, error) {
	n, err := r.varint()
	if err != nil || n > uint64(r.remaining) {
		return 0, errFrameWire
	}
	return int(n), nil
}

func (r *frameWire) skipField(num protowire.Number, typ protowire.Type, depth int) error {
	switch typ {
	case protowire.VarintType:
		_, err := r.varint()
		return err
	case protowire.Fixed32Type:
		return r.skip(4)
	case protowire.Fixed64Type:
		return r.skip(8)
	case protowire.BytesType:
		n, err := r.bytesLen()
		if err != nil {
			return err
		}
		return r.skip(n)
	case protowire.StartGroupType:
		if depth <= 0 {
			return errFrameWire
		}
		for r.remaining > 0 {
			field, kind, err := r.tag()
			if err != nil {
				return err
			}
			if kind == protowire.EndGroupType {
				if field != num {
					return errFrameWire
				}
				return nil
			}
			if err := r.skipField(field, kind, depth-1); err != nil {
				return err
			}
		}
	}
	return errFrameWire
}

func (r *frameWire) copyTo(dst []byte) error {
	if len(dst) > r.remaining {
		return errFrameWire
	}
	for len(dst) > 0 {
		if r.index >= len(r.data) {
			return errFrameWire
		}
		buf := r.data[r.index].ReadOnlyData()
		if r.offset == len(buf) {
			r.index++
			r.offset = 0
			continue
		}
		n := copy(dst, buf[r.offset:])
		dst = dst[n:]
		r.offset += n
		r.remaining -= n
	}
	return nil
}

func decodeFragmentedFrame(data mem.BufferSlice, response *frameResponse) error {
	r := frameWire{data: data, remaining: data.Len()}
	total := r.remaining
	var width, height, oldWidth, oldHeight, format uint64
	pixelOffset, pixelSize := 0, 0
	for r.remaining > 0 {
		n, typ, err := r.tag()
		if err != nil {
			return err
		}
		switch {
		case n == 1 && typ == protowire.BytesType:
			size, err := r.bytesLen()
			if err != nil {
				return err
			}
			sub := r
			sub.remaining = size
			for sub.remaining > 0 {
				field, kind, err := sub.tag()
				if err != nil {
					return err
				}
				if (field == 1 || field == 3 || field == 4) && kind == protowire.VarintType {
					v, err := sub.varint()
					if err != nil {
						return err
					}
					switch field {
					case 1:
						format = v
					case 3:
						width = v
					case 4:
						height = v
					}
				} else if err := sub.skipField(field, kind, protowire.DefaultRecursionLimit); err != nil {
					return err
				}
			}
			if err := r.skip(size); err != nil {
				return err
			}
		case (n == 2 || n == 3) && typ == protowire.VarintType:
			v, err := r.varint()
			if err != nil {
				return err
			}
			if n == 2 {
				oldWidth = v
			} else {
				oldHeight = v
			}
		case n == 4 && typ == protowire.BytesType:
			size, err := r.bytesLen()
			if err != nil {
				return err
			}
			pixelOffset, pixelSize = total-r.remaining, size
			if err := r.skip(size); err != nil {
				return err
			}
		default:
			if err := r.skipField(n, typ, protowire.DefaultRecursionLimit); err != nil {
				return err
			}
		}
	}
	if width == 0 {
		width = oldWidth
	}
	if height == 0 {
		height = oldHeight
	}
	if format != 1 || width == 0 || height == 0 || width > 8192 || height > 8192 || width*height > 32*1024*1024 || uint64(pixelSize) != width*height*4 {
		return errors.New("模拟器画面尺寸或像素数据无效")
	}
	rect := image.Rect(0, 0, int(width), int(height))
	if response.frame == nil || response.frame.Rect != rect || response.frame.Stride != int(width)*4 || len(response.frame.Pix) < pixelSize {
		response.frame = image.NewRGBA(rect)
	}
	pixels := frameWire{data: data, remaining: total}
	if err := pixels.skip(pixelOffset); err != nil {
		return err
	}
	return pixels.copyTo(response.frame.Pix[:pixelSize])
}
