//go:build windows && amd64

package vulkan

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// inspectHiddenSurface creates only a short-lived invisible window owned by
// this process. Its swapchain is closed before the HWND is destroyed.
// present is reserved for a short-lived probe process: if the GPU stops
// responding, the process must exit to let Windows release its resources.
func inspectHiddenSurface(h *Host, present, pixels bool) (SwapchainInfo, error) {
	var frame *image.RGBA
	if pixels {
		frame = image.NewRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				i := y*frame.Stride + x*4
				frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2], frame.Pix[i+3] = byte(x*3), byte(y*3), byte(x+y), 255
			}
		}
	}
	return inspectHiddenFrame(h, 64, 64, present, frame, false, false)
}

func inspectHiddenFrame(h *Host, width, height int, present bool, frame *image.RGBA, fit, gpuScale bool) (SwapchainInfo, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	createWindow := user32.NewProc("CreateWindowExW")
	destroyWindow := user32.NewProc("DestroyWindow")
	class, _ := windows.UTF16PtrFromString("STATIC")
	module, _, err := getModuleHandle.Call(0)
	if module == 0 {
		return SwapchainInfo{}, fmt.Errorf("获取进程模块失败：%w", err)
	}
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, uintptr(width), uintptr(height), 0, 0, module, 0)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		return SwapchainInfo{}, fmt.Errorf("创建测试窗口失败：%w", err)
	}
	destroy := true
	defer func() {
		if destroy {
			destroyWindow.Call(hwnd)
		}
	}()
	s := NewWindowRenderer(h, hwnd)
	info, err := s.Ensure()
	if err != nil {
		return SwapchainInfo{}, err
	}
	if present {
		if err := s.PresentClear([4]float32{1, 0.25, 0.5, 1}); err != nil {
			if closeErr := s.Close(); closeErr != nil {
				destroy = false
				return SwapchainInfo{}, fmt.Errorf("%v；%w", err, closeErr)
			}
			return SwapchainInfo{}, err
		}
		info.ClearSubmitted = true
	}
	if frame != nil {
		info, err = s.Ensure()
		if err != nil {
			if closeErr := s.Close(); closeErr != nil {
				destroy = false
				return SwapchainInfo{}, errors.Join(err, closeErr)
			}
			return SwapchainInfo{}, err
		}
		if int(info.Width) != width || int(info.Height) != height {
			if closeErr := s.Close(); closeErr != nil {
				destroy = false
				return SwapchainInfo{}, closeErr
			}
			return SwapchainInfo{}, fmt.Errorf("测试窗口尺寸异常：%dx%d", info.Width, info.Height)
		}
		var err error
		if fit && gpuScale {
			err = s.PresentFittedGPU(frame, true)
		} else if fit {
			err = s.PresentFittedRGBA(frame, true)
		} else {
			err = s.PresentRGBA(frame, true)
		}
		if err != nil {
			if closeErr := s.Close(); closeErr != nil {
				destroy = false
				return SwapchainInfo{}, fmt.Errorf("%v；%w", err, closeErr)
			}
			return SwapchainInfo{}, err
		}
	}
	info, err = s.Info()
	if err != nil {
		if closeErr := s.Close(); closeErr != nil {
			destroy = false
			return SwapchainInfo{}, errors.Join(err, closeErr)
		}
		return SwapchainInfo{}, err
	}
	if frame != nil && (int(info.Width) != width || int(info.Height) != height) {
		if closeErr := s.Close(); closeErr != nil {
			destroy = false
			return SwapchainInfo{}, closeErr
		}
		return SwapchainInfo{}, fmt.Errorf("测试窗口尺寸异常：%dx%d", info.Width, info.Height)
	}
	info.ClearSubmitted, info.PixelsVerified = present, frame != nil
	if err := s.Close(); err != nil {
		destroy = false
		return SwapchainInfo{}, err
	}
	return info, nil
}
