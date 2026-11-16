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

// pumpFrames retains the most recent frame while reading the next one, so a
// final frame is not lost if the guest stops producing updates. Two buffers
// are shared by the reader, the pending frame, and the consumer.
func pumpFrames(ctx context.Context, source display.FrameStream) (<-chan pumpedFrame, chan<- *image.RGBA, <-chan struct{}) {
	out := make(chan pumpedFrame)
	available := make(chan *image.RGBA, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(out)
		ctx, cancel := context.WithCancel(ctx)
		requests := make(chan *image.RGBA)
		type readResult struct {
			frame, buffer *image.RGBA
			err           error
		}
		results := make(chan readResult)
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			for {
				select {
				case <-ctx.Done():
					return
				case buffer := <-requests:
					frame, err := source.Next(buffer)
					select {
					case results <- readResult{frame: frame, buffer: buffer, err: err}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-workerDone }()
		var free [2]*image.RGBA
		freeCount := len(free)
		var pending *image.RGBA
		var pendingErr error
		reading := false
		for {
			var request chan<- *image.RGBA
			var buffer *image.RGBA
			if !reading && pendingErr == nil && freeCount > 0 {
				request, buffer = requests, free[freeCount-1]
			}
			var deliver chan<- pumpedFrame
			var item pumpedFrame
			if pending != nil {
				deliver, item = out, pumpedFrame{frame: pending}
			} else if pendingErr != nil {
				deliver, item = out, pumpedFrame{err: pendingErr}
			}
			select {
			case <-ctx.Done():
				return
			case request <- buffer:
				freeCount--
				free[freeCount] = nil
				reading = true
			case buffer := <-available:
				free[freeCount] = buffer
				freeCount++
			case result := <-results:
				reading = false
				if result.err != nil || result.frame == nil {
					free[freeCount] = result.buffer
					freeCount++
					pendingErr = result.err
					if pendingErr == nil {
						pendingErr = errors.New("画面流返回空画面")
					}
					if errors.Is(pendingErr, display.ErrDisplayInactive) && pending != nil {
						free[freeCount] = pending
						freeCount++
						pending = nil
					}
				} else {
					if pending != nil {
						free[freeCount] = pending
						freeCount++
					}
					pending = result.frame
				}
			case deliver <- item:
				if item.frame != nil {
					pending = nil
				} else if errors.Is(item.err, display.ErrDisplayInactive) {
					pendingErr = nil
				} else {
					return
				}
			}
		}
	}()
	return out, available, done
}
