//go:build windows && amd64

package vulkan

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"unsafe"
)

const (
	vkNotReady            = 1
	vkTimeout             = 2
	vkDeviceLost          = -4
	vkSuboptimal          = 1000001003
	vkOutOfDate           = -1000001004
	vkSurfaceLost         = -1000000000
	vkFullscreenLost      = -1000255000
	vkStageTop            = 0x1
	vkStageTransfer       = 0x1000
	vkStageBottom         = 0x2000
	vkAccessTransferWrite = 0x1000
	vkLayoutUndefined     = 0
	vkLayoutTransferDst   = 7
	vkLayoutPresent       = 1000001002
	vkIgnoredQueue        = ^uint32(0)
	presentWaitNS         = 2_000_000_000
)

var ErrPresentBusy = errors.New("Vulkan 图像仍在使用，资源未释放；请稍后重试")
var ErrSwapchainOutOfDate = errors.New("Vulkan 交换链需要重建")
var ErrSurfaceLost = errors.New("Vulkan 窗口表面已失效")

func swapchainResult(name string, value int32) error {
	switch value {
	case vkOutOfDate, vkSuboptimal:
		return fmt.Errorf("%s：%w (VkResult %d)", name, ErrSwapchainOutOfDate, value)
	case vkSurfaceLost, vkFullscreenLost:
		return fmt.Errorf("%s：%w (VkResult %d)", name, ErrSurfaceLost, value)
	default:
		return fmt.Errorf("%s 失败：VkResult %d", name, value)
	}
}

type fenceCreateInfo struct {
	SType uint32
	Next  unsafe.Pointer
	Flags uint32
}
type semaphoreCreateInfo struct {
	SType uint32
	Next  unsafe.Pointer
	Flags uint32
}
type commandPoolCreateInfo struct {
	SType                   uint32
	Next                    unsafe.Pointer
	Flags, QueueFamilyIndex uint32
}
type commandBufferAllocateInfo struct {
	SType        uint32
	Next         unsafe.Pointer
	Pool         uint64
	Level, Count uint32
}
type commandBufferBeginInfo struct {
	SType       uint32
	Next        unsafe.Pointer
	Flags       uint32
	Inheritance unsafe.Pointer
}
type imageSubresourceRange struct{ AspectMask, BaseMipLevel, LevelCount, BaseArrayLayer, LayerCount uint32 }
type imageMemoryBarrier struct {
	SType                                                                      uint32
	Next                                                                       unsafe.Pointer
	SrcAccess, DstAccess, OldLayout, NewLayout, SrcQueueFamily, DstQueueFamily uint32
	Image                                                                      uint64
	Range                                                                      imageSubresourceRange
}
type submitInfo struct {
	SType            uint32
	Next             unsafe.Pointer
	WaitCount        uint32
	WaitSemaphores   *uint64
	WaitStages       *uint32
	CommandCount     uint32
	Commands         *uintptr
	SignalCount      uint32
	SignalSemaphores *uint64
}
type presentFenceInfo struct {
	SType  uint32
	Next   unsafe.Pointer
	Count  uint32
	Fences *uint64
}
type presentInfo struct {
	SType          uint32
	Next           unsafe.Pointer
	WaitCount      uint32
	WaitSemaphores *uint64
	SwapchainCount uint32
	Swapchains     *uint64
	ImageIndices   *uint32
	Results        *int32
}

type presentProcs struct {
	createFence, destroyFence, fenceStatus, waitFences                  uintptr
	resetFences                                                         uintptr
	createSemaphore, destroySemaphore                                   uintptr
	createPool, destroyPool, allocateCommands, beginCommand, endCommand uintptr
	resetPool                                                           uintptr
	barrier, clear, acquire, submit, present                            uintptr
}

