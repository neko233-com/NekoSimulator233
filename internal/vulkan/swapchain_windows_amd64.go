//go:build windows && amd64

package vulkan

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	vkStructureWin32Surface = 1000009000
	vkStructureSwapchain    = 1000001000
	vkFormatUndefined       = 0
	vkFormatRGBA8           = 37
	vkFormatRGBA8SRGB       = 43
	vkFormatBGRA8           = 44
	vkFormatBGRA8SRGB       = 50
	vkColorSpaceSRGB        = 0
	vkPresentFIFO           = 2
	vkUsageTransferSrc      = 0x1
	vkUsageTransferDst      = 0x2
)

type extent2D struct{ Width, Height uint32 }
type win32SurfaceCreateInfo struct {
	SType           uint32
	Next            unsafe.Pointer
	Flags           uint32
	HInstance, HWND uintptr
}
type surfaceCapabilities struct {
	MinImageCount, MaxImageCount                                                                             uint32
	CurrentExtent, MinExtent, MaxExtent                                                                      extent2D
	MaxImageArrayLayers, SupportedTransforms, CurrentTransform, SupportedCompositeAlpha, SupportedUsageFlags uint32
}
type surfaceFormat struct{ Format, ColorSpace uint32 }
type swapchainCreateInfo struct {
	SType                                                                 uint32
	Next                                                                  unsafe.Pointer
	Flags                                                                 uint32
	Surface                                                               uint64
	MinImageCount, ImageFormat, ImageColorSpace                           uint32
	ImageExtent                                                           extent2D
	ImageArrayLayers, ImageUsage, ImageSharingMode, QueueFamilyIndexCount uint32
	QueueFamilyIndices                                                    *uint32
	PreTransform, CompositeAlpha, PresentMode, Clipped                    uint32
	OldSwapchain                                                          uint64
}
type winRect struct{ Left, Top, Right, Bottom int32 }

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	getWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	getClientRect            = user32.NewProc("GetClientRect")
	getModuleHandle          = kernel32.NewProc("GetModuleHandleW")
)

// Swapchain owns a Vulkan surface and its images, but not the caller's HWND.
// The HWND must outlive Close. Host.Close also destroys its swapchains.
type Swapchain struct {
	host                             *Host
	hwnd                             uintptr
	surface, swapchain               uint64
	destroySurface, destroySwapchain uintptr
	images                           []uint64
	extent                           extent2D
	format                           surfaceFormat
	usage                            uint32
	pending                          *presentResources
	poisoned                         bool
}

type SwapchainInfo struct {
	Width          uint32 `json:"width"`
	Height         uint32 `json:"height"`
	Format         uint32 `json:"format"`
	ImageCount     int    `json:"image_count"`
	ClearSubmitted bool   `json:"clear_submitted,omitempty"`
	PixelsVerified bool   `json:"pixels_verified,omitempty"`
}

func (s *Swapchain) Info() SwapchainInfo {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	return SwapchainInfo{Width: s.extent.Width, Height: s.extent.Height, Format: s.format.Format, ImageCount: len(s.images)}
}

func windowSize(hwnd uintptr) (extent2D, error) {
	if hwnd == 0 {
		return extent2D{}, errors.New("窗口句柄无效")
	}
	var pid uint32
	thread, _, _ := getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if thread == 0 || pid != windows.GetCurrentProcessId() {
		return extent2D{}, errors.New("窗口不存在或不属于当前进程")
	}
	var rect winRect
	ok, _, err := getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ok == 0 {
		return extent2D{}, fmt.Errorf("读取窗口大小失败：%w", err)
	}
	w, h := int64(rect.Right)-int64(rect.Left), int64(rect.Bottom)-int64(rect.Top)
	if w <= 0 || h <= 0 || w > math.MaxUint32 || h > math.MaxUint32 {
		return extent2D{}, errors.New("窗口不可见或大小无效")
	}
	return extent2D{Width: uint32(w), Height: uint32(h)}, nil
}

func chooseSurfaceFormat(formats []surfaceFormat) (surfaceFormat, error) {
	if len(formats) == 1 && formats[0].Format == vkFormatUndefined && formats[0].ColorSpace == vkColorSpaceSRGB {
		return surfaceFormat{vkFormatBGRA8, vkColorSpaceSRGB}, nil
	}
	for _, preferred := range []uint32{vkFormatBGRA8, vkFormatRGBA8, vkFormatBGRA8SRGB, vkFormatRGBA8SRGB} {
		for _, f := range formats {
			if f.Format == preferred && f.ColorSpace == vkColorSpaceSRGB {
				return f, nil
			}
		}
	}
	return surfaceFormat{}, errors.New("Vulkan 表面不支持所需的 8 位颜色格式")
}

