//go:build windows && amd64

package vulkan

import (
	"errors"
	"image"
	"testing"
	"unsafe"

	"github.com/neko233-com/NekoSimulator233/internal/previewinput"
	"golang.org/x/sys/windows"
)

func TestBlitWin64ABI(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"VkImageCreateInfo", unsafe.Sizeof(imageCreateInfo{}), 88},
		{"VkFormatProperties", unsafe.Sizeof(formatProperties{}), 12},
		{"VkImageFormatProperties", unsafe.Sizeof(imageFormatProperties{}), 32},
		{"VkImageBlit", unsafe.Sizeof(imageBlit{}), 80},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, tc.got, tc.want)
		}
	}
}

func fakeImageFormatProperties() uintptr {
	return windows.NewCallback(func(_, _, typ, tiling, usage, flags uintptr, props *imageFormatProperties) uintptr {
		if typ != 1 || tiling != 0 || usage != vkUsageTransferSrc|vkUsageTransferDst || flags != 0 {
			return uintptr(uint32(0xfffffff5))
		}
		props.MaxExtent = extent3D{8192, 8192, 1}
		props.MaxMipLevels, props.MaxArrayLayers, props.SampleCounts, props.MaxResourceSize = 1, 1, 1, 1<<32
		return 0
	})
}

func TestIntegerGPUScalingMatchesInputMapping(t *testing.T) {
	for sw := 1; sw <= 12; sw++ {
		for sh := 1; sh <= 12; sh++ {
			for scale := 2; scale <= 5; scale++ {
				plan, err := newFitPlan(sw, sh, extent2D{uint32(sw*scale + 2), uint32(sh * scale)})
				if err != nil || !plan.stableGPUScale() {
					t.Fatalf("incorrect integer scaling plan: %+v %v", plan, err)
				}
				for x := 0; x < plan.fw; x++ {
					if x*sw/plan.fw != (2*x+1)*sw/(2*plan.fw) {
						t.Fatal("GPU and pointer x coordinates differ")
					}
				}
				for y := 0; y < plan.fh; y++ {
					if y*sh/plan.fh != (2*y+1)*sh/(2*plan.fh) {
						t.Fatal("GPU and pointer y coordinates differ")
					}
				}
			}
		}
	}
	for _, dims := range [][4]int{{2, 2, 2, 2}, {4, 4, 2, 2}, {4, 4, 6, 6}, {4, 4, 5, 5}} {
		plan, err := newFitPlan(dims[0], dims[1], extent2D{uint32(dims[2]), uint32(dims[3])})
		if err != nil || plan.stableGPUScale() {
			t.Fatalf("unexpected GPU scaling for %v", dims)
		}
	}
}

func TestBlitFormatAndMemorySelection(t *testing.T) {
	h := &Host{physical: 1}
	props := uint32(vkFormatBlitSrc | vkFormatBlitDst)
	p := blitProcs{formatProperties: windows.NewCallback(func(_, _ uintptr, out *formatProperties) uintptr { out.Optimal = props; return 0 }), imageFormatProperties: fakeImageFormatProperties()}
	limits, supported, err := supportsBlit(p, h, vkFormatRGBA8)
	if err != nil || !supported || !limits.fits(1920, 1080) || limits.fits(8193, 1) {
		t.Fatal("supported blit was rejected")
	}
	props = vkFormatBlitSrc
	if _, supported, err := supportsBlit(p, h, vkFormatRGBA8); supported || err != nil {
		t.Fatal("missing destination support was accepted")
	}
	props = vkFormatBlitSrc | vkFormatBlitDst
	p.imageFormatProperties = windows.NewCallback(func(_, _, _, _, _, _, _ uintptr) uintptr { return uintptr(uint32(0xfffffff5)) })
	if _, supported, err := supportsBlit(p, h, vkFormatRGBA8); supported || err != nil {
		t.Fatal("unsupported image configuration accepted")
	}
	p.imageFormatProperties = windows.NewCallback(func(_, _, _, _, _, _, _ uintptr) uintptr { return uintptr(uint32(0xfffffffc)) })
	if _, supported, err := supportsBlit(p, h, vkFormatRGBA8); supported || err == nil {
		t.Fatal("device loss was silently ignored")
	}
	memory := physicalMemoryProperties{TypeCount: 3}
	memory.Types[1].Flags = vkMemoryDeviceLocal
	if i, err := chooseImageMemory(memory, 7); err != nil || i != 1 {
		t.Fatalf("device-local memory: %d %v", i, err)
	}
	if i, err := chooseImageMemory(memory, 4); err != nil || i != 2 {
		t.Fatalf("compatible fallback: %d %v", i, err)
	}
	if _, err := chooseImageMemory(memory, 0); err == nil {
		t.Fatal("missing memory accepted")
	}
}