func (h *Host) presentProcs() (presentProcs, error) {
	var p presentProcs
	for _, entry := range []struct {
		name string
		dst  *uintptr
	}{
		{"vkCreateFence", &p.createFence}, {"vkDestroyFence", &p.destroyFence}, {"vkGetFenceStatus", &p.fenceStatus}, {"vkWaitForFences", &p.waitFences}, {"vkResetFences", &p.resetFences},
		{"vkCreateSemaphore", &p.createSemaphore}, {"vkDestroySemaphore", &p.destroySemaphore},
		{"vkCreateCommandPool", &p.createPool}, {"vkDestroyCommandPool", &p.destroyPool}, {"vkAllocateCommandBuffers", &p.allocateCommands}, {"vkBeginCommandBuffer", &p.beginCommand}, {"vkEndCommandBuffer", &p.endCommand}, {"vkResetCommandPool", &p.resetPool},
		{"vkCmdPipelineBarrier", &p.barrier}, {"vkCmdClearColorImage", &p.clear}, {"vkAcquireNextImageKHR", &p.acquire}, {"vkQueueSubmit", &p.submit}, {"vkQueuePresentKHR", &p.present},
	} {
		var err error
		*entry.dst, err = getInstanceProc(h.getInstanceProcAddr, h.instance, entry.name)
		if err != nil {
			return presentProcs{}, err
		}
	}
	return p, nil
}

type presentResources struct {
	procs                                                   presentProcs
	pool, acquireFence, renderFence, presentFence, finished uint64
	command                                                 uintptr
	acquirePending, renderPending, presentPending           bool
	staging                                                 *stagingBuffer
}

func (op *presentResources) releaseIfSafe(h *Host) error {
	for _, pending := range []struct {
		active *bool
		fence  uint64
	}{{&op.acquirePending, op.acquireFence}, {&op.renderPending, op.renderFence}, {&op.presentPending, op.presentFence}} {
		if !*pending.active {
			continue
		}
		r, err := call(op.procs.fenceStatus, h.device, uintptr(pending.fence))
		if err != nil {
			return err
		}
		switch int32(r) {
		case vkSuccess, vkDeviceLost:
			*pending.active = false
		case vkNotReady:
			return ErrPresentBusy
		default:
			return fmt.Errorf("查询 Vulkan 栅栏失败：VkResult %d", int32(r))
		}
	}
	if op.staging != nil {
		op.staging.release(h)
		op.staging = nil
	}
	if op.pool != 0 {
		_, _ = call(op.procs.destroyPool, h.device, uintptr(op.pool), 0)
		op.pool = 0
		op.command = 0
	}
	if op.finished != 0 {
		_, _ = call(op.procs.destroySemaphore, h.device, uintptr(op.finished), 0)
		op.finished = 0
	}
	for _, fence := range []*uint64{&op.acquireFence, &op.renderFence, &op.presentFence} {
		if *fence != 0 {
			_, _ = call(op.procs.destroyFence, h.device, uintptr(*fence), 0)
			*fence = 0
		}
	}
	return nil
}

func waitFence(p presentProcs, device uintptr, fence *uint64) error {
	r, err := call(p.waitFences, device, 1, uintptr(unsafe.Pointer(fence)), 1, presentWaitNS)
	if err != nil {
		return err
	}
	if int32(r) == vkTimeout {
		return ErrPresentBusy
	}
	return result("vkWaitForFences", r)
}

func (op *presentResources) create(h *Host) error {
	for _, dest := range []*uint64{&op.acquireFence, &op.renderFence, &op.presentFence} {
		ci := fenceCreateInfo{SType: 8}
		var handle uint64
		r, err := call(op.procs.createFence, h.device, uintptr(unsafe.Pointer(&ci)), 0, uintptr(unsafe.Pointer(&handle)))
		if err != nil {
			return err
		}
		if err = result("vkCreateFence", r); err != nil {
			return err
		}
		if handle == 0 {
			return errors.New("Vulkan 返回空栅栏")
		}
		*dest = handle
	}
	semaphore := semaphoreCreateInfo{SType: 9}
	var handle uint64
	r, err := call(op.procs.createSemaphore, h.device, uintptr(unsafe.Pointer(&semaphore)), 0, uintptr(unsafe.Pointer(&handle)))
	if err != nil {
		return err
	}
	if err = result("vkCreateSemaphore", r); err != nil {
		return err
	}
	if handle == 0 {
		return errors.New("Vulkan 返回空信号量")
	}
	op.finished = handle
	pool := commandPoolCreateInfo{SType: 39, Flags: 1, QueueFamilyIndex: h.info.GraphicsQueueFamily}
	handle = 0
	r, err = call(op.procs.createPool, h.device, uintptr(unsafe.Pointer(&pool)), 0, uintptr(unsafe.Pointer(&handle)))
	if err != nil {
		return err
	}
	if err = result("vkCreateCommandPool", r); err != nil {
		return err
	}
	if handle == 0 {
		return errors.New("Vulkan 返回空命令池")
	}
	op.pool = handle
	alloc := commandBufferAllocateInfo{SType: 40, Pool: op.pool, Count: 1}
	r, err = call(op.procs.allocateCommands, h.device, uintptr(unsafe.Pointer(&alloc)), uintptr(unsafe.Pointer(&op.command)))
	if err != nil {
		return err
	}
	if err = result("vkAllocateCommandBuffers", r); err != nil {
		return err
	}
	if op.command == 0 {
		return errors.New("Vulkan 返回空命令缓冲区")
	}
	return nil
}

