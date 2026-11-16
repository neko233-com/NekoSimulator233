//go:build windows && amd64

package vulkan

import (
	"bytes"
	"image"
	"testing"
	"unsafe"
)

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
