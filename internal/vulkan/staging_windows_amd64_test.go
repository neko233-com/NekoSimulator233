//go:build windows && amd64

package vulkan

import (
	"bytes"
	"image"
	"strings"
	"testing"
	"unsafe"

	"github.com/neko233-com/NekoSimulator233/internal/previewinput"
	"golang.org/x/sys/windows"
)

func TestUnscaledLetterboxRecordsClearBarrierAndSourceCopy(t *testing.T) {
	for _, dims := range [][4]int{{4, 2, 4, 5}, {2, 4, 5, 4}} {
		plan, err := newFitPlan(dims[0], dims[1], extent2D{uint32(dims[2]), uint32(dims[3])})
		if err != nil || !plan.unscaledLetterbox() {
			t.Fatalf("letterbox plan %v: %v", dims, err)
		}
		var calls []string
		var gotColor [4]float32
		var gotBarrier imageMemoryBarrier
		var gotRegion bufferImageCopy
		op := &presentResources{staging: &stagingBuffer{buffer: 8}}
		op.procs.clear = windows.NewCallback(func(_, _, _, color, _, _ uintptr) uintptr {
			calls = append(calls, "clear")
			gotColor = *(*[4]float32)(unsafe.Pointer(color))
			return 0
		})
		op.procs.barrier = windows.NewCallback(func(_, src, dst, _, _, _, _, _, count, ptr uintptr) uintptr {
			calls = append(calls, "barrier")
			if src != vkStageTransfer || dst != vkStageTransfer || count != 1 {
				calls = append(calls, "invalid barrier")
			}
			gotBarrier = *(*imageMemoryBarrier)(unsafe.Pointer(ptr))
			return 0
		})
		op.staging.procs.copyToImage = windows.NewCallback(func(_, buffer, image, layout, count, ptr uintptr) uintptr {
			calls = append(calls, "copy")
			if buffer != 8 || image != 9 || layout != vkLayoutTransferDst || count != 1 {
				calls = append(calls, "invalid copy")
			}
			gotRegion = *(*bufferImageCopy)(unsafe.Pointer(ptr))
			return 0
		})
		rng := imageSubresourceRange{AspectMask: 1, LevelCount: 1, LayerCount: 1}
		if err := recordUnscaledLetterbox(op, 7, 9, &rng, plan); err != nil {
			t.Fatal(err)
		}
		if len(calls) != 3 || calls[0] != "clear" || calls[1] != "barrier" || calls[2] != "copy" || gotColor != [4]float32{0, 0, 0, 1} {
			t.Fatalf("invalid command sequence or clear color: %v %v", calls, gotColor)
		}
		if gotBarrier.SrcAccess != vkAccessTransferWrite || gotBarrier.DstAccess != vkAccessTransferWrite || gotBarrier.OldLayout != vkLayoutTransferDst || gotBarrier.NewLayout != vkLayoutTransferDst || gotBarrier.Image != 9 {
			t.Fatalf("invalid barrier %+v", gotBarrier)
		}
		if gotRegion.Offset.X != int32(plan.left) || gotRegion.Offset.Y != int32(plan.top) || gotRegion.Extent != (extent3D{Width: uint32(plan.sw), Height: uint32(plan.sh), Depth: 1}) {
			t.Fatalf("invalid source region %+v", gotRegion)
		}
	}
	for _, dims := range [][4]int{{4, 2, 4, 2}, {4, 2, 8, 4}, {4, 2, 2, 3}} {
		plan, err := newFitPlan(dims[0], dims[1], extent2D{uint32(dims[2]), uint32(dims[3])})
		if err != nil || plan.unscaledLetterbox() {
			t.Fatalf("incorrect fast-path selection for %v", dims)
		}
	}
}

