//go:build windows && amd64

package vulkan

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

const (
	vkFormatBlitSrc     = 0x400
	vkFormatBlitDst     = 0x800
	vkMemoryDeviceLocal = 1
)

type imageCreateInfo struct {
	SType                                                                              uint32
	Next                                                                               unsafe.Pointer
	Flags, ImageType, Format                                                           uint32
	Extent                                                                             extent3D
	MipLevels, ArrayLayers, Samples, Tiling, Usage, SharingMode, QueueFamilyIndexCount uint32
	QueueFamilyIndices                                                                 *uint32
	InitialLayout                                                                      uint32
}
type formatProperties struct{ Linear, Optimal, Buffer uint32 }
type imageBlit struct {
	SrcSubresource imageSubresourceLayers
	SrcOffsets     [2]offset3D
	DstSubresource imageSubresourceLayers
	DstOffsets     [2]offset3D
}

type blitProcs struct {
	createImage, destroyImage, requirements, bindImage, formatProperties, blit uintptr
	memory                                                                     stagingProcs
}

func (h *Host) blitProcs() (blitProcs, error) {
	var p blitProcs
	var err error
	p.memory, err = h.stagingProcs()
	if err != nil {
		return p, err
	}
	for _, entry := range []struct {
		name string
		dst  *uintptr
	}{
		{"vkCreateImage", &p.createImage}, {"vkDestroyImage", &p.destroyImage}, {"vkGetImageMemoryRequirements", &p.requirements},
		{"vkBindImageMemory", &p.bindImage}, {"vkGetPhysicalDeviceFormatProperties", &p.formatProperties}, {"vkCmdBlitImage", &p.blit},
	} {
		*entry.dst, err = getInstanceProc(h.getInstanceProcAddr, h.instance, entry.name)
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

func supportsBlit(p blitProcs, h *Host, format uint32) bool {
	var props formatProperties
	if _, err := call(p.formatProperties, h.physical, uintptr(format), uintptr(unsafe.Pointer(&props))); err != nil {
		return false
	}
	return props.Optimal&(vkFormatBlitSrc|vkFormatBlitDst) == vkFormatBlitSrc|vkFormatBlitDst
}

func chooseImageMemory(p physicalMemoryProperties, bits uint32) (uint32, error) {
	if p.TypeCount > uint32(len(p.Types)) {
		return 0, errors.New("Vulkan 内存类型数量无效")
	}
	var first uint32
	found := false
	for i := uint32(0); i < p.TypeCount; i++ {
		if bits&(uint32(1)<<i) == 0 {
			continue
		}
		if !found {
			first, found = i, true
		}
		if p.Types[i].Flags&vkMemoryDeviceLocal != 0 {
			return i, nil
		}
	}
	if !found {
		return 0, errors.New("Vulkan 没有兼容的图像内存类型")
	}
	return first, nil
}

type blitImage struct {
	procs                 blitProcs
	image, memory         uint64
	width, height, format uint32
}

func (b *blitImage) compatible(width, height, format uint32) bool {
	return b != nil && b.image != 0 && b.memory != 0 && b.width == width && b.height == height && b.format == format
}

func (b *blitImage) create(h *Host, p blitProcs, width, height, format uint32) error {
	b.procs, b.width, b.height, b.format = p, width, height, format
	ci := imageCreateInfo{SType: 14, ImageType: 1, Format: format, Extent: extent3D{Width: width, Height: height, Depth: 1}, MipLevels: 1, ArrayLayers: 1, Samples: 1, Usage: vkUsageTransferSrc | vkUsageTransferDst}
	var image uint64
	r, err := call(p.createImage, h.device, uintptr(unsafe.Pointer(&ci)), 0, uintptr(unsafe.Pointer(&image)))
	if err != nil {
		return err
	}
	if err = result("vkCreateImage", r); err != nil {
		return err
	}
	if image == 0 {
		return errors.New("Vulkan 返回空图像")
	}
	b.image = image
	var req memoryRequirements
	if _, err := call(p.requirements, h.device, uintptr(image), uintptr(unsafe.Pointer(&req))); err != nil {
		return err
	}
	if req.Size == 0 {
		return errors.New("Vulkan 图像内存大小无效")
	}
	var props physicalMemoryProperties
	if _, err := call(p.memory.memoryProperties, h.physical, uintptr(unsafe.Pointer(&props))); err != nil {
		return err
	}
	index, err := chooseImageMemory(props, req.MemoryTypeBits)
	if err != nil {
		return err
	}
	alloc := memoryAllocateInfo{SType: 5, Size: req.Size, MemoryTypeIndex: index}
	var memory uint64
	r, err = call(p.memory.allocate, h.device, uintptr(unsafe.Pointer(&alloc)), 0, uintptr(unsafe.Pointer(&memory)))
	if err != nil {
		return err
	}
	if err = result("vkAllocateMemory", r); err != nil {
		return err
	}
	if memory == 0 {
		return errors.New("Vulkan 返回空图像内存")
	}
	b.memory = memory
	r, err = call(p.bindImage, h.device, uintptr(image), uintptr(memory), 0)
	if err != nil {
		return err
	}
	if err = result("vkBindImageMemory", r); err != nil {
		return fmt.Errorf("绑定缩放图像失败：%w", err)
	}
	return nil
}

func (b *blitImage) release(h *Host) {
	if b.image != 0 {
		_, _ = call(b.procs.destroyImage, h.device, uintptr(b.image), 0)
		b.image = 0
	}
	if b.memory != 0 {
		_, _ = call(b.procs.memory.free, h.device, uintptr(b.memory), 0)
		b.memory = 0
	}
}

func (op *presentResources) ensureBlitImage(h *Host, p blitProcs, width, height, format uint32) error {
	if op.scaled != nil && !op.scaled.compatible(width, height, format) {
		op.scaled.release(h)
		op.scaled = nil
	}
	if op.scaled == nil {
		op.scaled = &blitImage{}
		if err := op.scaled.create(h, p, width, height, format); err != nil {
			return err
		}
	}
	return nil
}

// recordScaledFrame is used only for integer upscales, so the nearest-filter
// image and existing pointer-coordinate mapping select the same pixels.
func recordScaledFrame(op *presentResources, cmd uintptr, dst uint64, rng *imageSubresourceRange, fit *fitPlan) error {
	src := op.scaled
	barrier := imageMemoryBarrier{SType: 45, DstAccess: vkAccessTransferWrite, OldLayout: vkLayoutUndefined, NewLayout: vkLayoutTransferDst, SrcQueueFamily: vkIgnoredQueue, DstQueueFamily: vkIgnoredQueue, Image: src.image, Range: *rng}
	if _, err := call(op.procs.barrier, cmd, vkStageTop, vkStageTransfer, 0, 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&barrier))); err != nil {
		return err
	}
	region := bufferImageCopy{Subresource: imageSubresourceLayers{AspectMask: 1, LayerCount: 1}, Extent: extent3D{Width: src.width, Height: src.height, Depth: 1}}
	if _, err := call(op.staging.procs.copyToImage, cmd, uintptr(op.staging.buffer), uintptr(src.image), vkLayoutTransferDst, 1, uintptr(unsafe.Pointer(&region))); err != nil {
		return err
	}
	barrier.SrcAccess, barrier.DstAccess = vkAccessTransferWrite, vkAccessTransferRead
	barrier.OldLayout, barrier.NewLayout = vkLayoutTransferDst, vkLayoutTransferSrc
	if _, err := call(op.procs.barrier, cmd, vkStageTransfer, vkStageTransfer, 0, 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&barrier))); err != nil {
		return err
	}
	black := [4]float32{0, 0, 0, 1}
	if _, err := call(op.procs.clear, cmd, uintptr(dst), vkLayoutTransferDst, uintptr(unsafe.Pointer(&black[0])), 1, uintptr(unsafe.Pointer(rng))); err != nil {
		return err
	}
	barrier = imageMemoryBarrier{SType: 45, SrcAccess: vkAccessTransferWrite, DstAccess: vkAccessTransferWrite, OldLayout: vkLayoutTransferDst, NewLayout: vkLayoutTransferDst, SrcQueueFamily: vkIgnoredQueue, DstQueueFamily: vkIgnoredQueue, Image: dst, Range: *rng}
	if _, err := call(op.procs.barrier, cmd, vkStageTransfer, vkStageTransfer, 0, 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&barrier))); err != nil {
		return err
	}
	blit := imageBlit{SrcSubresource: imageSubresourceLayers{AspectMask: 1, LayerCount: 1}, DstSubresource: imageSubresourceLayers{AspectMask: 1, LayerCount: 1}}
	blit.SrcOffsets[1] = offset3D{X: int32(fit.sw), Y: int32(fit.sh), Z: 1}
	blit.DstOffsets[0] = offset3D{X: int32(fit.left), Y: int32(fit.top)}
	blit.DstOffsets[1] = offset3D{X: int32(fit.left + fit.fw), Y: int32(fit.top + fit.fh), Z: 1}
	_, err := call(src.procs.blit, cmd, uintptr(src.image), vkLayoutTransferSrc, uintptr(dst), vkLayoutTransferDst, 1, uintptr(unsafe.Pointer(&blit)), 0)
	runtime.KeepAlive(region)
	runtime.KeepAlive(black)
	runtime.KeepAlive(blit)
	return err
}