func TestRecordScaledFrameUsesOrderedBarriersAndOffsets(t *testing.T) {
	plan, err := newFitPlan(2, 2, extent2D{6, 4})
	if err != nil || !plan.stableGPUScale() {
		t.Fatal("invalid plan")
	}
	var sequence []string
	var blit imageBlit
	op := &presentResources{staging: &stagingBuffer{buffer: 4}, scaled: &blitImage{image: 5, width: 2, height: 2}}
	op.procs.barrier = windows.NewCallback(func(_, _, _, _, _, _, _, _, _, ptr uintptr) uintptr {
		barrier := *(*imageMemoryBarrier)(unsafe.Pointer(ptr))
		sequence = append(sequence, string(rune('0'+barrier.Image)))
		return 0
	})
	op.procs.clear = windows.NewCallback(func(_, _, _, _, _, _ uintptr) uintptr { sequence = append(sequence, "clear"); return 0 })
	op.staging.procs.copyToImage = windows.NewCallback(func(_, _, _, _, _, _ uintptr) uintptr { sequence = append(sequence, "upload"); return 0 })
	op.scaled.procs.blit = windows.NewCallback(func(_, src, srcLayout, dst, dstLayout, count, ptr, filter uintptr) uintptr {
		if src != 5 || srcLayout != vkLayoutTransferSrc || dst != 6 || dstLayout != vkLayoutTransferDst || count != 1 || filter != 0 {
			sequence = append(sequence, "invalid")
		}
		blit = *(*imageBlit)(unsafe.Pointer(ptr))
		sequence = append(sequence, "blit")
		return 0
	})
	rng := imageSubresourceRange{AspectMask: 1, LevelCount: 1, LayerCount: 1}
	if err := recordScaledFrame(op, 3, 6, &rng, plan); err != nil {
		t.Fatal(err)
	}
	want := []string{"5", "upload", "5", "clear", "6", "blit"}
	if len(sequence) != len(want) {
		t.Fatalf("command order %v", sequence)
	}
	for i := range want {
		if sequence[i] != want[i] {
			t.Fatalf("command order %v", sequence)
		}
	}
	if blit.SrcOffsets[1] != (offset3D{2, 2, 1}) || blit.DstOffsets[0] != (offset3D{1, 0, 0}) || blit.DstOffsets[1] != (offset3D{5, 4, 1}) {
		t.Fatalf("blit geometry %+v", blit)
	}
}

func TestGPUScaleAllowsUnambiguousNonIntegerRatios(t *testing.T) {
	for _, dims := range [][4]int{{3, 3, 5, 5}, {3, 3, 2, 2}, {5, 5, 6, 6}} {
		plan, err := newFitPlan(dims[0], dims[1], extent2D{uint32(dims[2]), uint32(dims[3])})
		if err != nil || !plan.stableGPUScale() {
			t.Fatalf("safe ratio rejected %v: %v", dims, err)
		}
		frame := image.NewRGBA(image.Rect(0, 0, dims[0], dims[1]))
		for y := 0; y < dims[1]; y++ {
			for x := 0; x < dims[0]; x++ {
				i := y*frame.Stride + x*4
				frame.Pix[i], frame.Pix[i+1], frame.Pix[i+3] = byte(x+1), byte(y+1), 255
			}
		}
		pixels := make([]byte, dims[2]*dims[3]*4)
		if err := fittedFrameSample(pixels, frame, extent2D{uint32(dims[2]), uint32(dims[3])}, vkFormatRGBA8, false, true); err != nil {
			t.Fatal(err)
		}
		for y := 0; y < dims[3]; y++ {
			for x := 0; x < dims[2]; x++ {
				e := previewinput.Event{Kind: "down", X: x, Y: y, Width: dims[2], Height: dims[3], FrameWidth: dims[0], FrameHeight: dims[1], GPUScaled: true}
				sx, sy, ok := e.Point(false)
				i := (y*dims[2] + x) * 4
				if !ok || pixels[i] != byte(sx+1) || pixels[i+1] != byte(sy+1) {
					t.Fatalf("point %d,%d does not match rendered pixel", x, y)
				}
			}
		}
	}
	for _, dims := range [][2]int{{4, 2}, {4, 5}, {720, 1080}} {
		if stableBlitAxis(dims[0], dims[1]) {
			t.Fatalf("ambiguous ratio accepted: %v", dims)
		}
	}
}

