//go:build windows && amd64

package vulkan

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// inspectHiddenSurface creates only a short-lived invisible window owned by
// this process. Its swapchain is closed before the HWND is destroyed.
func inspectHiddenSurface(h *Host) (SwapchainInfo, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	createWindow := user32.NewProc("CreateWindowExW")
	destroyWindow := user32.NewProc("DestroyWindow")
	class, _ := windows.UTF16PtrFromString("STATIC")
	module, _, err := getModuleHandle.Call(0)
	if module == 0 {
		return SwapchainInfo{}, fmt.Errorf("获取进程模块失败：%w", err)
	}
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 64, 64, 0, 0, module, 0)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		return SwapchainInfo{}, fmt.Errorf("创建测试窗口失败：%w", err)
	}
	defer destroyWindow.Call(hwnd)
	s, err := h.CreateSwapchain(hwnd)
	if err != nil {
		return SwapchainInfo{}, err
	}
	info := s.Info()
	if err := s.Close(); err != nil {
		return SwapchainInfo{}, err
	}
	return info, nil
}
