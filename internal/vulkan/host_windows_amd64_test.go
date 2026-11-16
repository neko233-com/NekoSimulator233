//go:build windows && amd64

package vulkan

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestVulkanWin64ABI(t *testing.T) {
	checks := []struct {
		name      string
		got, want uintptr
	}{
		{"VkApplicationInfo size", unsafe.Sizeof(applicationInfo{}), 48},
		{"VkInstanceCreateInfo size", unsafe.Sizeof(instanceCreateInfo{}), 64},
		{"VkDeviceQueueCreateInfo size", unsafe.Sizeof(queueCreateInfo{}), 40},
		{"VkDeviceCreateInfo size", unsafe.Sizeof(deviceCreateInfo{}), 72},
		{"VkQueueFamilyProperties size", unsafe.Sizeof(queueFamilyProperties{}), 24},
		{"VkExtensionProperties size", unsafe.Sizeof(extensionProperties{}), 260},
		{"VkPhysicalDeviceProperties deviceName", unsafe.Offsetof(physicalProperties{}.DeviceName), 20},
		{"VkPhysicalDeviceProperties limits", unsafe.Offsetof(physicalProperties{}.Tail), 296},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestDeviceSelectionAndQueueFamily(t *testing.T) {
	family, ok := graphicsFamily([]queueFamilyProperties{{Flags: vkGraphics, Count: 0}, {Flags: 0, Count: 1}, {Flags: vkGraphics, Count: 1}})
	if !ok || family != 2 {
		t.Fatalf("graphics family %d %v", family, ok)
	}
	if _, ok = graphicsFamily([]queueFamilyProperties{{Flags: vkGraphics}}); ok {
		t.Fatal("accepted an empty queue family")
	}
	input := []candidate{{physical: 1, info: DeviceInfo{DeviceType: vkIntegratedGPU}}, {physical: 2, info: DeviceInfo{DeviceType: vkDiscreteGPU}}}
	chosen, ok := chooseDevice(input)
	if !ok || chosen.physical != 2 {
		t.Fatalf("selected %+v %v", chosen, ok)
	}
	if _, ok = chooseDevice(nil); ok {
		t.Fatal("selected missing device")
	}
}

func TestRequiredWin32SurfaceExtensions(t *testing.T) {
	if _, err := requiredInstanceExtensions(map[string]bool{"VK_KHR_surface": true}); err == nil {
		t.Fatal("missing Win32 surface extension accepted")
	}
	names, err := requiredInstanceExtensions(map[string]bool{"VK_KHR_surface": true, "VK_KHR_win32_surface": true})
	if err != nil || len(names) != 2 || names[0] != "VK_KHR_surface" || names[1] != "VK_KHR_win32_surface" {
		t.Fatalf("unexpected instance extensions %v: %v", names, err)
	}
}

func TestEnumerateDevicesRetriesIncomplete(t *testing.T) {
	attempt := 0
	proc := windows.NewCallback(func(_ uintptr, count *uint32, data *uintptr) uintptr {
		if data == nil {
			*count = 2
			return 0
		}
		attempt++
		if attempt == 1 {
			*count = 2
			return vkIncomplete
		}
		items := unsafe.Slice(data, int(*count))
		items[0], items[1] = 11, 22
		return 0
	})
	items, err := enumerateDevices(proc, 1)
	if err != nil || attempt != 2 || len(items) != 2 || items[0] != 11 || items[1] != 22 {
		t.Fatalf("enumeration %v, attempts %d: %v", items, attempt, err)
	}
}

func TestCloseDestroysObjectsOnceInOrder(t *testing.T) {
	var calls []string
	h := &Host{device: 2, instance: 1}
	h.destroyDevice = windows.NewCallback(func(device, allocator uintptr) uintptr {
		calls = append(calls, "device")
		if device != 2 || allocator != 0 {
			t.Error("invalid device cleanup")
		}
		return 0
	})
	h.destroyInstance = windows.NewCallback(func(instance, allocator uintptr) uintptr {
		calls = append(calls, "instance")
		if instance != 1 || allocator != 0 {
			t.Error("invalid instance cleanup")
		}
		return 0
	})
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "device" || calls[1] != "instance" {
		t.Fatalf("cleanup order %v", calls)
	}
}

// Opt in only after checking host health: this loads the installed Vulkan
// driver and creates a real logical device, but never starts an emulator.
func TestLiveHostVulkan(t *testing.T) {
	if os.Getenv("NEKO_TEST_HOST_VULKAN") != "1" {
		t.Skip("set NEKO_TEST_HOST_VULKAN=1 for a host driver test")
	}
	h, err := OpenHost()
	if err != nil {
		t.Fatal(err)
	}
	info := h.Info()
	if info.Name == "" || !info.Swapchain {
		t.Fatalf("incomplete hardware device: %+v", info)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	t.Logf("created host Vulkan graphics device: %+v", info)
}