func TestFractionalGPUSelectionAndAmbiguousFallback(t *testing.T) {
	h := &Host{device: 1, physical: 2, info: DeviceInfo{PresentFence: true}}
	procs := blitProcs{imageFormatProperties: fakeImageFormatProperties(), formatProperties: windows.NewCallback(func(_, _ uintptr, props *formatProperties) uintptr {
		props.Optimal = vkFormatBlitSrc | vkFormatBlitDst
		return 0
	})}
	for _, tc := range []struct {
		source, target int
		want           bool
	}{{3, 5, true}, {4, 5, false}, {3, 2, true}} {
		s := &Swapchain{host: h, swapchain: 1, extent: extent2D{uint32(tc.target), uint32(tc.target)}, format: surfaceFormat{Format: vkFormatRGBA8}}
		used := false
		hooks := s.frameRGBAHooksMode(image.NewRGBA(image.Rect(0, 0, tc.source, tc.source)), false, true, true, &used, nil, func(*Host) (blitProcs, error) { return procs, nil })
		if err := hooks.validate(h); err != nil || used != tc.want {
			t.Fatalf("%d -> %d: used=%v err=%v", tc.source, tc.target, used, err)
		}
	}
}

func TestBlitImageRemainsAliveUntilGPUFinishes(t *testing.T) {
	ready := false
	destroyed := 0
	op := &presentResources{renderPending: true, renderFence: 3, scaled: &blitImage{image: 4, memory: 5}}
	op.procs.fenceStatus = windows.NewCallback(func(_, _ uintptr) uintptr {
		if ready {
			return vkSuccess
		}
		return vkNotReady
	})
	op.procs.destroyFence = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
	op.scaled.procs.destroyImage = windows.NewCallback(func(_, _, _ uintptr) uintptr { destroyed++; return 0 })
	op.scaled.procs.memory.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { destroyed++; return 0 })
	h := &Host{device: 1}
	if err := op.releaseIfSafe(h); !errors.Is(err, ErrPresentBusy) || destroyed != 0 {
		t.Fatalf("in-flight image released: %v", err)
	}
	ready = true
	if err := op.releaseIfSafe(h); err != nil || destroyed != 2 || op.scaled != nil {
		t.Fatalf("finished image leaked: %v", err)
	}
}

func TestUnsupportedGPUScaleFallsBackBeforeAllocating(t *testing.T) {
	h := &Host{device: 1, physical: 2, info: DeviceInfo{PresentFence: true}}
	s := &Swapchain{host: h, swapchain: 1, extent: extent2D{4, 4}, format: surfaceFormat{Format: vkFormatRGBA8}}
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	used := true
	hooks := s.frameRGBAHooksMode(frame, false, true, true, &used, nil, func(*Host) (blitProcs, error) { return blitProcs{}, errors.New("unsupported") })
	if err := hooks.validate(h); err != nil || used {
		t.Fatalf("fallback failed: %v", err)
	}
}