// reset is only called after all three previous fences have signaled.
func (op *presentResources) reset(h *Host) error {
	if op.acquirePending || op.renderPending || op.presentPending {
		return ErrPresentBusy
	}
	fences := [3]uint64{op.acquireFence, op.renderFence, op.presentFence}
	r, err := call(op.procs.resetFences, h.device, uintptr(len(fences)), uintptr(unsafe.Pointer(&fences[0])))
	runtime.KeepAlive(fences)
	if err != nil {
		return err
	}
	if err = result("vkResetFences", r); err != nil {
		return err
	}
	r, err = call(op.procs.resetPool, h.device, uintptr(op.pool), 0)
	if err != nil {
		return err
	}
	return result("vkResetCommandPool", r)
}

// PresentClear submits one cleared image and waits on separate acquire,
// rendering and presentation fences. It is a correctness-oriented foundation,
// not a low-latency frame pipeline. On timeout Close returns ErrPresentBusy
// without destroying resources that the GPU or presentation engine may use.
func (s *Swapchain) PresentClear(color [4]float32) (err error) {
	for _, v := range color {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < 0 || v > 1 {
			return errors.New("颜色分量必须在 0 到 1 之间")
		}
	}
	return s.presentFrame(frameHooks{record: func(_ *Host, op *presentResources, cmd uintptr, image uint64, rangeInfo *imageSubresourceRange) (uint32, uint32, error) {
		_, err := call(op.procs.clear, cmd, uintptr(image), vkLayoutTransferDst, uintptr(unsafe.Pointer(&color[0])), 1, uintptr(unsafe.Pointer(rangeInfo)))
		runtime.KeepAlive(color)
		return vkLayoutTransferDst, vkAccessTransferWrite, err
	}})
}

type frameHooks struct {
	validate func(*Host) error
	prepare  func(*Host, *presentResources) error
	record   func(*Host, *presentResources, uintptr, uint64, *imageSubresourceRange) (uint32, uint32, error)
	verify   func(*Host, *presentResources) error
}

func (s *Swapchain) presentFrame(hooks frameHooks) (err error) {
	return s.presentFrameWith(hooks, (*Host).presentProcs)
}

