//go:build windows && amd64

package vulkan

import (
	"errors"
	"image"
	"testing"
)

type fakeRenderChain struct {
	info             SwapchainInfo
	presentErr       error
	closeErr         error
	presents, closes int
}

func (s *fakeRenderChain) Info() SwapchainInfo                 { return s.info }
func (s *fakeRenderChain) PresentClear([4]float32) error       { s.presents++; return s.presentErr }
func (s *fakeRenderChain) PresentRGBA(*image.RGBA, bool) error { s.presents++; return s.presentErr }
func (s *fakeRenderChain) PresentFittedRGBA(*image.RGBA, bool) error {
	s.presents++
	return s.presentErr
}
func (s *fakeRenderChain) PresentFittedGPU(*image.RGBA, bool) error {
	s.presents++
	return s.presentErr
}
func (s *fakeRenderChain) Close() error { s.closes++; return s.closeErr }

func TestWindowRendererResizeAndBusySafety(t *testing.T) {
	size := extent2D{640, 480}
	var chains []*fakeRenderChain
	r := &WindowRenderer{
		size: func() (extent2D, error) { return size, nil },
		create: func() (renderSwapchain, error) {
			s := &fakeRenderChain{info: SwapchainInfo{Width: size.Width, Height: size.Height}}
			chains = append(chains, s)
			return s, nil
		},
	}
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := r.PresentFittedRGBA(frame, false); err != nil {
		t.Fatal(err)
	}
	if err := r.PresentFittedRGBA(frame, false); err != nil || len(chains) != 1 {
		t.Fatal("unchanged window recreated swapchain")
	}
	size = extent2D{800, 600}
	chains[0].closeErr = ErrPresentBusy
	if err := r.PresentFittedRGBA(frame, false); !errors.Is(err, ErrPresentBusy) || len(chains) != 1 {
		t.Fatal("recreated while GPU busy")
	}
	chains[0].closeErr = nil
	if err := r.PresentFittedRGBA(frame, false); err != nil || len(chains) != 2 || chains[1].info.Width != 800 {
		t.Fatalf("resize failed: %v", err)
	}
	size = extent2D{}
	if err := r.PresentFittedRGBA(frame, false); err == nil || chains[1].closes != 0 {
		t.Fatal("invalid window closed active swapchain")
	}
	size = extent2D{800, 600}
	chains[1].closeErr = ErrPresentBusy
	if err := r.Close(); !errors.Is(err, ErrPresentBusy) {
		t.Fatal("busy close accepted")
	}
	if err := r.PresentFittedRGBA(frame, false); err == nil {
		t.Fatal("closed renderer accepted new work")
	}
	chains[1].closeErr = nil
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowRendererRetriesOutOfDateOnlyOnce(t *testing.T) {
	for _, cause := range []error{ErrSwapchainOutOfDate, ErrSurfaceLost} {
		var chains []*fakeRenderChain
		r := &WindowRenderer{size: func() (extent2D, error) { return extent2D{2, 2}, nil }, create: func() (renderSwapchain, error) {
			s := &fakeRenderChain{}
			if len(chains) == 0 {
				s.presentErr = cause
			}
			chains = append(chains, s)
			return s, nil
		}}
		if err := r.PresentClear([4]float32{}); err != nil || len(chains) != 2 || chains[0].closes != 1 || chains[1].presents != 1 {
			t.Fatalf("rebuild failed: %v %+v", err, chains)
		}
	}
	created := 0
	r := &WindowRenderer{size: func() (extent2D, error) { return extent2D{2, 2}, nil }, create: func() (renderSwapchain, error) {
		created++
		return &fakeRenderChain{presentErr: ErrSwapchainOutOfDate}, nil
	}}
	if err := r.PresentClear([4]float32{}); !errors.Is(err, ErrSwapchainOutOfDate) || created != 2 {
		t.Fatalf("unbounded retry: %v %d", err, created)
	}
}

func TestWindowRendererPreservesBusySubmission(t *testing.T) {
	first := &fakeRenderChain{presentErr: ErrPresentBusy, closeErr: ErrPresentBusy}
	created := 0
	r := &WindowRenderer{size: func() (extent2D, error) { return extent2D{2, 2}, nil }, create: func() (renderSwapchain, error) {
		created++
		if created == 1 {
			return first, nil
		}
		return &fakeRenderChain{}, nil
	}}
	if err := r.PresentClear([4]float32{}); !errors.Is(err, ErrPresentBusy) || first.closes != 0 {
		t.Fatal("busy submission was destroyed")
	}
	if err := r.PresentClear([4]float32{}); !errors.Is(err, ErrPresentBusy) || created != 1 {
		t.Fatal("busy submission was replaced")
	}
	first.closeErr = nil
	if err := r.PresentClear([4]float32{}); err != nil || created != 2 {
		t.Fatalf("completed submission not recovered: %v", err)
	}
}
