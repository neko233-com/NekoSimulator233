package main

import (
	"errors"
	"image"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/display"
)

type fakeMultiSession struct{ frames [][]display.TouchPoint }

func (*fakeMultiSession) Capture(dst *image.RGBA) (*image.RGBA, error) { return dst, nil }
func (*fakeMultiSession) TouchDown(int, int) error                     { return nil }
func (*fakeMultiSession) TouchUp() error                               { return nil }
func (*fakeMultiSession) Close() error                                 { return nil }
func (s *fakeMultiSession) SetTouches(p []display.TouchPoint) error {
	s.frames = append(s.frames, append([]display.TouchPoint(nil), p...))
	return nil
}

type flakyReleaseSession struct {
	fakeMultiSession
	keyDowns      int
	keyUps        int
	keyUpObserved chan int
	touchFailures int
}

func (s *flakyReleaseSession) KeyDown(uint16) error { s.keyDowns++; return nil }
func (s *flakyReleaseSession) KeyUp(uint16) error {
	s.keyUps++
	if s.keyUpObserved != nil {
		s.keyUpObserved <- s.keyUps
	}
	if s.keyUps == 1 {
		return errors.New("lost release response")
	}
	return nil
}
func (s *flakyReleaseSession) SetTouches(points []display.TouchPoint) error {
	s.frames = append(s.frames, append([]display.TouchPoint(nil), points...))
	if s.touchFailures > 0 {
		s.touchFailures--
		return errors.New("lost touch response")
	}
	return nil
}

func TestViewerRetriesFailedKeyReleaseWhileRunning(t *testing.T) {
	s := &flakyReleaseSession{keyUpObserved: make(chan int, 4)}
	events := make(chan pointerEvent)
	done, stopped := make(chan struct{}), make(chan struct{})
	reported := make(chan error, 1)
	go func() {
		defer close(stopped)
		runViewerInput(done, events, newViewerInput(s), func(err error) { reported <- err })
	}()
	events <- pointerEvent{kind: keyDown, usage: 4}
	events <- pointerEvent{kind: keyUp, usage: 4}
	select {
	case err := <-reported:
		if err == nil {
			t.Fatal("release failure not reported")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("release failure not reported")
	}
	for want := 1; want <= 2; want++ {
		select {
		case got := <-s.keyUpObserved:
			if got != want {
				t.Fatalf("release attempt %d, want %d", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("pending release was not retried")
		}
	}
	close(done)
	<-stopped
}

func TestViewerFlushesFailedTouchReleaseBeforeReusingContact(t *testing.T) {
	s := &flakyReleaseSession{}
	in := newViewerInput(s)
	if err := in.handle(pointerEvent{kind: mappedDown, usage: 4, x: 1, y: 2}); err != nil {
		t.Fatal(err)
	}
	s.touchFailures = 2
	if err := in.handle(pointerEvent{kind: mappedUp, usage: 4}); err == nil || !in.hasPendingReleases() {
		t.Fatal("failed release was not retained")
	}
	if err := in.handle(pointerEvent{kind: mappedDown, usage: 5, x: 3, y: 4}); err == nil {
		t.Fatal("reused a contact before releasing it")
	}
	if err := in.handle(pointerEvent{kind: mappedDown, usage: 5, x: 3, y: 4}); err != nil {
		t.Fatal(err)
	}
	if len(s.frames) != 5 || len(s.frames[1]) != 0 || len(s.frames[2]) != 0 || len(s.frames[3]) != 0 || len(s.frames[4]) != 1 || s.frames[4][0].ID != 1 {
		t.Fatalf("unexpected touch sequence: %+v", s.frames)
	}
	if in.hasPendingReleases() {
		t.Fatal("successful release remains pending")
	}
}

func TestViewerReleasesOldKeyBeforePressingItAgain(t *testing.T) {
	s := &flakyReleaseSession{}
	in := newViewerInput(s)
	if err := in.handle(pointerEvent{kind: keyDown, usage: 4}); err != nil {
		t.Fatal(err)
	}
	if err := in.handle(pointerEvent{kind: keyUp, usage: 4}); err == nil {
		t.Fatal("expected failed release")
	}
	if err := in.handle(pointerEvent{kind: keyDown, usage: 4}); err != nil || s.keyUps != 2 || s.keyDowns != 2 || !in.pressed[4] || in.hasPendingReleases() {
		t.Fatalf("re-press did not flush old release: %v", err)
	}
}

type flakyLegacyTouch struct{ ups int }

func (*flakyLegacyTouch) Capture(dst *image.RGBA) (*image.RGBA, error) { return dst, nil }
func (*flakyLegacyTouch) TouchDown(int, int) error                     { return nil }
func (*flakyLegacyTouch) Close() error                                 { return nil }
func (s *flakyLegacyTouch) TouchUp() error {
	s.ups++
	if s.ups == 1 {
		return errors.New("lost touch response")
	}
	return nil
}

func TestViewerRetriesLegacyTouchRelease(t *testing.T) {
	s := &flakyLegacyTouch{}
	in := newViewerInput(s)
	if err := in.handle(pointerEvent{kind: pointerDown}); err != nil {
		t.Fatal(err)
	}
	if err := in.handle(pointerEvent{kind: pointerUp}); err == nil || !in.touched || !in.hasPendingReleases() {
		t.Fatal("failed legacy release was lost")
	}
	if err := in.retryReleases(); err != nil || in.touched || in.hasPendingReleases() || s.ups != 2 {
		t.Fatalf("legacy release retry failed: %v", err)
	}
}

func TestViewerRetriesStickDirectionAfterFailedKeyRelease(t *testing.T) {
	s := &flakyReleaseSession{}
	in := newViewerInput(s)
	for _, usage := range []uint16{0x1a, 0x07} {
		if err := in.handle(pointerEvent{kind: stickDown, usage: usage, x: 100, y: 100, radius: 20, maxX: 200, maxY: 200}); err != nil {
			t.Fatal(err)
		}
	}
	s.touchFailures = 1
	if err := in.handle(pointerEvent{kind: stickUp, usage: 0x1a}); err == nil || !in.hasPendingReleases() {
		t.Fatal("failed direction update was not retained")
	}
	if err := in.retryReleases(); err != nil || in.hasPendingReleases() {
		t.Fatalf("direction retry failed: %v", err)
	}
	last := s.frames[len(s.frames)-1]
	if len(last) != 1 || last[0].X != 120 || last[0].Y != 100 {
		t.Fatalf("wrong direction after retry: %+v", last)
	}
}
