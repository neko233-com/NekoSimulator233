package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCaptureRateSelectionKeepsLatestChange(t *testing.T) {
	v := &viewer{rateChanged: make(chan struct{}, 1)}
	if v.currentCaptureRate() != 30 {
		t.Fatal("unexpected default rate")
	}
	for _, rate := range []int{15, 60, 120} {
		if !v.setCaptureRate(rate) {
			t.Fatalf("valid rate %d rejected", rate)
		}
	}
	if v.currentCaptureRate() != 120 || len(v.rateChanged) != 1 {
		t.Fatal("latest rate or coalesced notification lost")
	}
	if v.setCaptureRate(0) || v.setCaptureRate(61) || v.currentCaptureRate() != 120 {
		t.Fatal("invalid rate changed current setting")
	}
	<-v.rateChanged
	if !v.setCaptureRate(30) || v.currentCaptureRate() != 30 || len(v.rateChanged) != 1 {
		t.Fatal("new rate was not notified")
	}
}

func TestViewerCaptureRateControl(t *testing.T) {
	v := &viewer{done: make(chan struct{}), events: make(chan pointerEvent, 8), rateChanged: make(chan struct{}, 1)}
	tester := ui.NewTester(v.view, 1000, 650)
	if err := tester.Click("截图采样上限"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("60 FPS"); err != nil {
		t.Fatal(err)
	}
	if v.currentCaptureRate() != 60 {
		t.Fatalf("selection did not apply: %d", v.currentCaptureRate())
	}
}