func selectSwapchainSettings(c surfaceCapabilities, window extent2D) (uint32, extent2D, uint32, error) {
	if c.MinImageCount == 0 || (c.MaxImageCount != 0 && c.MaxImageCount < c.MinImageCount) || c.MaxImageArrayLayers < 1 || c.SupportedUsageFlags&vkUsageTransferDst == 0 {
		return 0, extent2D{}, 0, errors.New("Vulkan 表面不支持所需的交换链图像")
	}
	if c.MinExtent.Width > c.MaxExtent.Width || c.MinExtent.Height > c.MaxExtent.Height {
		return 0, extent2D{}, 0, errors.New("Vulkan 表面尺寸范围无效")
	}
	if c.CurrentTransform == 0 || c.CurrentTransform&(c.CurrentTransform-1) != 0 || c.SupportedTransforms&c.CurrentTransform == 0 {
		return 0, extent2D{}, 0, errors.New("Vulkan 表面方向无效")
	}
	e := c.CurrentExtent
	if e.Width == math.MaxUint32 && e.Height == math.MaxUint32 {
		e = extent2D{Width: max(c.MinExtent.Width, min(c.MaxExtent.Width, window.Width)), Height: max(c.MinExtent.Height, min(c.MaxExtent.Height, window.Height))}
	}
	if e.Width == 0 || e.Height == 0 || e.Width == math.MaxUint32 || e.Height == math.MaxUint32 {
		return 0, extent2D{}, 0, errors.New("窗口当前无法呈现")
	}
	if e.Width < c.MinExtent.Width || e.Width > c.MaxExtent.Width || e.Height < c.MinExtent.Height || e.Height > c.MaxExtent.Height {
		return 0, extent2D{}, 0, errors.New("Vulkan 表面尺寸超出支持范围")
	}
	count := c.MinImageCount
	if count < math.MaxUint32 && (c.MaxImageCount == 0 || count < c.MaxImageCount) {
		count++
	}
	for _, alpha := range []uint32{1, 2, 4, 8} {
		if c.SupportedCompositeAlpha&alpha != 0 {
			return count, e, alpha, nil
		}
	}
	return 0, extent2D{}, 0, errors.New("Vulkan 表面没有可用的透明度模式")
}

func enumerateSurfaceFormats(proc, physical uintptr, surface uint64) ([]surfaceFormat, error) {
	for attempt := 0; attempt < maxEnumerationAttempts; attempt++ {
		var count uint32
		r, err := call(proc, physical, uintptr(surface), uintptr(unsafe.Pointer(&count)), 0)
		if err != nil {
			return nil, err
		}
		if err = result("vkGetPhysicalDeviceSurfaceFormatsKHR", r); err != nil {
			return nil, err
		}
		if count > maxDriverEntries {
			return nil, errors.New("Vulkan 表面格式数量异常")
		}
		if count == 0 {
			return nil, nil
		}
		items := make([]surfaceFormat, count)
		r, err = call(proc, physical, uintptr(surface), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&items[0])))
		runtime.KeepAlive(items)
		if err != nil {
			return nil, err
		}
		if int32(r) == vkIncomplete || count > uint32(len(items)) {
			continue
		}
		if err = result("vkGetPhysicalDeviceSurfaceFormatsKHR", r); err != nil {
			return nil, err
		}
		return items[:count], nil
	}
	return nil, errors.New("Vulkan 表面格式持续变化")
}

func enumerateSwapchainImages(proc, device uintptr, swapchain uint64) ([]uint64, error) {
	for attempt := 0; attempt < maxEnumerationAttempts; attempt++ {
		var count uint32
		r, err := call(proc, device, uintptr(swapchain), uintptr(unsafe.Pointer(&count)), 0)
		if err != nil {
			return nil, err
		}
		if err = result("vkGetSwapchainImagesKHR", r); err != nil {
			return nil, err
		}
		if count > maxDriverEntries {
			return nil, errors.New("Vulkan 交换链图像数量异常")
		}
		if count == 0 {
			return nil, errors.New("Vulkan 交换链没有图像")
		}
		items := make([]uint64, count)
		r, err = call(proc, device, uintptr(swapchain), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&items[0])))
		runtime.KeepAlive(items)
		if err != nil {
			return nil, err
		}
		if int32(r) == vkIncomplete || count > uint32(len(items)) {
			continue
		}
		if err = result("vkGetSwapchainImagesKHR", r); err != nil {
			return nil, err
		}
		return items[:count], nil
	}
	return nil, errors.New("Vulkan 交换链图像持续变化")
}

