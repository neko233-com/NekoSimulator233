//go:build windows && amd64

package vulkan

import (
	"math"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSwapchainWin64ABI(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"VkWin32SurfaceCreateInfoKHR", unsafe.Sizeof(win32SurfaceCreateInfo{}), 40},
		{"VkSurfaceCapabilitiesKHR", unsafe.Sizeof(surfaceCapabilities{}), 52},
		{"VkSurfaceFormatKHR", unsafe.Sizeof(surfaceFormat{}), 8},
		{"VkSwapchainCreateInfoKHR", unsafe.Sizeof(swapchainCreateInfo{}), 104},
		{"swapchain surface offset", unsafe.Offsetof(swapchainCreateInfo{}.Surface), 24},
		{"swapchain queues offset", unsafe.Offsetof(swapchainCreateInfo{}.QueueFamilyIndices), 72},
		{"old swapchain offset", unsafe.Offsetof(swapchainCreateInfo{}.OldSwapchain), 96},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestSwapchainSettings(t *testing.T) {
	c := surfaceCapabilities{MinImageCount: 2, MaxImageCount: 3, CurrentExtent: extent2D{math.MaxUint32, math.MaxUint32}, MinExtent: extent2D{64, 64}, MaxExtent: extent2D{1920, 1080}, MaxImageArrayLayers: 1, SupportedTransforms: 1, CurrentTransform: 1, SupportedCompositeAlpha: 1, SupportedUsageFlags: vkUsageTransferDst}
	count, e, alpha, err := selectSwapchainSettings(c, extent2D{40, 2000})
	if err != nil || count != 3 || e != (extent2D{64, 1080}) || alpha != 1 {
		t.Fatalf("settings %d %+v %d: %v", count, e, alpha, err)
	}
	c.CurrentExtent = extent2D{0, 0}
	if _, _, _, err := selectSwapchainSettings(c, extent2D{64, 64}); err == nil {
		t.Fatal("accepted an unavailable window")
	}
	c.CurrentExtent = extent2D{800, 600}
	c.SupportedUsageFlags = 0
	if _, _, _, err := selectSwapchainSettings(c, extent2D{64, 64}); err == nil {
		t.Fatal("accepted unsupported transfer usage")
	}
	got, err := chooseSurfaceFormat([]surfaceFormat{{vkFormatRGBA8, 0}, {vkFormatBGRA8, 0}})
	if err != nil || got.Format != vkFormatBGRA8 {
		t.Fatalf("surface format %+v: %v", got, err)
	}
	got, err = chooseSurfaceFormat([]surfaceFormat{{vkFormatUndefined, 0}})
	if err != nil || got.Format != vkFormatBGRA8 {
		t.Fatalf("undefined surface format %+v: %v", got, err)
	}
	if _, err = chooseSurfaceFormat([]surfaceFormat{{999, 0}}); err == nil {
		t.Fatal("accepted unsupported color format")
	}
	got, err = chooseSurfaceFormat([]surfaceFormat{{vkFormatRGBA8SRGB, 0}})
	if err != nil || got.Format != vkFormatRGBA8SRGB {
		t.Fatal("supported SRGB format rejected")
	}
}

func TestHostCloseDestroysSwapchainBeforeDevice(t *testing.T) {
	var calls []string
	h := &Host{instance: 1, device: 2, swapchains: make(map[*Swapchain]struct{})}
	s := &Swapchain{host: h, surface: 3, swapchain: 4}
	s.destroySwapchain = windows.NewCallback(func(device, chain, allocator uintptr) uintptr {
		calls = append(calls, "swapchain")
		if device != 2 || chain != 4 || allocator != 0 {
			t.Error("wrong swapchain cleanup arguments")
		}
		return 0
	})
	s.destroySurface = windows.NewCallback(func(instance, surface, allocator uintptr) uintptr {
		calls = append(calls, "surface")
		if instance != 1 || surface != 3 || allocator != 0 {
			t.Error("wrong surface cleanup arguments")
		}
		return 0
	})
	h.destroyDevice = windows.NewCallback(func(device, allocator uintptr) uintptr { calls = append(calls, "device"); return 0 })
	h.destroyInstance = windows.NewCallback(func(instance, allocator uintptr) uintptr { calls = append(calls, "instance"); return 0 })
	h.swapchains[s] = struct{}{}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{"swapchain", "surface", "device", "instance"}
	if len(calls) != len(want) {
		t.Fatalf("cleanup order %v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("cleanup order %v", calls)
		}
	}
	if _, err := windowSize(0); err == nil {
		t.Fatal("accepted null window")
	}
}

func TestLiveSwapchainOnOwnedHiddenWindow(t *testing.T) {
	if os.Getenv("NEKO_TEST_HOST_VULKAN") != "1" {
		t.Skip("set NEKO_TEST_HOST_VULKAN=1 to create an isolated hidden test window and swapchain")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	createWindow := user32.NewProc("CreateWindowExW")
	destroyWindow := user32.NewProc("DestroyWindow")
	class, _ := windows.UTF16PtrFromString("STATIC")
	module, _, err := getModuleHandle.Call(0)
	if module == 0 {
		t.Fatal(err)
	}
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 64, 64, 0, 0, module, 0)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		t.Fatalf("create hidden window: %v", err)
	}
	defer destroyWindow.Call(hwnd)
	h, err := OpenHost()
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	probe, err := inspectHiddenSurface(h)
	if err != nil || probe.Width == 0 || probe.Height == 0 || probe.ImageCount == 0 {
		t.Fatalf("hidden surface probe %+v: %v", probe, err)
	}
	s, err := h.CreateSwapchain(hwnd)
	if err != nil {
		t.Fatal(err)
	}
	if s.extent.Width == 0 || s.extent.Height == 0 || len(s.images) == 0 {
		t.Fatal("swapchain has no image or extent")
	}
	if _, err = h.CreateSwapchain(hwnd); err == nil {
		t.Fatal("created duplicate swapchain for window")
	}
	t.Logf("created hidden swapchain: %dx%d images=%d format=%d", s.extent.Width, s.extent.Height, len(s.images), s.format.Format)
	if err = h.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}
