//go:build windows && amd64

package vulkan

import (
	"errors"
	"fmt"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type mockFrameDriver struct {
	createdFences, createdPools, createdSemaphores, allocatedCommands             int
	resetFences, resetPools, destroyedFences, destroyedPools, destroyedSemaphores int
	failReset                                                                     bool
	waits, timeoutAt                                                              int
	ready                                                                         bool
	stagingUnmaps, stagingBuffers, stagingMemory                                  int
}

func (m *mockFrameDriver) procs() presentProcs {
	var p presentProcs
	p.createFence = windows.NewCallback(func(_ uintptr, _ *fenceCreateInfo, _ uintptr, out *uint64) uintptr {
		m.createdFences++
		*out = uint64(m.createdFences + 10)
		return 0
	})
	p.createSemaphore = windows.NewCallback(func(_ uintptr, _ *semaphoreCreateInfo, _ uintptr, out *uint64) uintptr {
		m.createdSemaphores++
		*out = 20
		return 0
	})
	p.createPool = windows.NewCallback(func(_ uintptr, _ *commandPoolCreateInfo, _ uintptr, out *uint64) uintptr {
		m.createdPools++
		*out = 30
		return 0
	})
	p.allocateCommands = windows.NewCallback(func(_ uintptr, _ *commandBufferAllocateInfo, out *uintptr) uintptr {
		m.allocatedCommands++
		*out = 40
		return 0
	})
	p.destroyFence = windows.NewCallback(func(_, _, _ uintptr) uintptr { m.destroyedFences++; return 0 })
	p.destroySemaphore = windows.NewCallback(func(_, _, _ uintptr) uintptr { m.destroyedSemaphores++; return 0 })
	p.destroyPool = windows.NewCallback(func(_, _, _ uintptr) uintptr { m.destroyedPools++; return 0 })
	p.resetFences = windows.NewCallback(func(_ uintptr, n uint32, fs *uint64) uintptr {
		m.resetFences++
		if n != 3 || fs == nil {
			tpanic()
		}
		if m.failReset {
			return uintptr(uint32(0xfffffffc))
		}
		return 0
	})
	p.resetPool = windows.NewCallback(func(_, _, _ uintptr) uintptr { m.resetPools++; return 0 })
	p.acquire = windows.NewCallback(func(_, _, _, _, _ uintptr, index *uint32) uintptr { *index = 0; return 0 })
	p.waitFences = windows.NewCallback(func(_, _, _, _, _ uintptr) uintptr {
		m.waits++
		if m.waits == m.timeoutAt {
			return vkTimeout
		}
		return 0
	})
	p.fenceStatus = windows.NewCallback(func(_, _ uintptr) uintptr {
		if m.ready {
			return vkSuccess
		}
		return vkNotReady
	})
	p.beginCommand = windows.NewCallback(func(_ uintptr, _ *commandBufferBeginInfo) uintptr { return 0 })
	p.endCommand = windows.NewCallback(func(_ uintptr) uintptr { return 0 })
	p.barrier = windows.NewCallback(func(_, _, _, _, _, _, _, _, _, _ uintptr) uintptr { return 0 })
	p.submit = windows.NewCallback(func(_, _, _, _ uintptr) uintptr { return 0 })
	p.present = windows.NewCallback(func(_ uintptr, _ *presentInfo) uintptr { return 0 })
	return p
}

func tpanic() { panic("bad reset arguments") }

func TestPresentResourcesReusedAcrossFrames(t *testing.T) {
	driver := &mockFrameDriver{}
	procs := driver.procs()
	h := &Host{device: 1, instance: 2, queue: 3, info: DeviceInfo{PresentFence: true}, swapchains: map[*Swapchain]struct{}{}}
	s := &Swapchain{host: h, swapchain: 4, images: []uint64{5}}
	h.swapchains[s] = struct{}{}
	s.destroySwapchain = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
	hooks := frameHooks{record: func(_ *Host, _ *presentResources, _ uintptr, _ uint64, _ *imageSubresourceRange) (uint32, uint32, error) {
		return vkLayoutTransferDst, vkAccessTransferWrite, nil
	}}
	resolved := 0
	factory := func(*Host) (presentProcs, error) { resolved++; return procs, nil }
	for i := 0; i < 3; i++ {
		if err := s.presentFrameWith(hooks, factory); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if resolved != 1 || driver.createdFences != 3 || driver.createdPools != 1 || driver.createdSemaphores != 1 || driver.allocatedCommands != 1 || driver.resetFences != 2 || driver.resetPools != 2 {
		t.Fatalf("unexpected allocations/resets: %+v resolved=%d", driver, resolved)
	}
	if driver.destroyedFences != 0 || driver.destroyedPools != 0 || driver.destroyedSemaphores != 0 {
		t.Fatal("resources destroyed between successful frames")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if driver.destroyedFences != 3 || driver.destroyedPools != 1 || driver.destroyedSemaphores != 1 {
		t.Fatalf("close did not release resources: %+v", driver)
	}
}

func TestPresentResetFailureDoesNotReuseResources(t *testing.T) {
	driver := &mockFrameDriver{}
	procs := driver.procs()
	h := &Host{device: 1, instance: 2, queue: 3, info: DeviceInfo{PresentFence: true}}
	s := &Swapchain{host: h, swapchain: 4, images: []uint64{5}}
	hooks := frameHooks{record: func(_ *Host, _ *presentResources, _ uintptr, _ uint64, _ *imageSubresourceRange) (uint32, uint32, error) {
		return vkLayoutTransferDst, vkAccessTransferWrite, nil
	}}
	factory := func(*Host) (presentProcs, error) { return procs, nil }
	if err := s.presentFrameWith(hooks, factory); err != nil {
		t.Fatal(err)
	}
	driver.failReset = true
	if err := s.presentFrameWith(hooks, factory); err == nil || !s.poisoned || s.reusable != nil {
		t.Fatal("failed reset did not retire frame resources")
	}
	if driver.destroyedFences != 3 || driver.destroyedPools != 1 || driver.destroyedSemaphores != 1 {
		t.Fatalf("failed reset did not release resources: %+v", driver)
	}
}

func TestResetRejectsInFlightWork(t *testing.T) {
	op := &presentResources{renderPending: true}
	if err := op.reset(&Host{}); !errors.Is(err, ErrPresentBusy) {
		t.Fatalf("pending reset accepted: %v", err)
	}
}

func TestTimedOutPresentRetainsResourcesUntilSafe(t *testing.T) {
	for _, timeoutAt := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("wait_%d", timeoutAt), func(t *testing.T) {
			driver := &mockFrameDriver{timeoutAt: timeoutAt}
			procs := driver.procs()
			h := &Host{device: 1, instance: 2, queue: 3, info: DeviceInfo{PresentFence: true}}
			s := &Swapchain{host: h, swapchain: 4, images: []uint64{5}}
			s.destroySwapchain = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
			data := make([]byte, 16)
			hooks := frameHooks{prepare: func(_ *Host, op *presentResources) error {
				b := &stagingBuffer{buffer: 6, memory: 7, size: len(data), mapped: unsafe.Pointer(&data[0])}
				b.procs.unmapMemory = windows.NewCallback(func(_, _ uintptr) uintptr { driver.stagingUnmaps++; return 0 })
				b.procs.destroyBuffer = windows.NewCallback(func(_, _, _ uintptr) uintptr { driver.stagingBuffers++; return 0 })
				b.procs.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { driver.stagingMemory++; return 0 })
				op.staging = b
				return nil
			}, record: func(_ *Host, _ *presentResources, _ uintptr, _ uint64, _ *imageSubresourceRange) (uint32, uint32, error) {
				return vkLayoutTransferDst, vkAccessTransferWrite, nil
			}}
			if err := s.presentFrameWith(hooks, func(*Host) (presentProcs, error) { return procs, nil }); !errors.Is(err, ErrPresentBusy) || s.pending == nil {
				t.Fatalf("timeout did not retain resources: %v", err)
			}
			if err := s.Close(); !errors.Is(err, ErrPresentBusy) || driver.destroyedFences != 0 || driver.destroyedPools != 0 || driver.stagingUnmaps != 0 || driver.stagingBuffers != 0 || driver.stagingMemory != 0 {
				t.Fatalf("busy close destroyed resources: %v", err)
			}
			driver.ready = true
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if driver.destroyedFences != 3 || driver.destroyedPools != 1 || driver.destroyedSemaphores != 1 || driver.stagingUnmaps != 1 || driver.stagingBuffers != 1 || driver.stagingMemory != 1 {
				t.Fatalf("resources not released after fence completion: %+v", driver)
			}
		})
	}
}

