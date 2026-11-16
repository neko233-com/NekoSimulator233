//go:build windows && amd64

package vulkan

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"unsafe"
)

const (
	vkBufferTransferSrc  = 1
	vkBufferTransferDst  = 2
	vkMemoryHostVisible  = 2
	vkMemoryHostCoherent = 4
	vkAccessTransferRead = 0x800
	vkAccessHostRead     = 0x2000
	vkStageHost          = 0x4000
	vkLayoutTransferSrc  = 6
)

type bufferCreateInfo struct {
	SType                                     uint32
	Next                                      unsafe.Pointer
	Flags                                     uint32
	Size                                      uint64
	Usage, SharingMode, QueueFamilyIndexCount uint32
	QueueFamilyIndices                        *uint32
}
type memoryRequirements struct {
	Size, Alignment uint64
	MemoryTypeBits  uint32
}
type memoryAllocateInfo struct {
	SType           uint32
	Next            unsafe.Pointer
	Size            uint64
	MemoryTypeIndex uint32
}
type memoryType struct{ Flags, HeapIndex uint32 }
type memoryHeap struct {
	Size  uint64
	Flags uint32
}
type physicalMemoryProperties struct {
	TypeCount uint32
	Types     [32]memoryType
	HeapCount uint32
	Heaps     [16]memoryHeap
}
type bufferMemoryBarrier struct {
	SType                                                uint32
	Next                                                 unsafe.Pointer
	SrcAccess, DstAccess, SrcQueueFamily, DstQueueFamily uint32
	Buffer, Offset, Size                                 uint64
}
type imageSubresourceLayers struct{ AspectMask, MipLevel, BaseArrayLayer, LayerCount uint32 }
type offset3D struct{ X, Y, Z int32 }
type extent3D struct{ Width, Height, Depth uint32 }
type bufferImageCopy struct {
	BufferOffset                       uint64
	BufferRowLength, BufferImageHeight uint32
	Subresource                        imageSubresourceLayers
	Offset                             offset3D
	Extent                             extent3D
}

type stagingProcs struct{ createBuffer, destroyBuffer, requirements, memoryProperties, allocate, free, bind, mapMemory, unmapMemory, copyToImage, copyFromImage uintptr }

func (h *Host) stagingProcs() (stagingProcs, error) {
	var p stagingProcs
	for _, e := range []struct {
		name string
		dst  *uintptr
	}{
		{"vkCreateBuffer", &p.createBuffer}, {"vkDestroyBuffer", &p.destroyBuffer}, {"vkGetBufferMemoryRequirements", &p.requirements},
		{"vkGetPhysicalDeviceMemoryProperties", &p.memoryProperties}, {"vkAllocateMemory", &p.allocate}, {"vkFreeMemory", &p.free}, {"vkBindBufferMemory", &p.bind},
		{"vkMapMemory", &p.mapMemory}, {"vkUnmapMemory", &p.unmapMemory}, {"vkCmdCopyBufferToImage", &p.copyToImage}, {"vkCmdCopyImageToBuffer", &p.copyFromImage},
	} {
		var err error
		*e.dst, err = getInstanceProc(h.getInstanceProcAddr, h.instance, e.name)
		if err != nil {
			return stagingProcs{}, err
		}
	}
	return p, nil
}

type stagingBuffer struct {
	procs          stagingProcs
	buffer, memory uint64
	size           int
}

func (b *stagingBuffer) release(h *Host) {
	if b.buffer != 0 {
		_, _ = call(b.procs.destroyBuffer, h.device, uintptr(b.buffer), 0)
		b.buffer = 0
	}
	if b.memory != 0 {
		_, _ = call(b.procs.free, h.device, uintptr(b.memory), 0)
		b.memory = 0
	}
}

func chooseHostMemory(p physicalMemoryProperties, bits uint32) (uint32, error) {
	if p.TypeCount > uint32(len(p.Types)) {
		return 0, errors.New("Vulkan 内存类型数量无效")
	}
	for i := uint32(0); i < p.TypeCount; i++ {
		if bits&(uint32(1)<<i) != 0 && p.Types[i].Flags&(vkMemoryHostVisible|vkMemoryHostCoherent) == (vkMemoryHostVisible|vkMemoryHostCoherent) {
			return i, nil
		}
	}
	return 0, errors.New("Vulkan 没有可映射且一致的缓冲区内存")
}

