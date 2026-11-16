package main

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

type controlledFrameStream struct {
	ctx      context.Context
	requests chan *image.RGBA
	values   chan byte
}

func (s *controlledFrameStream) Next(dst *image.RGBA) (*image.RGBA, error) {
	select {
	case s.requests <- dst:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	var value byte
	select {
	case value = <-s.values:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	if dst == nil {
		dst = image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	dst.Pix[0] = value
	return dst, nil
}

func TestPumpFramesDropsStaleFramesAndKeepsConsumerBuffer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &controlledFrameStream{ctx: ctx, requests: make(chan *image.RGBA), values: make(chan byte)}
	frames, available, done := pumpFrames(ctx, s)
	defer func() { cancel(); <-done }()
	nextRequest := func() *image.RGBA {
		t.Helper()
		select {
		case p := <-s.requests:
			return p
		case <-time.After(time.Second):
			t.Fatal("frame request timed out")
			return nil
		}
	}
	send := func(value byte) {
		t.Helper()
		select {
		case s.values <- value:
		case <-time.After(time.Second):
			t.Fatal("frame delivery timed out")
		}
	}
	if nextRequest() != nil {
		t.Fatal("first request did not start empty")
	}
	send(1)
	reused := nextRequest()
	if reused == nil || reused.Pix[0] != 1 {
		t.Fatal("first discarded frame not reused")
	}
	send(2)
	if got := nextRequest(); got != reused || got.Pix[0] != 2 {
		t.Fatal("stale buffer not reused")
	}
	nextEmpty := make(chan struct{})
	go func() {
		for value := byte(3); ; value++ {
			select {
			case s.values <- value:
			case <-ctx.Done():
				return
			}
			select {
			case next := <-s.requests:
				if next == nil {
					close(nextEmpty)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	var result pumpedFrame
	select {
	case result = <-frames:
	case <-time.After(time.Second):
		t.Fatal("pump failed to deliver")
	}
	if result.err != nil || result.frame == nil {
		t.Fatalf("invalid frame: %+v", result)
	}
	value := result.frame.Pix[0]
	select {
	case <-nextEmpty:
	case <-time.After(time.Second):
		t.Fatal("consumer-owned frame was reused")
	}
	send(100)
	if nextRequest() == result.frame || result.frame.Pix[0] != value {
		t.Fatal("producer mutated consumer-owned frame")
	}
	available <- result.frame
}

type failedFrameStream struct{}

func (failedFrameStream) Next(*image.RGBA) (*image.RGBA, error) {
	return nil, errors.New("stream failed")
}

func TestPumpFramesReportsTerminalError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames, _, done := pumpFrames(ctx, failedFrameStream{})
	select {
	case got := <-frames:
		if got.err == nil || got.err.Error() != "stream failed" {
			t.Fatal(got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("missing stream failure")
	}
	<-done
}
