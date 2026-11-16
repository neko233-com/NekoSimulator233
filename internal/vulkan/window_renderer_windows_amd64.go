//go:build windows && amd64

package vulkan

import (
	"errors"
	"image"
	"sync"
)

type renderSwapchain interface {
	Info() SwapchainInfo
	PresentClear([4]float32) error
	PresentRGBA(*image.RGBA, bool) error
	PresentFittedRGBA(*image.RGBA, bool) error
	Close() error
}

// WindowRenderer manages a window's swapchain. It does not own the HWND or
// Host; both must remain alive until Close succeeds. On a stalled GPU call,
// use a disposable renderer process so it can be terminated safely.
type WindowRenderer struct {
	mu      sync.Mutex
	size    func() (extent2D, error)
	create  func() (renderSwapchain, error)
	chain   renderSwapchain
	window  extent2D
	stale   bool
	closing bool
}

func NewWindowRenderer(h *Host, hwnd uintptr) *WindowRenderer {
	return &WindowRenderer{
		size:   func() (extent2D, error) { return windowSize(hwnd) },
		create: func() (renderSwapchain, error) { return h.CreateSwapchain(hwnd) },
	}
}

func (r *WindowRenderer) ensureLocked() error {
	if r.closing {
		return errors.New("Vulkan 窗口呈现器已关闭")
	}
	size, err := r.size()
	if err != nil {
		return err
	}
	if size.Width == 0 || size.Height == 0 {
		return errors.New("窗口当前无法呈现")
	}
	if r.chain != nil && !r.stale && size == r.window {
		return nil
	}
	if r.chain != nil {
		if err := r.chain.Close(); err != nil {
			return err
		}
		r.chain = nil
	}
	r.chain, err = r.create()
	if err != nil {
		return err
	}
	if r.chain == nil {
		return errors.New("Vulkan 交换链创建失败")
	}
	r.window, r.stale = size, false
	return nil
}

func (r *WindowRenderer) Ensure() (SwapchainInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureLocked(); err != nil {
		return SwapchainInfo{}, err
	}
	return r.chain.Info(), nil
}

// Info describes the swapchain that performed the most recent submission; it
// never creates a new one, so probe metadata cannot refer to untested images.
func (r *WindowRenderer) Info() (SwapchainInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chain == nil || r.closing || r.stale {
		return SwapchainInfo{}, errors.New("Vulkan 交换链不可用")
	}
	return r.chain.Info(), nil
}

func (r *WindowRenderer) present(fn func(renderSwapchain) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if err := r.ensureLocked(); err != nil {
			return err
		}
		err := fn(r.chain)
		if err == nil {
			return nil
		}
		r.stale = true
		if errors.Is(err, ErrPresentBusy) || (!errors.Is(err, ErrSwapchainOutOfDate) && !errors.Is(err, ErrSurfaceLost)) || attempt != 0 {
			return err
		}
	}
	return ErrSwapchainOutOfDate
}

func (r *WindowRenderer) PresentClear(color [4]float32) error {
	return r.present(func(s renderSwapchain) error { return s.PresentClear(color) })
}
func (r *WindowRenderer) PresentRGBA(frame *image.RGBA, verify bool) error {
	return r.present(func(s renderSwapchain) error { return s.PresentRGBA(frame, verify) })
}
func (r *WindowRenderer) PresentFittedRGBA(frame *image.RGBA, verify bool) error {
	return r.present(func(s renderSwapchain) error { return s.PresentFittedRGBA(frame, verify) })
}

func (r *WindowRenderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closing = true
	if r.chain == nil {
		return nil
	}
	if err := r.chain.Close(); err != nil {
		return err
	}
	r.chain = nil
	return nil
}