func TestPersistentStagingCompatibilityAndRelease(t *testing.T) {
	data := make([]byte, 16)
	b := &stagingBuffer{buffer: 1, memory: 2, size: len(data), mapped: unsafe.Pointer(&data[0])}
	if !b.compatible(16, false) || b.compatible(16, true) || b.compatible(8, false) {
		t.Fatal("incorrect staging compatibility")
	}
	b.verify = true
	if !b.compatible(16, true) {
		t.Fatal("readback-capable staging was not reusable")
	}
	var calls []string
	b.procs.unmapMemory = windows.NewCallback(func(_, _ uintptr) uintptr { calls = append(calls, "unmap"); return 0 })
	b.procs.destroyBuffer = windows.NewCallback(func(_, _, _ uintptr) uintptr { calls = append(calls, "buffer"); return 0 })
	b.procs.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { calls = append(calls, "memory"); return 0 })
	if err := b.withMapped(&Host{}, func(p []byte) error { p[0] = 42; return nil }); err != nil || data[0] != 42 {
		t.Fatal("persistent mapping is not usable")
	}
	b.release(&Host{device: 3})
	b.release(&Host{device: 3})
	if len(calls) != 3 || calls[0] != "unmap" || calls[1] != "buffer" || calls[2] != "memory" {
		t.Fatalf("unexpected release order %v", calls)
	}
}

func TestStagingReusesOrReplacesWhenReadbackRequirementsChange(t *testing.T) {
	h := &Host{device: 1}
	op := &presentResources{}
	data := make([]byte, 16)
	created, released := 0, 0
	create := func(b *stagingBuffer, _ *Host, size int, verify bool) error {
		created++
		b.buffer = 1
		b.memory = 2
		b.size = size
		b.verify = verify
		b.mapped = unsafe.Pointer(&data[0])
		b.procs.unmapMemory = windows.NewCallback(func(_, _ uintptr) uintptr { return 0 })
		b.procs.destroyBuffer = windows.NewCallback(func(_, _, _ uintptr) uintptr { released++; return 0 })
		b.procs.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
		return nil
	}
	for _, verify := range []bool{false, false, true, false, true} {
		if err := op.ensureStaging(h, 16, verify, create); err != nil {
			t.Fatal(err)
		}
	}
	if created != 2 || released != 1 {
		t.Fatalf("created=%d released=%d", created, released)
	}
	op.staging.release(h)
	if released != 2 {
		t.Fatalf("final buffer not released: %d", released)
	}
}