func (b *stagingBuffer) create(h *Host, size int, verify bool) error {
	p, err := h.stagingProcs()
	if err != nil {
		return err
	}
	b.procs = p
	b.size = size
	usage := uint32(vkBufferTransferSrc)
	if verify {
		usage |= vkBufferTransferDst
	}
	ci := bufferCreateInfo{SType: 12, Size: uint64(size), Usage: usage}
	var buffer uint64
	r, e := call(p.createBuffer, h.device, uintptr(unsafe.Pointer(&ci)), 0, uintptr(unsafe.Pointer(&buffer)))
	if e != nil {
		return e
	}
	if e = result("vkCreateBuffer", r); e != nil {
		return e
	}
	if buffer == 0 {
		return errors.New("Vulkan 返回空缓冲区")
	}
	b.buffer = buffer
	var req memoryRequirements
	_, e = call(p.requirements, h.device, uintptr(buffer), uintptr(unsafe.Pointer(&req)))
	if e != nil {
		return e
	}
	if req.Size < uint64(size) {
		return errors.New("Vulkan 缓冲区内存需求无效")
	}
	var properties physicalMemoryProperties
	_, e = call(p.memoryProperties, h.physical, uintptr(unsafe.Pointer(&properties)))
	if e != nil {
		return e
	}
	index, e := chooseHostMemory(properties, req.MemoryTypeBits)
	if e != nil {
		return e
	}
	alloc := memoryAllocateInfo{SType: 5, Size: req.Size, MemoryTypeIndex: index}
	var memory uint64
	r, e = call(p.allocate, h.device, uintptr(unsafe.Pointer(&alloc)), 0, uintptr(unsafe.Pointer(&memory)))
	if e != nil {
		return e
	}
	if e = result("vkAllocateMemory", r); e != nil {
		return e
	}
	if memory == 0 {
		return errors.New("Vulkan 返回空内存")
	}
	b.memory = memory
	r, e = call(p.bind, h.device, uintptr(buffer), uintptr(memory), 0)
	if e != nil {
		return e
	}
	return result("vkBindBufferMemory", r)
}

func (b *stagingBuffer) withMapped(h *Host, fn func([]byte) error) error {
	var mapped unsafe.Pointer
	r, err := call(b.procs.mapMemory, h.device, uintptr(b.memory), 0, uintptr(b.size), 0, uintptr(unsafe.Pointer(&mapped)))
	if err != nil {
		return err
	}
	if err = result("vkMapMemory", r); err != nil {
		return err
	}
	defer call(b.procs.unmapMemory, h.device, uintptr(b.memory))
	if mapped == nil {
		return errors.New("Vulkan 返回空映射")
	}
	return fn(unsafe.Slice((*byte)(mapped), b.size))
}

func frameSize(frame *image.RGBA, extent extent2D) (int, error) {
	if frame == nil || frame.Rect.Dx() <= 0 || frame.Rect.Dy() <= 0 || uint64(frame.Rect.Dx()) != uint64(extent.Width) || uint64(frame.Rect.Dy()) != uint64(extent.Height) {
		return 0, errors.New("画面尺寸必须与 Vulkan 交换链一致")
	}
	width, height := uint64(extent.Width), uint64(extent.Height)
	maxInt := uint64(^uint(0) >> 1)
	if width > maxInt/4 || height > maxInt/(width*4) {
		return 0, errors.New("画面大小超出系统范围")
	}
	row := width * 4
	if frame.Stride < 0 || uint64(frame.Stride) < row || (height > 1 && uint64(frame.Stride) > (maxInt-row)/(height-1)) || uint64(len(frame.Pix)) < (height-1)*uint64(frame.Stride)+row {
		return 0, errors.New("画面像素缓冲区无效")
	}
	return int(row * height), nil
}

