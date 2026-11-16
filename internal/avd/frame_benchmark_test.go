package avd

import (
	"image"
	"testing"

	"google.golang.org/grpc/mem"
)

func BenchmarkFragmentedFrameDecode(b *testing.B) {
	const w, h = 720, 1280
	format := appendInt(nil, 1, 1)
	format = appendInt(format, 3, w)
	format = appendInt(format, 4, h)
	message := appendBytes(nil, 1, format)
	message = appendBytes(message, 4, make([]byte, w*h*4))
	var chunks mem.BufferSlice
	for len(message) != 0 {
		n := min(len(message), 32<<10)
		chunks = append(chunks, mem.SliceBuffer(message[:n]))
		message = message[n:]
	}
	frame := frameResponse{frame: image.NewRGBA(image.Rect(0, 0, w, h))}
	b.ReportAllocs()
	b.SetBytes(w * h * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := (protoCodec{}).Unmarshal(chunks, &frame); err != nil {
			b.Fatal(err)
		}
	}
}
