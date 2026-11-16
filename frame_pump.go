package main

import (
	"context"
	"errors"
	"image"

	"github.com/neko233-com/NekoSimulator233/internal/display"
)

type pumpedFrame struct {
	frame *image.RGBA
	err   error
}

// pumpFrames keeps reading the stream while the UI is rate-limited. An
// unaccepted frame is replaced in the same buffer, so delayed presentation
// does not build a queue of old frames. At most two decoded frames are owned
// by the pump and consumer together; the consumer returns each after use.
func pumpFrames(ctx context.Context, source display.FrameStream) (<-chan pumpedFrame, chan<- *image.RGBA, <-chan struct{}) {
	out := make(chan pumpedFrame)
	available := make(chan *image.RGBA, 1)
	available <- nil
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(out)
		var buffer *image.RGBA
		for {
			frame, err := source.Next(buffer)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				select {
				case out <- pumpedFrame{err: err}:
				case <-ctx.Done():
					return
				}
				if !errors.Is(err, display.ErrDisplayInactive) {
					return
				}
				continue
			}
			if frame == nil {
				select {
				case out <- pumpedFrame{err: errors.New("画面流返回空画面")}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- pumpedFrame{frame: frame}:
				select {
				case buffer = <-available:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			default:
				buffer = frame
			}
		}
	}()
	return out, available, done
}