func (s *Swapchain) presentFrameWith(hooks frameHooks, resolve func(*Host) (presentProcs, error)) (err error) {
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.device == 0 || s.swapchain == 0 {
		return errors.New("Vulkan 交换链已关闭")
	}
	if !h.info.PresentFence {
		return errors.New("驱动不支持安全的呈现完成栅栏")
	}
	if s.poisoned || s.pending != nil {
		return errors.New("交换链需要关闭后重建")
	}
	if hooks.validate != nil {
		if err := hooks.validate(h); err != nil {
			return err
		}
	}
	op := s.reusable
	s.reusable = nil
	if op == nil {
		p, e := resolve(h)
		if e != nil {
			return e
		}
		op = &presentResources{procs: p}
	}
	defer func() {
		if err != nil {
			s.poisoned = true
			if cleanupErr := op.releaseIfSafe(h); cleanupErr != nil {
				s.pending = op
				err = errors.Join(err, cleanupErr)
			}
		} else {
			s.reusable = op
		}
	}()
	if op.pool == 0 {
		err = op.create(h)
	} else {
		err = op.reset(h)
	}
	if err != nil {
		return err
	}
	p := op.procs
	if hooks.prepare != nil {
		if err = hooks.prepare(h, op); err != nil {
			return err
		}
	}
	cmd := op.command
	var index uint32
	r, e := call(p.acquire, h.device, uintptr(s.swapchain), presentWaitNS, 0, uintptr(op.acquireFence), uintptr(unsafe.Pointer(&index)))
	if e != nil {
		return e
	}
	if int32(r) != vkSuccess && int32(r) != vkSuboptimal {
		return swapchainResult("vkAcquireNextImageKHR", int32(r))
	}
	suboptimal := int32(r) == vkSuboptimal
	op.acquirePending = true
	if err = waitFence(p, h.device, &op.acquireFence); err != nil {
		return err
	}
	op.acquirePending = false
	if int(index) >= len(s.images) {
		return errors.New("Vulkan 返回了无效的交换链图像索引")
	}
	begin := commandBufferBeginInfo{SType: 42, Flags: 1}
	r, e = call(p.beginCommand, cmd, uintptr(unsafe.Pointer(&begin)))
	if e != nil {
		return e
	}
	if e = result("vkBeginCommandBuffer", r); e != nil {
		return e
	}
	rangeInfo := imageSubresourceRange{AspectMask: 1, LevelCount: 1, LayerCount: 1}
	barrier := imageMemoryBarrier{SType: 45, DstAccess: vkAccessTransferWrite, OldLayout: vkLayoutUndefined, NewLayout: vkLayoutTransferDst, SrcQueueFamily: vkIgnoredQueue, DstQueueFamily: vkIgnoredQueue, Image: s.images[index], Range: rangeInfo}
	_, e = call(p.barrier, cmd, vkStageTop, vkStageTransfer, 0, 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&barrier)))
	if e != nil {
		return e
	}
	oldLayout, oldAccess, e := hooks.record(h, op, cmd, s.images[index], &rangeInfo)
	if e != nil {
		return e
	}
	barrier.SrcAccess = oldAccess
	barrier.DstAccess = 0
	barrier.OldLayout = oldLayout
	barrier.NewLayout = vkLayoutPresent
	_, e = call(p.barrier, cmd, vkStageTransfer, vkStageBottom, 0, 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&barrier)))
	if e != nil {
		return e
	}
	r, e = call(p.endCommand, cmd)
	if e != nil {
		return e
	}
	if e = result("vkEndCommandBuffer", r); e != nil {
		return e
	}
	submit := submitInfo{SType: 4, CommandCount: 1, Commands: &cmd, SignalCount: 1, SignalSemaphores: &op.finished}
	r, e = call(p.submit, h.queue, 1, uintptr(unsafe.Pointer(&submit)), uintptr(op.renderFence))
	runtime.KeepAlive(submit)
	if e != nil {
		return e
	}
	if e = result("vkQueueSubmit", r); e != nil {
		return e
	}
	op.renderPending = true
	if hooks.verify != nil {
		if err = waitFence(p, h.device, &op.renderFence); err != nil {
			return err
		}
		op.renderPending = false
		if err = hooks.verify(h, op); err != nil {
			return err
		}
	}
	presentFence := presentFenceInfo{SType: 1000275001, Count: 1, Fences: &op.presentFence}
	present := presentInfo{SType: 1000001001, Next: unsafe.Pointer(&presentFence), WaitCount: 1, WaitSemaphores: &op.finished, SwapchainCount: 1, Swapchains: &s.swapchain, ImageIndices: &index}
	r, e = call(p.present, h.queue, uintptr(unsafe.Pointer(&present)))
	runtime.KeepAlive(present)
	runtime.KeepAlive(presentFence)
	if e != nil {
		return e
	}
	presentResult := int32(r)
	if presentResult == vkSuccess || presentResult == vkSuboptimal || presentResult == vkOutOfDate || presentResult == vkSurfaceLost || presentResult == vkFullscreenLost {
		op.presentPending = true
	}
	if op.renderPending {
		if err = waitFence(p, h.device, &op.renderFence); err != nil {
			return err
		}
		op.renderPending = false
	}
	if op.presentPending {
		if err = waitFence(p, h.device, &op.presentFence); err != nil {
			return err
		}
		op.presentPending = false
	}
	if presentResult != vkSuccess {
		return swapchainResult("vkQueuePresentKHR", presentResult)
	}
	if suboptimal {
		return swapchainResult("vkAcquireNextImageKHR", vkSuboptimal)
	}
	return nil
}
