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
	verify         bool
	mapped         unsafe.Pointer
}

func (b *stagingBuffer) compatible(size int, verify bool) bool {
	return b != nil && b.buffer != 0 && b.memory != 0 && b.mapped != nil && b.size == size && (!verify || b.verify)
}

// The previous command pool has already been reset before this is called,
// so no executable or pending command buffer still references an old buffer.
func (op *presentResources) ensureStaging(h *Host, size int, verify bool, create func(*stagingBuffer, *Host, int, bool) error) error {
	if op.staging != nil && !op.staging.compatible(size, verify) {
		op.staging.release(h)
		op.staging = nil
	}
	if op.staging == nil {
		op.staging = &stagingBuffer{}
		if err := create(op.staging, h, size, verify); err != nil {
			return err
		}
	}
	return nil
}

func (b *stagingBuffer) release(h *Host) {
	if b.mapped != nil {
		_, _ = call(b.procs.unmapMemory, h.device, uintptr(b.memory))
		b.mapped = nil
	}
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
	b.verify = verify
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
	if e = result("vkBindBufferMemory", r); e != nil {
		return e
	}
	var mapped unsafe.Pointer
	r, e = call(p.mapMemory, h.device, uintptr(memory), 0, uintptr(size), 0, uintptr(unsafe.Pointer(&mapped)))
	if e != nil {
		return e
	}
	if e = result("vkMapMemory", r); e != nil {
		return e
	}
	if mapped == nil {
		_, _ = call(p.unmapMemory, h.device, uintptr(memory))
		return errors.New("Vulkan 返回空映射")
	}
	b.mapped = mapped
	return nil
}

func (b *stagingBuffer) withMapped(h *Host, fn func([]byte) error) error {
	if b.mapped == nil {
		return errors.New("Vulkan 缓冲区未映射")
	}
	return fn(unsafe.Slice((*byte)(b.mapped), b.size))
}

func frameSize(frame *image.RGBA, extent extent2D) (int, error) {
	if frame == nil || frame.Rect.Dx() <= 0 || frame.Rect.Dy() <= 0 || uint64(frame.Rect.Dx()) != uint64(extent.Width) || uint64(frame.Rect.Dy()) != uint64(extent.Height) {
		return 0, errors.New("画面尺寸必须与 Vulkan 交换链一致")
	}
	return validateFrame(frame)
}

func validateFrame(frame *image.RGBA) (int, error) {
	if frame == nil || frame.Rect.Dx() <= 0 || frame.Rect.Dy() <= 0 {
		return 0, errors.New("画面尺寸无效")
	}
	width, height := uint64(frame.Rect.Dx()), uint64(frame.Rect.Dy())
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

// fittedFrame packs an aspect-preserving, nearest-neighbor image directly into
// the mapped staging buffer, including opaque black bars. No extra full-size
// CPU image is allocated. GPU scaling is a future optimization.
func fittedFrame(dst []byte, frame *image.RGBA, extent extent2D, format uint32, compare bool) error {
	if _, err := validateFrame(frame); err != nil {
		return err
	}
	w, h := uint64(extent.Width), uint64(extent.Height)
	if w == 0 || h == 0 || w > 8192 || h > 8192 || w*h > 32*1024*1024 || uint64(len(dst)) != w*h*4 {
		return errors.New("目标画面尺寸无效")
	}
	bgra := format == vkFormatBGRA8 || format == vkFormatBGRA8SRGB
	if !bgra && format != vkFormatRGBA8 && format != vkFormatRGBA8SRGB {
		return errors.New("不支持的 Vulkan 像素格式")
	}
	sw, sh := uint64(frame.Rect.Dx()), uint64(frame.Rect.Dy())
	// Input is bounded here to keep all intermediate products well within uint64.
	if sw > 8192 || sh > 8192 || sw*sh > 32*1024*1024 {
		return errors.New("源画面尺寸无效")
	}
	if w == sw && h == sh {
		return frameBytes(dst, frame, format, compare)
	}
	fw, fh := w, h
	if w*sh <= h*sw {
		fh = max(uint64(1), w*sh/sw)
	} else {
		fw = max(uint64(1), h*sw/sh)
	}
	left, top := (w-fw)/2, (h-fh)/2
	for y := uint64(0); y < h; y++ {
		for x := uint64(0); x < w; x++ {
			var r, g, b, a byte = 0, 0, 0, 255
			if x >= left && x < left+fw && y >= top && y < top+fh {
				sx, sy := (x-left)*sw/fw, (y-top)*sh/fh
				i := int(sy)*frame.Stride + int(sx)*4
				r, g, b, a = frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2], frame.Pix[i+3]
				if bgra {
					r, b = b, r
				}
			}
			i := int((y*w + x) * 4)
			if compare {
				if dst[i] != r || dst[i+1] != g || dst[i+2] != b || dst[i+3] != a {
					return fmt.Errorf("GPU 画面回读不一致：(%d,%d)", x, y)
				}
			} else {
				dst[i], dst[i+1], dst[i+2], dst[i+3] = r, g, b, a
			}
		}
	}
	return nil
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
	return s.presentRGBA(frame, verify, false)
}

// PresentFittedRGBA preserves aspect ratio and fills the rest with black.
// CPU scaling is synchronous and is not a zero-copy or low-latency path.
func (s *Swapchain) PresentFittedRGBA(frame *image.RGBA, verify bool) error {
	return s.presentRGBA(frame, verify, true)
}

func (s *Swapchain) presentRGBA(frame *image.RGBA, verify, fit bool) error {
	var size int
	pack := func(data []byte, compare bool) error {
		if fit {
			if compare {
				return fittedFrame(data, frame, s.extent, s.format.Format, true)
			}
			return s.fit.pack(data, frame, s.format.Format)
		}
		return frameBytes(data, frame, s.format.Format, compare)
	}
	hooks := frameHooks{
		validate: func(_ *Host) error {
			var err error
			if fit {
				_, err = validateFrame(frame)
				w, h := uint64(s.extent.Width), uint64(s.extent.Height)
				if err == nil && (w == 0 || h == 0 || w > 8192 || h > 8192 || w*h > 32*1024*1024 || frame.Rect.Dx() > 8192 || frame.Rect.Dy() > 8192 || uint64(frame.Rect.Dx())*uint64(frame.Rect.Dy()) > 32*1024*1024) {
					err = errors.New("画面尺寸无效")
				}
				if err == nil {
					size = int(w * h * 4)
				}
			} else {
				size, err = frameSize(frame, s.extent)
			}
			if err != nil {
				return err
			}
			if verify && s.usage&vkUsageTransferSrc == 0 {
				return errors.New("Vulkan 表面不支持图像回读")
			}
			if fit {
				if err := s.ensureFitPlan(frame); err != nil {
					return err
				}
			}
			return nil
		},
		prepare: func(h *Host, op *presentResources) error {
			if err := op.ensureStaging(h, size, verify, (*stagingBuffer).create); err != nil {
				return err
			}
			return op.staging.withMapped(h, func(data []byte) error { return pack(data, false) })
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
			return op.staging.withMapped(h, func(data []byte) error { return pack(data, true) })
		}
	}
	return s.presentFrame(hooks)
}
