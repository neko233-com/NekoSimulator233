package main

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/display"
)

type waitingFrameSource struct{ opened chan struct{} }
type waitingFrameStream struct{ ctx context.Context }

func (s *waitingFrameSource) OpenFrameStream(ctx context.Context) (display.FrameStream, error) {
	s.opened <- struct{}{}
	return &waitingFrameStream{ctx: ctx}, nil
}
func (s *waitingFrameStream) Next(*image.RGBA) (*image.RGBA, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func TestViewerStreamPauseResumeAndStop(t *testing.T) {
	v := &viewer{done: make(chan struct{})}
	source := &waitingFrameSource{opened: make(chan struct{}, 2)}
	done := make(chan bool, 1)
	go func() { done <- v.runStream(source) }()
	waitOpen := func() {
		t.Helper()
		select {
		case <-source.opened:
		case <-time.After(3 * time.Second):
			t.Fatal("stream did not open")
		}
	}
	waitOpen()
	v.hidden.Store(true)
	v.cancelStream()
	v.hidden.Store(false)
	waitOpen()
	v.stop()
	select {
	case stopped := <-done:
		if !stopped {
			t.Fatal("cancellation incorrectly fell back to polling")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop")
	}
}