func TestIntegerGPUScalePipelineAndReadback(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	copy(frame.Pix, []byte{1, 2, 3, 255, 10, 20, 30, 255, 40, 50, 60, 255, 70, 80, 90, 255})
	var source [16]byte
	destination := make([]byte, 6*4*4)
	buffers := map[uintptr][]byte{}
	var handle uintptr = 100
	var invalid bool
	driver := &mockFrameDriver{}
	present := driver.procs()
	present.clear = windows.NewCallback(func(_, _, _, _, _, _ uintptr) uintptr {
		for i := 0; i < len(destination); i += 4 {
			destination[i], destination[i+1], destination[i+2], destination[i+3] = 0, 0, 0, 255
		}
		return 0
	})
	copyTo := windows.NewCallback(func(_, buffer, imageHandle, layout, _, ptr uintptr) uintptr {
		region := *(*bufferImageCopy)(unsafe.Pointer(ptr))
		if imageHandle != 20 || layout != vkLayoutTransferDst || region.Extent != (extent3D{2, 2, 1}) || len(buffers[buffer]) != len(source) {
			invalid = true
			return 0
		}
		copy(source[:], buffers[buffer])
		return 0
	})
	copyFrom := windows.NewCallback(func(_, _, _, buffer, _, _ uintptr) uintptr { copy(buffers[buffer], destination); return 0 })
	createBuffer := func(b *stagingBuffer, _ *Host, size int, verify bool) error {
		handle++
		data := make([]byte, size)
		buffers[handle] = data
		b.buffer, b.memory, b.size, b.verify, b.mapped = uint64(handle), uint64(handle+1000), size, verify, unsafe.Pointer(&data[0])
		b.procs.copyToImage, b.procs.copyFromImage = copyTo, copyFrom
		b.procs.unmapMemory = windows.NewCallback(func(_, _ uintptr) uintptr { return 0 })
		b.procs.destroyBuffer = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
		b.procs.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
		return nil
	}
	var imageReleases, memoryReleases int
	blit := blitProcs{}
	blit.formatProperties = windows.NewCallback(func(_, _ uintptr, props *formatProperties) uintptr {
		props.Optimal = vkFormatBlitSrc | vkFormatBlitDst
		return 0
	})
	blit.imageFormatProperties = fakeImageFormatProperties()
	blit.createImage = windows.NewCallback(func(_ uintptr, info *imageCreateInfo, _ uintptr, out *uint64) uintptr {
		if info.Extent != (extent3D{2, 2, 1}) || info.Usage != vkUsageTransferSrc|vkUsageTransferDst || info.Format != vkFormatRGBA8 {
			invalid = true
		}
		*out = 20
		return 0
	})
	blit.requirements = windows.NewCallback(func(_, _ uintptr, req *memoryRequirements) uintptr { req.Size, req.MemoryTypeBits = 64, 1; return 0 })
	blit.memory.memoryProperties = windows.NewCallback(func(_ uintptr, props *physicalMemoryProperties) uintptr {
		props.TypeCount = 1
		props.Types[0].Flags = vkMemoryDeviceLocal
		return 0
	})
	blit.memory.allocate = windows.NewCallback(func(_ uintptr, _ *memoryAllocateInfo, _ uintptr, out *uint64) uintptr { *out = 21; return 0 })
	blit.bindImage = windows.NewCallback(func(_, _, _, _ uintptr) uintptr { return 0 })
	blit.destroyImage = windows.NewCallback(func(_, _, _ uintptr) uintptr { imageReleases++; return 0 })
	blit.memory.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { memoryReleases++; return 0 })
	blit.blit = windows.NewCallback(func(_, _, _, _, _, _ uintptr, ptr *imageBlit, _ uintptr) uintptr {
		for y := int(ptr.DstOffsets[0].Y); y < int(ptr.DstOffsets[1].Y); y++ {
			for x := int(ptr.DstOffsets[0].X); x < int(ptr.DstOffsets[1].X); x++ {
				sx, sy := (x-int(ptr.DstOffsets[0].X))/2, (y-int(ptr.DstOffsets[0].Y))/2
				copy(destination[(y*6+x)*4:(y*6+x+1)*4], source[(sy*2+sx)*4:(sy*2+sx+1)*4])
			}
		}
		return 0
	})
	h := &Host{device: 1, physical: 2, queue: 3, info: DeviceInfo{PresentFence: true}}
	s := &Swapchain{host: h, swapchain: 4, images: []uint64{5}, extent: extent2D{6, 4}, format: surfaceFormat{Format: vkFormatRGBA8}, usage: vkUsageTransferSrc}
	used := false
	hooks := s.frameRGBAHooksMode(frame, true, true, true, &used, createBuffer, func(*Host) (blitProcs, error) { return blit, nil })
	if err := s.presentFrameWith(hooks, func(*Host) (presentProcs, error) { return present, nil }); err != nil {
		t.Fatal(err)
	}
	if invalid || !used || !s.Info().GPUScaled || s.reusable.scaled == nil || s.reusable.staging.size != 16 || s.reusable.readback.size != len(destination) {
		t.Fatal("GPU scaling did not use expected image and buffers")
	}
	if err := s.reusable.releaseIfSafe(h); err != nil || imageReleases != 1 || memoryReleases != 1 {
		t.Fatalf("scale resources not released: %v %d %d", err, imageReleases, memoryReleases)
	}
}

