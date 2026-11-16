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
