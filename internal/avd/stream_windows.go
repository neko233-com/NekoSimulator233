package avd

import (
	"context"
	"fmt"
	"image"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type frameStream struct {
	controller *Controller
	stream     grpc.ClientStream
}

// OpenFrameStream is an optional event-driven path. Cancel ctx to stop it,
// including when the guest is inactive and no more frames arrive.
func (c *Controller) OpenFrameStream(ctx context.Context) (display.FrameStream, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
	stream, err := c.conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, controller+"streamScreenshot")
	if err != nil {
		return nil, fmt.Errorf("打开画面流失败：%w", err)
	}
	request := rawProto(appendInt(nil, 1, 1))
	if err = stream.SendMsg(&request); err != nil {
		return nil, fmt.Errorf("请求画面流失败：%w", err)
	}
	if err = stream.CloseSend(); err != nil {
		return nil, fmt.Errorf("请求画面流失败：%w", err)
	}
	return &frameStream{controller: c, stream: stream}, nil
}

func (s *frameStream) Next(dst *image.RGBA) (*image.RGBA, error) {
	response := frameResponse{frame: dst, allowEmpty: true}
	if err := s.stream.RecvMsg(&response); err != nil {
		return nil, fmt.Errorf("读取画面流失败：%w", err)
	}
	s.controller.mu.Lock()
	defer s.controller.mu.Unlock()
	if response.empty {
		s.controller.width, s.controller.height = 0, 0
		return nil, display.ErrDisplayInactive
	}
	s.controller.width, s.controller.height = response.frame.Bounds().Dx(), response.frame.Bounds().Dy()
	return response.frame, nil
}
