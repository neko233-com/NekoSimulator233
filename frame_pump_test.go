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
	if nextRequest() != nil {
		t.Fatal("second request should use the other buffer")
	}
	send(2)
	reused := nextRequest()
	if reused == nil || reused.Pix[0] != 1 {
		t.Fatal("superseded frame was not reused")
	}
	send(3)
	next := nextRequest()
	if next == nil || next == reused || next.Pix[0] != 2 {
		t.Fatal("second stale buffer not reused")
	}
	var result pumpedFrame
	select {
	case result = <-frames:
	case <-time.After(time.Second):
		t.Fatal("pump failed to deliver")
	}
	if result.err != nil || result.frame == nil || result.frame.Pix[0] != 3 {
		t.Fatalf("invalid frame: %+v", result)
	}
	value := result.frame.Pix[0]
	send(100)
	if result.frame.Pix[0] != value {
		t.Fatal("producer mutated consumer-owned frame")
	}
	available <- result.frame
	if nextRequest() != result.frame {
		t.Fatal("returned frame was not reused")
	}
}

func TestPumpFramesRetainsSingleFrameUntilConsumerIsReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &controlledFrameStream{ctx: ctx, requests: make(chan *image.RGBA), values: make(chan byte)}
	frames, _, done := pumpFrames(ctx, s)
	defer func() { cancel(); <-done }()
	<-s.requests
	s.values <- 42
	// The next read begins before the consumer accepts the first frame.
	<-s.requests
	select {
	case result := <-frames:
		if result.err != nil || result.frame == nil || result.frame.Pix[0] != 42 {
			t.Fatalf("last frame was lost: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("last frame was not retained")
	}
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