func frameBytes(dst []byte, frame *image.RGBA, format uint32, compare bool) error {
	w, h := frame.Rect.Dx(), frame.Rect.Dy()
	bgra := format == vkFormatBGRA8 || format == vkFormatBGRA8SRGB
	if !bgra && format != vkFormatRGBA8 && format != vkFormatRGBA8SRGB {
		return errors.New("不支持的 Vulkan 像素格式")
	}
	for y := 0; y < h; y++ {
		src := frame.Pix[y*frame.Stride : y*frame.Stride+w*4]
		row := dst[y*w*4 : (y+1)*w*4]
		if !bgra && !compare {
			copy(row, src)
			continue
		}
		for x := 0; x < w; x++ {
			i := x * 4
			r, g, b, a := src[i], src[i+1], src[i+2], src[i+3]
			if bgra {
				r, b = b, r
			}
			if compare {
				if row[i] != r || row[i+1] != g || row[i+2] != b || row[i+3] != a {
					return fmt.Errorf("GPU 画面回读不一致：(%d,%d)", x, y)
				}
			} else {
				row[i], row[i+1], row[i+2], row[i+3] = r, g, b, a
			}
		}
	}
	return nil
}

// PresentRGBA copies a top-down RGBA frame into a same-size swapchain image.
// The caller must not mutate frame until the call returns. verify performs a
// GPU image-to-buffer round trip and is intended for diagnostics only.
func (s *Swapchain) PresentRGBA(frame *image.RGBA, verify bool) error {
	var size int
	hooks := frameHooks{
		validate: func(_ *Host) error {
			var err error
			size, err = frameSize(frame, s.extent)
			if err != nil {
				return err
			}
			if verify && s.usage&vkUsageTransferSrc == 0 {
				return errors.New("Vulkan 表面不支持图像回读")
			}
			return nil
		},
		prepare: func(h *Host, op *presentResources) error {
			op.staging = &stagingBuffer{}
			if err := op.staging.create(h, size, verify); err != nil {
				return err
			}
			return op.staging.withMapped(h, func(data []byte) error { return frameBytes(data, frame, s.format.Format, false) })
		},
		record: func(h *Host, op *presentResources, cmd uintptr, img uint64, rng *imageSubresourceRange) (uint32, uint32, error) {
			b := op.staging
			region := bufferImageCopy{Subresource: imageSubresourceLayers{AspectMask: 1, LayerCount: 1}, Extent: extent3D{Width: s.extent.Width, Height: s.extent.Height, Depth: 1}}
			_, err := call(b.procs.copyToImage, cmd, uintptr(b.buffer), uintptr(img), vkLayoutTransferDst, 1, uintptr(unsafe.Pointer(&region)))
			if err != nil {
				return 0, 0, err
			}
			if !verify {
				return vkLayoutTransferDst, vkAccessTransferWrite, nil
			}
			imageBarrier := imageMemoryBarrier{SType: 45, SrcAccess: vkAccessTransferWrite, DstAccess: vkAccessTransferRead, OldLayout: vkLayoutTransferDst, NewLayout: vkLayoutTransferSrc, SrcQueueFamily: vkIgnoredQueue, DstQueueFamily: vkIgnoredQueue, Image: img, Range: *rng}
			bufferBarrier := bufferMemoryBarrier{SType: 44, SrcAccess: vkAccessTransferRead, DstAccess: vkAccessTransferWrite, SrcQueueFamily: vkIgnoredQueue, DstQueueFamily: vkIgnoredQueue, Buffer: b.buffer, Size: uint64(b.size)}
			_, err = call(op.procs.barrier, cmd, vkStageTransfer, vkStageTransfer, 0, 0, 0, 1, uintptr(unsafe.Pointer(&bufferBarrier)), 1, uintptr(unsafe.Pointer(&imageBarrier)))
			if err != nil {
				return 0, 0, err
			}
			_, err = call(b.procs.copyFromImage, cmd, uintptr(img), vkLayoutTransferSrc, uintptr(b.buffer), 1, uintptr(unsafe.Pointer(&region)))
			if err != nil {
				return 0, 0, err
			}
			bufferBarrier.SrcAccess = vkAccessTransferWrite
			bufferBarrier.DstAccess = vkAccessHostRead
			_, err = call(op.procs.barrier, cmd, vkStageTransfer, vkStageHost, 0, 0, 0, 1, uintptr(unsafe.Pointer(&bufferBarrier)), 0, 0)
			runtime.KeepAlive(region)
			return vkLayoutTransferSrc, vkAccessTransferRead, err
		},
	}
	if verify {
		hooks.verify = func(h *Host, op *presentResources) error {
			return op.staging.withMapped(h, func(data []byte) error { return frameBytes(data, frame, s.format.Format, true) })
		}
	}
	return s.presentFrame(hooks)
}
