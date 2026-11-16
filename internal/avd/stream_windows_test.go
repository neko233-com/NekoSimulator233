package avd

import (
	"bytes"
	"context"
	"errors"
	"image"
	"net"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type streamTestService interface{ streamTestMarker() }
type fakeStreamService struct{ finished chan struct{} }

func (*fakeStreamService) streamTestMarker() {}

func TestFrameStreamProtocolAndCancellation(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &fakeStreamService{finished: make(chan struct{})}
	server := grpc.NewServer(grpc.ForceServerCodecV2(protoCodec{}))
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "android.emulation.control.EmulatorController", HandlerType: (*streamTestService)(nil), Streams: []grpc.StreamDesc{{StreamName: "streamScreenshot", ServerStreams: true, Handler: func(srv any, stream grpc.ServerStream) error {
		defer close(srv.(*fakeStreamService).finished)
		var request rawProto
		if err := stream.RecvMsg(&request); err != nil {
			return err
		}
		md, _ := metadata.FromIncomingContext(stream.Context())
		if !bytes.Equal(request, []byte{8, 1}) || len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer test-token" {
			t.Error("unexpected stream request or authorization")
		}
		empty := rawProto(nil)
		if err := stream.SendMsg(&empty); err != nil {
			return err
		}
		format := appendInt(nil, 1, 1)
		format = appendInt(format, 3, 2)
		format = appendInt(format, 4, 1)
		message := rawProto(appendBytes(appendBytes(nil, 1, format), 4, []byte{1, 2, 3, 255, 4, 5, 6, 255}))
		if err := stream.SendMsg(&message); err != nil {
			return err
		}
		if err := stream.SendMsg(&empty); err != nil {
			return err
		}
		<-stream.Context().Done()
		return stream.Context().Err()
	}}}}, service)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	c, err := DialController(listener.Addr().(*net.TCPAddr).Port, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := c.OpenFrameStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Next(nil); !errors.Is(err, display.ErrDisplayInactive) {
		t.Fatalf("empty display: %v", err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 2, 1))
	got, err := stream.Next(frame)
	if err != nil || got != frame || !bytes.Equal(got.Pix, []byte{1, 2, 3, 255, 4, 5, 6, 255}) {
		t.Fatalf("frame mismatch or buffer not reused: %v", err)
	}
	if c.width != 2 || c.height != 1 {
		t.Fatal("stream did not update touch dimensions")
	}
	if _, err := stream.Next(nil); !errors.Is(err, display.ErrDisplayInactive) {
		t.Fatalf("display became inactive: %v", err)
	}
	if c.width != 0 || c.height != 0 {
		t.Fatal("inactive display retained touch dimensions")
	}
	done := make(chan error, 1)
	go func() { _, err := stream.Next(nil); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled stream returned a frame")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled read did not stop")
	}
	select {
	case <-service.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("server stream was not canceled")
	}
}