func TestGPUImageAllocationFailureFallsBackWithoutHidingDeviceLoss(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   uint32
		fallback bool
	}{
		{"out_of_device_memory", 0xfffffffe, true},
		{"device_lost", 0xfffffffc, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := &mockFrameDriver{}
			present := driver.procs()
			var attempts, imageReleases, uploads int
			procs := blitProcs{imageFormatProperties: fakeImageFormatProperties()}
			procs.formatProperties = windows.NewCallback(func(_, _ uintptr, props *formatProperties) uintptr {
				props.Optimal = vkFormatBlitSrc | vkFormatBlitDst
				return 0
			})
			procs.createImage = windows.NewCallback(func(_ uintptr, _ *imageCreateInfo, _ uintptr, out *uint64) uintptr { attempts++; *out = 20; return 0 })
			procs.requirements = windows.NewCallback(func(_, _ uintptr, req *memoryRequirements) uintptr { req.Size, req.MemoryTypeBits = 64, 1; return 0 })
			procs.memory.memoryProperties = windows.NewCallback(func(_ uintptr, props *physicalMemoryProperties) uintptr { props.TypeCount = 1; return 0 })
			procs.memory.allocate = windows.NewCallback(func(_, _, _, _ uintptr) uintptr { return uintptr(tc.result) })
			procs.destroyImage = windows.NewCallback(func(_, _, _ uintptr) uintptr { imageReleases++; return 0 })
			data := make([]byte, 4*4*4)
			create := func(b *stagingBuffer, _ *Host, size int, verify bool) error {
				if size != len(data) {
					t.Fatalf("fallback staging size %d", size)
				}
				b.buffer, b.memory, b.size, b.verify, b.mapped = 30, 31, size, verify, unsafe.Pointer(&data[0])
				b.procs.copyToImage = windows.NewCallback(func(_, _, _, _, _, _ uintptr) uintptr { uploads++; return 0 })
				b.procs.unmapMemory = windows.NewCallback(func(_, _ uintptr) uintptr { return 0 })
				b.procs.destroyBuffer = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
				b.procs.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
				return nil
			}
			h := &Host{device: 1, physical: 2, queue: 3, info: DeviceInfo{PresentFence: true}}
			s := &Swapchain{host: h, swapchain: 4, images: []uint64{5}, extent: extent2D{4, 4}, format: surfaceFormat{Format: vkFormatRGBA8}}
			frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
			used := true
			makeHooks := func() frameHooks {
				return s.frameRGBAHooksMode(frame, false, true, true, &used, create, func(*Host) (blitProcs, error) { return procs, nil })
			}
			err := s.presentFrameWith(makeHooks(), func(*Host) (presentProcs, error) { return present, nil })
			if !tc.fallback {
				if err == nil || uploads != 0 || !s.poisoned || imageReleases != 1 {
					t.Fatalf("device loss was hidden: %v uploads=%d releases=%d", err, uploads, imageReleases)
				}
				return
			}
			if err != nil || used || s.Info().GPUScaled || uploads != 1 || imageReleases != 1 || s.reusable.scaled != nil {
				t.Fatalf("CPU fallback failed: %v used=%v uploads=%d releases=%d", err, used, uploads, imageReleases)
			}
			if err := s.presentFrameWith(makeHooks(), func(*Host) (presentProcs, error) { return present, nil }); err != nil || attempts != 1 || uploads != 2 {
				t.Fatalf("failed GPU path retried: %v attempts=%d uploads=%d", err, attempts, uploads)
			}
			if err := s.reusable.releaseIfSafe(h); err != nil {
				t.Fatal(err)
			}
		})
	}
}
