//go:build windows && amd64

package vulkan

import (
	"errors"
	"math"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPresentWin64ABI(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"VkFenceCreateInfo", unsafe.Sizeof(fenceCreateInfo{}), 24},
		{"VkCommandPoolCreateInfo", unsafe.Sizeof(commandPoolCreateInfo{}), 24},
		{"VkCommandBufferAllocateInfo", unsafe.Sizeof(commandBufferAllocateInfo{}), 32},
		{"VkCommandBufferBeginInfo", unsafe.Sizeof(commandBufferBeginInfo{}), 32},
		{"VkImageMemoryBarrier", unsafe.Sizeof(imageMemoryBarrier{}), 72},
		{"VkSubmitInfo", unsafe.Sizeof(submitInfo{}), 72},
		{"VkSwapchainPresentFenceInfoEXT", unsafe.Sizeof(presentFenceInfo{}), 32},
		{"VkPresentInfoKHR", unsafe.Sizeof(presentInfo{}), 64},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestPresentResourcesStayAliveUntilFenceSignals(t *testing.T) {
	ready := false
	var destroyed []string
	h := &Host{device: 1}
	op := &presentResources{pool: 2, finished: 3, acquireFence: 4, renderFence: 5, presentFence: 6, presentPending: true}
	op.procs.fenceStatus = windows.NewCallback(func(_, _ uintptr) uintptr {
		if ready {
			return vkSuccess
		}
		return vkNotReady
	})
	op.procs.destroyPool = windows.NewCallback(func(_, _, _ uintptr) uintptr { destroyed = append(destroyed, "pool"); return 0 })
	op.procs.destroySemaphore = windows.NewCallback(func(_, _, _ uintptr) uintptr { destroyed = append(destroyed, "semaphore"); return 0 })
	op.procs.destroyFence = windows.NewCallback(func(_, _, _ uintptr) uintptr { destroyed = append(destroyed, "fence"); return 0 })
	if err := op.releaseIfSafe(h); !errors.Is(err, ErrPresentBusy) || len(destroyed) != 0 {
		t.Fatalf("pending work released: %v %v", err, destroyed)
	}
	ready = true
	if err := op.releaseIfSafe(h); err != nil || len(destroyed) != 5 {
		t.Fatalf("completed work not released: %v %v", err, destroyed)
	}
	if err := op.releaseIfSafe(h); err != nil || len(destroyed) != 5 {
		t.Fatal("resources released twice")
	}
}

func TestPresentRejectsInvalidColorWithoutDevice(t *testing.T) {
	s := &Swapchain{}
	for _, color := range [][4]float32{{-1, 0, 0, 1}, {0, 2, 0, 1}, {float32(math.NaN()), 0, 0, 1}} {
		if err := s.PresentClear(color); err == nil {
			t.Fatalf("invalid color accepted: %v", color)
		}
	}
}

func TestBusyPresentationBlocksHostDestruction(t *testing.T) {
	ready := false
	var calls []string
	h := &Host{device: 1, instance: 2, swapchains: make(map[*Swapchain]struct{})}
	s := &Swapchain{host: h, swapchain: 3, surface: 4}
	op := &presentResources{presentFence: 5, presentPending: true}
	op.procs.fenceStatus = windows.NewCallback(func(_, _ uintptr) uintptr {
		if ready {
			return vkSuccess
		}
		return vkNotReady
	})
	op.procs.destroyFence = windows.NewCallback(func(_, _, _ uintptr) uintptr { calls = append(calls, "fence"); return 0 })
	s.destroySwapchain = windows.NewCallback(func(_, _, _ uintptr) uintptr { calls = append(calls, "swapchain"); return 0 })
	s.destroySurface = windows.NewCallback(func(_, _, _ uintptr) uintptr { calls = append(calls, "surface"); return 0 })
	h.destroyDevice = windows.NewCallback(func(_, _ uintptr) uintptr { calls = append(calls, "device"); return 0 })
	h.destroyInstance = windows.NewCallback(func(_, _ uintptr) uintptr { calls = append(calls, "instance"); return 0 })
	s.pending = op
	h.swapchains[s] = struct{}{}
	if err := h.Close(); !errors.Is(err, ErrPresentBusy) || len(calls) != 0 {
		t.Fatalf("busy host destroyed resources: %v %v", err, calls)
	}
	ready = true
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{"fence", "swapchain", "surface", "device", "instance"}
	if len(calls) != len(want) {
		t.Fatalf("cleanup order %v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("cleanup order %v", calls)
		}
	}
}

func TestPresentReportsSurfaceRebuildAfterSafeCompletion(t *testing.T) {
	for _, tc := range []struct {
		name             string
		acquire, present int32
		want             error
	}{
		{"acquire_outdated", vkOutOfDate, 0, ErrSwapchainOutOfDate},
		{"acquire_suboptimal", vkSuboptimal, 0, ErrSwapchainOutOfDate},
		{"present_outdated", 0, vkOutOfDate, ErrSwapchainOutOfDate},
		{"present_suboptimal", 0, vkSuboptimal, ErrSwapchainOutOfDate},
		{"present_surface_lost", 0, vkSurfaceLost, ErrSurfaceLost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := &mockFrameDriver{}
			p := driver.procs()
			p.acquire = windows.NewCallback(func(_, _, _, _, _ uintptr, index *uint32) uintptr { *index = 0; return uintptr(uint32(tc.acquire)) })
			p.present = windows.NewCallback(func(_ uintptr, _ *presentInfo) uintptr { return uintptr(uint32(tc.present)) })
			h := &Host{device: 1, queue: 2, info: DeviceInfo{PresentFence: true}}
			s := &Swapchain{host: h, swapchain: 3, images: []uint64{4}}
			hooks := frameHooks{record: func(*Host, *presentResources, uintptr, uint64, *imageSubresourceRange) (uint32, uint32, error) {
				return vkLayoutTransferDst, vkAccessTransferWrite, nil
			}}
			err := s.presentFrameWith(hooks, func(*Host) (presentProcs, error) { return p, nil })
			if !errors.Is(err, tc.want) || !s.poisoned || s.pending != nil {
				t.Fatalf("rebuild status %v", err)
			}
		})
	}
}