func TestUnscaledLetterboxUsesSamePathForReadbackVerification(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for i := range frame.Pix {
		frame.Pix[i] = byte(i*17 + 1)
	}
	extent := extent2D{4, 5}
	pixels := make([]byte, int(extent.Width*extent.Height*4))
	buffers := map[uintptr][]byte{}
	var nextHandle uintptr = 10
	var invalid bool
	var corrupt bool
	driver := &mockFrameDriver{}
	p := driver.procs()
	p.clear = windows.NewCallback(func(_, _, _, color, _, _ uintptr) uintptr {
		if *(*[4]float32)(unsafe.Pointer(color)) != [4]float32{0, 0, 0, 1} {
			invalid = true
		}
		for i := 0; i < len(pixels); i += 4 {
			pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = 0, 0, 0, 255
		}
		return 0
	})
	copyTo := windows.NewCallback(func(_, buffer, _, _, _, ptr uintptr) uintptr {
		region := *(*bufferImageCopy)(unsafe.Pointer(ptr))
		src := buffers[buffer]
		if region.Extent.Width != 4 || region.Extent.Height != 2 || region.Offset.Y != 1 || len(src) != len(frame.Pix) {
			invalid = true
			return 0
		}
		for y := 0; y < int(region.Extent.Height); y++ {
			copy(pixels[(y+int(region.Offset.Y))*int(extent.Width)*4:], src[y*int(region.Extent.Width)*4:(y+1)*int(region.Extent.Width)*4])
		}
		return 0
	})
	copyFrom := windows.NewCallback(func(_, _, _, buffer, _, _ uintptr) uintptr {
		copy(buffers[buffer], pixels)
		if corrupt {
			buffers[buffer][0] ^= 1
		}
		return 0
	})
	create := func(b *stagingBuffer, _ *Host, size int, verify bool) error {
		nextHandle++
		data := make([]byte, size)
		buffers[nextHandle] = data
		b.buffer, b.memory, b.size, b.verify, b.mapped = uint64(nextHandle), uint64(nextHandle+100), size, verify, unsafe.Pointer(&data[0])
		b.procs.copyToImage, b.procs.copyFromImage = copyTo, copyFrom
		b.procs.unmapMemory = windows.NewCallback(func(_, _ uintptr) uintptr { return 0 })
		b.procs.destroyBuffer = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
		b.procs.free = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0 })
		return nil
	}
	h := &Host{device: 1, queue: 2, info: DeviceInfo{PresentFence: true}}
	s := &Swapchain{host: h, swapchain: 3, images: []uint64{4}, extent: extent, format: surfaceFormat{Format: vkFormatRGBA8}, usage: vkUsageTransferSrc}
	if err := s.presentFrameWith(s.frameRGBAHooks(frame, true, true, create), func(*Host) (presentProcs, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
	if invalid || s.reusable.staging.size != len(frame.Pix) || s.reusable.readback == nil || s.reusable.readback.size != len(pixels) {
		t.Fatalf("fast path or readback buffer incorrect: invalid=%v", invalid)
	}
	want := make([]byte, len(pixels))
	if err := fittedFrame(want, frame, extent, vkFormatRGBA8, false); err != nil || !bytes.Equal(pixels, want) {
		t.Fatal("verified GPU output differs from reference", err)
	}
	if err := s.presentFrameWith(s.frameRGBAHooks(frame, false, true, create), func(*Host) (presentProcs, error) { return p, nil }); err != nil || s.reusable.readback != nil {
		t.Fatalf("diagnostic buffer was not released when verification stopped: %v", err)
	}
	corrupt = true
	if err := s.presentFrameWith(s.frameRGBAHooks(frame, true, true, create), func(*Host) (presentProcs, error) { return p, nil }); err == nil || !strings.Contains(err.Error(), "回读不一致") {
		t.Fatalf("corrupted readback was not rejected: %v", err)
	}
	if s.reusable != nil || s.pending != nil || !s.poisoned {
		t.Fatal("failed verification did not retire both buffers safely")
	}
}

func TestStagingWin64ABI(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"VkBufferCreateInfo", unsafe.Sizeof(bufferCreateInfo{}), 56},
		{"VkMemoryRequirements", unsafe.Sizeof(memoryRequirements{}), 24},
		{"VkMemoryAllocateInfo", unsafe.Sizeof(memoryAllocateInfo{}), 32},
		{"VkPhysicalDeviceMemoryProperties", unsafe.Sizeof(physicalMemoryProperties{}), 520},
		{"VkBufferMemoryBarrier", unsafe.Sizeof(bufferMemoryBarrier{}), 56},
		{"VkBufferImageCopy", unsafe.Sizeof(bufferImageCopy{}), 56},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func BenchmarkFittedFrame(b *testing.B) {
	for _, tc := range []struct {
		name         string
		sw, sh, w, h int
	}{{"portrait", 720, 1280, 900, 700}, {"landscape", 1280, 720, 900, 700}} {
		b.Run(tc.name+"/baseline", func(b *testing.B) {
			frame := image.NewRGBA(image.Rect(0, 0, tc.sw, tc.sh))
			dst := make([]byte, tc.w*tc.h*4)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := fittedFrame(dst, frame, extent2D{uint32(tc.w), uint32(tc.h)}, vkFormatBGRA8, false); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/cached", func(b *testing.B) {
			frame := image.NewRGBA(image.Rect(0, 0, tc.sw, tc.sh))
			dst := make([]byte, tc.w*tc.h*4)
			plan, err := newFitPlan(tc.sw, tc.sh, extent2D{uint32(tc.w), uint32(tc.h)})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := plan.pack(dst, frame, vkFormatBGRA8); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestFitPlanMatchesReferenceAndReusesGeometry(t *testing.T) {
	for _, c := range [][4]int{{1, 1, 1, 1}, {2, 1, 2, 3}, {3, 7, 8, 5}, {7, 3, 2, 9}, {8, 4, 16, 8}, {13, 17, 19, 11}} {
		sw, sh, w, h := c[0], c[1], c[2], c[3]
		parent := image.NewRGBA(image.Rect(0, 0, sw+2, sh+2))
		frame := parent.SubImage(image.Rect(1, 1, sw+1, sh+1)).(*image.RGBA)
		for i := range frame.Pix {
			frame.Pix[i] = byte(i*73 + 19)
		}
		extent := extent2D{uint32(w), uint32(h)}
		plan, err := newFitPlan(sw, sh, extent)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []uint32{vkFormatRGBA8, vkFormatBGRA8, vkFormatRGBA8SRGB, vkFormatBGRA8SRGB} {
			for pass := 0; pass < 2; pass++ {
				frame.Pix[0]++
				got, want := make([]byte, w*h*4), make([]byte, w*h*4)
				if err := fittedFrame(want, frame, extent, format, false); err != nil {
					t.Fatal(err)
				}
				if err := plan.pack(got, frame, format); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("format %d geometry %v pass %d differs", format, c, pass)
				}
			}
		}
		if err := plan.pack(make([]byte, w*h*4-1), frame, vkFormatRGBA8); err == nil {
			t.Fatal("accepted short output")
		}
		if err := plan.pack(make([]byte, w*h*4), frame, 999); err == nil {
			t.Fatal("accepted unsupported format")
		}
	}
}

func TestSwapchainFitPlanCache(t *testing.T) {
	h := &Host{swapchains: make(map[*Swapchain]struct{})}
	s := &Swapchain{host: h, extent: extent2D{6, 6}}
	frame := image.NewRGBA(image.Rect(0, 0, 4, 2))
	if err := s.ensureFitPlan(frame); err != nil {
		t.Fatal(err)
	}
	first := s.fit
	if err := s.ensureFitPlan(frame); err != nil || s.fit != first {
		t.Fatal("same geometry was not reused")
	}
	frame = image.NewRGBA(image.Rect(0, 0, 2, 4))
	if err := s.ensureFitPlan(frame); err != nil || s.fit == first {
		t.Fatal("changed geometry reused old plan")
	}
	if err := s.Close(); err != nil || s.fit != nil {
		t.Fatal("close retained plan")
	}
}

func TestFittedFrameMatchesPreviewInputCoordinates(t *testing.T) {
	for _, dims := range [][4]int{{4, 2, 5, 5}, {2, 4, 7, 3}, {3, 3, 2, 2}, {1, 5, 8, 7}} {
		sw, sh, w, h := dims[0], dims[1], dims[2], dims[3]
		frame := image.NewRGBA(image.Rect(0, 0, sw, sh))
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				i := y*frame.Stride + x*4
				frame.Pix[i], frame.Pix[i+1], frame.Pix[i+3] = byte(x+1), byte(y+1), 255
			}
		}
		buf := make([]byte, w*h*4)
		if err := fittedFrame(buf, frame, extent2D{uint32(w), uint32(h)}, vkFormatRGBA8, false); err != nil {
			t.Fatal(err)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				e := previewinput.Event{Kind: "down", X: x, Y: y, Width: w, Height: h, FrameWidth: sw, FrameHeight: sh}
				sx, sy, ok := e.Point(false)
				i := (y*w + x) * 4
				if ok && (buf[i] != byte(sx+1) || buf[i+1] != byte(sy+1)) {
					t.Fatalf("frame %v point %d,%d mapped to %d,%d", dims, x, y, sx, sy)
				}
				if !ok && (buf[i] != 0 || buf[i+1] != 0 || buf[i+2] != 0 || buf[i+3] != 255) {
					t.Fatalf("frame %v bar %d,%d", dims, x, y)
				}
			}
		}
	}
}

func TestFramePackingAndValidation(t *testing.T) {
	frame := &image.RGBA{Pix: []byte{1, 2, 3, 4, 5, 6, 7, 8, 99, 99, 99, 99, 9, 10, 11, 12, 13, 14, 15, 16}, Stride: 12, Rect: image.Rect(4, 5, 6, 7)}
	if size, err := frameSize(frame, extent2D{2, 2}); err != nil || size != 16 {
		t.Fatalf("frame size %d %v", size, err)
	}
	packed := make([]byte, 16)
	if err := frameBytes(packed, frame, vkFormatBGRA8, false); err != nil {
		t.Fatal(err)
	}
	want := []byte{3, 2, 1, 4, 7, 6, 5, 8, 11, 10, 9, 12, 15, 14, 13, 16}
	if !bytes.Equal(packed, want) {
		t.Fatalf("BGRA packing %v", packed)
	}
	if err := frameBytes(packed, frame, vkFormatBGRA8, true); err != nil {
		t.Fatal(err)
	}
	packed[15]++
	if err := frameBytes(packed, frame, vkFormatBGRA8, true); err == nil {
		t.Fatal("readback mismatch accepted")
	}
	if _, err := frameSize(frame, extent2D{3, 2}); err == nil {
		t.Fatal("wrong dimensions accepted")
	}
	frame.Pix = frame.Pix[:19]
	if _, err := frameSize(frame, extent2D{2, 2}); err == nil {
		t.Fatal("truncated image accepted")
	}
}

func TestFittedFramePacking(t *testing.T) {
	frame := &image.RGBA{Rect: image.Rect(3, 5, 5, 6), Stride: 12, Pix: []byte{1, 2, 3, 255, 4, 5, 6, 255, 99, 99, 99, 99}}
	buf := make([]byte, 2*3*4)
	if err := fittedFrame(buf, frame, extent2D{2, 3}, vkFormatBGRA8, false); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 255, 0, 0, 0, 255, 3, 2, 1, 255, 6, 5, 4, 255, 0, 0, 0, 255, 0, 0, 0, 255}
	if !bytes.Equal(buf, want) {
		t.Fatalf("letterboxed BGRA: %v", buf)
	}
	if err := fittedFrame(buf, frame, extent2D{2, 3}, vkFormatBGRA8, true); err != nil {
		t.Fatal(err)
	}
	buf[0]++
	if err := fittedFrame(buf, frame, extent2D{2, 3}, vkFormatBGRA8, true); err == nil {
		t.Fatal("incorrect bar accepted")
	}
	up := make([]byte, 4*2*4)
	if err := fittedFrame(up, frame, extent2D{4, 2}, vkFormatRGBA8, false); err != nil {
		t.Fatal(err)
	}
	wantRow := []byte{1, 2, 3, 255, 1, 2, 3, 255, 4, 5, 6, 255, 4, 5, 6, 255}
	if !bytes.Equal(up, append(append([]byte{}, wantRow...), wantRow...)) {
		t.Fatalf("upscale: %v", up)
	}
	if err := fittedFrame(make([]byte, 1), frame, extent2D{2, 3}, vkFormatRGBA8, false); err == nil {
		t.Fatal("accepted truncated target")
	}
	if err := fittedFrame(make([]byte, 4), frame, extent2D{1, 1}, 999, false); err == nil {
		t.Fatal("accepted unsupported format")
	}
	h := &Host{device: 1, info: DeviceInfo{PresentFence: true}}
	s := &Swapchain{host: h, swapchain: 1, extent: extent2D{8193, 1}}
	if err := s.PresentFittedRGBA(frame, false); err == nil || s.poisoned {
		t.Fatal("invalid size accepted or poisoned swapchain")
	}
}

func TestHostMemorySelectionAndInvalidFrameDoesNotPoison(t *testing.T) {
	p := physicalMemoryProperties{TypeCount: 3}
	p.Types[0].Flags = vkMemoryHostVisible
	p.Types[1].Flags = vkMemoryHostVisible | vkMemoryHostCoherent
	p.Types[2].Flags = vkMemoryHostVisible | vkMemoryHostCoherent
	index, err := chooseHostMemory(p, 4)
	if err != nil || index != 2 {
		t.Fatalf("memory type %d: %v", index, err)
	}
	if _, err = chooseHostMemory(p, 1); err == nil {
		t.Fatal("accepted noncoherent memory")
	}
	h := &Host{device: 1, info: DeviceInfo{PresentFence: true}}
	s := &Swapchain{host: h, swapchain: 1, extent: extent2D{2, 2}}
	if err = s.PresentRGBA(image.NewRGBA(image.Rect(0, 0, 1, 1)), false); err == nil || s.poisoned {
		t.Fatal("invalid frame accepted or poisoned swapchain")
	}
}
