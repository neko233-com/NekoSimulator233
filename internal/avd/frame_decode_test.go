package avd

import (
	"bytes"
	"context"
	"image"
	"math/rand"
	"net"
	"strconv"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protowire"
)

type screenshotServerInterface interface{ screenshotMarker() }
type screenshotServer struct{ frame rawProto }

func (*screenshotServer) screenshotMarker() {}

func TestCaptureOverGRPCReusesFrame(t *testing.T) {
	const width, height = 512, 256
	format := appendInt(nil, 1, 1)
	format = appendInt(format, 3, width)
	format = appendInt(format, 4, height)
	pixels := make([]byte, width*height*4)
	for i := range pixels {
		pixels[i] = byte(i*37 + i/257)
	}
	message := appendBytes(nil, 1, format)
	message = appendBytes(message, 4, pixels)
	service := &screenshotServer{frame: rawProto(message)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.ForceServerCodecV2(protoCodec{}))
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "android.emulation.control.EmulatorController", HandlerType: (*screenshotServerInterface)(nil), Methods: []grpc.MethodDesc{{MethodName: "getScreenshot", Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		var request rawProto
		if err := dec(&request); err != nil {
			return nil, err
		}
		md, _ := metadata.FromIncomingContext(ctx)
		if !bytes.Equal(request, []byte{8, 1}) || len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer test-token" {
			t.Error("unexpected screenshot request or authorization")
		}
		return &srv.(*screenshotServer).frame, nil
	}}}}, service)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialController(port, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	got, err := client.Capture(frame)
	if err != nil || got != frame || !bytes.Equal(got.Pix, pixels) {
		t.Fatalf("gRPC frame mismatch or buffer not reused: %v", err)
	}
}

func TestFragmentedFrameMatchesContiguousDecoder(t *testing.T) {
	format := appendInt(nil, 1, 1)
	format = appendInt(format, 3, 2)
	format = appendInt(format, 4, 2)
	pixels := []byte{1, 2, 3, 255, 4, 5, 6, 255, 7, 8, 9, 255, 10, 11, 12, 255}
	base := appendBytes(nil, 1, format)
	base = appendBytes(base, 4, pixels)
	reversed := appendBytes(nil, 4, pixels)
	reversed = appendBytes(reversed, 1, format)
	legacy := appendBytes(nil, 1, appendInt(nil, 1, 1))
	legacy = appendInt(legacy, 2, 2)
	legacy = appendInt(legacy, 3, 2)
	legacy = appendBytes(legacy, 4, pixels)
	unknown := append([]byte(nil), base...)
	unknown = protowire.AppendTag(unknown, 99, protowire.Fixed32Type)
	unknown = protowire.AppendFixed32(unknown, 42)
	unknown = protowire.AppendTag(unknown, 100, protowire.StartGroupType)
	unknown = appendInt(unknown, 1, 123)
	unknown = protowire.AppendTag(unknown, 100, protowire.EndGroupType)
	cases := [][]byte{nil, base, reversed, legacy, unknown}
	for i := 0; i < len(base); i++ {
		cases = append(cases, append([]byte(nil), base[:i]...))
	}
	random := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		data := append([]byte(nil), base...)
		for changes := 0; changes < 1+random.Intn(4); changes++ {
			data[random.Intn(len(data))] = byte(random.Intn(256))
		}
		cases = append(cases, data)
	}
	for i, msg := range cases {
		w, h, wantPixels, referenceErr := parseImage(msg)
		for _, chunkSize := range []int{1, 3, 7, 64} {
			parts := mem.BufferSlice{mem.SliceBuffer(nil)}
			for p := 0; p < len(msg); p += chunkSize {
				parts = append(parts, mem.SliceBuffer(msg[p:min(p+chunkSize, len(msg))]))
			}
			previous := image.NewRGBA(image.Rect(0, 0, 1, 1))
			previous.Pix[0] = 99
			response := frameResponse{frame: previous}
			err := decodeFragmentedFrame(parts, &response)
			if (err == nil) != (referenceErr == nil) {
				t.Fatalf("case %d split %d: reference=%v fragmented=%v input=%x", i, chunkSize, referenceErr, err, msg)
			}
			if err != nil {
				if response.frame != previous || previous.Pix[0] != 99 {
					t.Fatalf("case %d changed the previous frame on invalid data", i)
				}
				continue
			}
			if response.frame.Bounds().Dx() != w || response.frame.Bounds().Dy() != h || !bytes.Equal(response.frame.Pix[:len(wantPixels)], wantPixels) {
				t.Fatalf("case %d split %d: decoded pixels differ", i, chunkSize)
			}
		}
	}
}

func TestFragmentedFrameReusesMatchingBuffer(t *testing.T) {
	format := appendInt(nil, 1, 1)
	format = appendInt(format, 3, 1)
	format = appendInt(format, 4, 1)
	msg := appendBytes(nil, 1, format)
	msg = appendBytes(msg, 4, []byte{9, 8, 7, 255})
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	response := frameResponse{frame: frame}
	if err := decodeFragmentedFrame(mem.BufferSlice{mem.SliceBuffer(msg[:2]), mem.SliceBuffer(msg[2:])}, &response); err != nil {
		t.Fatal(err)
	}
	if response.frame != frame || !bytes.Equal(frame.Pix, []byte{9, 8, 7, 255}) {
		t.Fatal("matching frame buffer was not reused")
	}
}