// CreateSwapchain creates a FIFO transfer-destination swapchain for an HWND
// owned by the current process. It does not submit or present any frames.
func (h *Host) CreateSwapchain(hwnd uintptr) (_ *Swapchain, err error) {
	window, err := windowSize(hwnd)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.device == 0 || h.instance == 0 {
		return nil, errors.New("Vulkan 设备已关闭")
	}
	for existing := range h.swapchains {
		if existing.hwnd == hwnd {
			return nil, errors.New("窗口已有 Vulkan 交换链")
		}
	}
	s := &Swapchain{host: h, hwnd: hwnd}
	defer func() {
		if err != nil {
			_ = s.closeLocked()
		}
	}()
	get := h.getInstanceProcAddr
	createSurface, e := getInstanceProc(get, h.instance, "vkCreateWin32SurfaceKHR")
	if e != nil {
		return nil, e
	}
	s.destroySurface, e = getInstanceProc(get, h.instance, "vkDestroySurfaceKHR")
	if e != nil {
		return nil, e
	}
	getSupport, e := getInstanceProc(get, h.instance, "vkGetPhysicalDeviceSurfaceSupportKHR")
	if e != nil {
		return nil, e
	}
	getCaps, e := getInstanceProc(get, h.instance, "vkGetPhysicalDeviceSurfaceCapabilitiesKHR")
	if e != nil {
		return nil, e
	}
	getFormats, e := getInstanceProc(get, h.instance, "vkGetPhysicalDeviceSurfaceFormatsKHR")
	if e != nil {
		return nil, e
	}
	createSwapchain, e := getInstanceProc(get, h.instance, "vkCreateSwapchainKHR")
	if e != nil {
		return nil, e
	}
	s.destroySwapchain, e = getInstanceProc(get, h.instance, "vkDestroySwapchainKHR")
	if e != nil {
		return nil, e
	}
	getImages, e := getInstanceProc(get, h.instance, "vkGetSwapchainImagesKHR")
	if e != nil {
		return nil, e
	}
	module, _, e := getModuleHandle.Call(0)
	if module == 0 {
		return nil, fmt.Errorf("获取进程模块失败：%w", e)
	}
	ci := win32SurfaceCreateInfo{SType: vkStructureWin32Surface, HInstance: module, HWND: hwnd}
	var surface uint64
	r, e := call(createSurface, h.instance, uintptr(unsafe.Pointer(&ci)), 0, uintptr(unsafe.Pointer(&surface)))
	runtime.KeepAlive(ci)
	if e != nil {
		return nil, e
	}
	if e = result("vkCreateWin32SurfaceKHR", r); e != nil {
		return nil, e
	}
	if surface == 0 {
		return nil, errors.New("Vulkan 返回空窗口表面")
	}
	s.surface = surface
	var supported uint32
	r, e = call(getSupport, h.physical, uintptr(h.info.GraphicsQueueFamily), uintptr(s.surface), uintptr(unsafe.Pointer(&supported)))
	if e != nil {
		return nil, e
	}
	if e = result("vkGetPhysicalDeviceSurfaceSupportKHR", r); e != nil {
		return nil, e
	}
	if supported == 0 {
		return nil, errors.New("所选 Vulkan 图形队列不支持此窗口呈现")
	}
	var caps surfaceCapabilities
	r, e = call(getCaps, h.physical, uintptr(s.surface), uintptr(unsafe.Pointer(&caps)))
	if e != nil {
		return nil, e
	}
	if e = result("vkGetPhysicalDeviceSurfaceCapabilitiesKHR", r); e != nil {
		return nil, e
	}
	count, extent, alpha, e := selectSwapchainSettings(caps, window)
	if e != nil {
		return nil, e
	}
	formats, e := enumerateSurfaceFormats(getFormats, h.physical, s.surface)
	if e != nil {
		return nil, e
	}
	format, e := chooseSurfaceFormat(formats)
	if e != nil {
		return nil, e
	}
	usage := uint32(vkUsageTransferDst)
	if caps.SupportedUsageFlags&vkUsageTransferSrc != 0 {
		usage |= vkUsageTransferSrc
	}
	info := swapchainCreateInfo{SType: vkStructureSwapchain, Surface: s.surface, MinImageCount: count, ImageFormat: format.Format, ImageColorSpace: format.ColorSpace, ImageExtent: extent, ImageArrayLayers: 1, ImageUsage: usage, PreTransform: caps.CurrentTransform, CompositeAlpha: alpha, PresentMode: vkPresentFIFO, Clipped: 1}
	var chain uint64
	r, e = call(createSwapchain, h.device, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&chain)))
	runtime.KeepAlive(info)
	if e != nil {
		return nil, e
	}
	if e = result("vkCreateSwapchainKHR", r); e != nil {
		return nil, e
	}
	if chain == 0 {
		return nil, errors.New("Vulkan 返回空交换链")
	}
	s.swapchain = chain
	s.images, e = enumerateSwapchainImages(getImages, h.device, s.swapchain)
	if e != nil {
		return nil, e
	}
	s.extent, s.format, s.usage = extent, format, usage
	if h.swapchains == nil {
		h.swapchains = make(map[*Swapchain]struct{})
	}
	h.swapchains[s] = struct{}{}
	return s, nil
}

func (s *Swapchain) closeLocked() error {
	if s.pending != nil {
		if err := s.pending.releaseIfSafe(s.host); err != nil {
			return err
		}
		s.pending = nil
	}
	if s.swapchain != 0 {
		_, _ = call(s.destroySwapchain, s.host.device, uintptr(s.swapchain), 0)
		s.swapchain = 0
	}
	if s.surface != 0 {
		_, _ = call(s.destroySurface, s.host.instance, uintptr(s.surface), 0)
		s.surface = 0
	}
	s.images = nil
	delete(s.host.swapchains, s)
	return nil
}
func (s *Swapchain) Close() error {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	return s.closeLocked()
}
