//go:build windows && amd64

// Package vulkan owns the Windows Vulkan host device used by the future
// renderer. It does not load MuMu libraries or create an Android VM.
package vulkan

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	vkSuccess       = 0
	vkIncomplete    = 5
	vkGraphics      = 1
	vkDiscreteGPU   = 2
	vkIntegratedGPU = 1
	vkAPIVersion10  = 1 << 22
)

// These declarations match the Vulkan 1.0 Win64 ABI. Pointer fields remain Go
// pointers so their backing values stay alive across the native call.
type applicationInfo struct {
	SType                     uint32
	Next                      unsafe.Pointer
	ApplicationName           *byte
	ApplicationVersion        uint32
	EngineName                *byte
	EngineVersion, APIVersion uint32
}
type instanceCreateInfo struct {
	SType           uint32
	Next            unsafe.Pointer
	Flags           uint32
	ApplicationInfo *applicationInfo
	LayerCount      uint32
	LayerNames      **byte
	ExtensionCount  uint32
	ExtensionNames  **byte
}
type queueCreateInfo struct {
	SType                          uint32
	Next                           unsafe.Pointer
	Flags, FamilyIndex, QueueCount uint32
	Priorities                     *float32
}
type deviceCreateInfo struct {
	SType                       uint32
	Next                        unsafe.Pointer
	Flags, QueueCreateInfoCount uint32
	QueueCreateInfos            *queueCreateInfo
	LayerCount                  uint32
	LayerNames                  **byte
	ExtensionCount              uint32
	ExtensionNames              **byte
	EnabledFeatures             unsafe.Pointer
}
type queueFamilyProperties struct {
	Flags, Count, TimestampValidBits                      uint32
	MinTransferWidth, MinTransferHeight, MinTransferDepth uint32
}
type extensionProperties struct {
	Name    [256]byte
	Version uint32
}

type features2 struct {
	SType    uint32
	Next     unsafe.Pointer
	Features [55]uint32
}
type swapchainMaintenanceFeatures struct {
	SType                 uint32
	Next                  unsafe.Pointer
	SwapchainMaintenance1 uint32
}

// Vulkan writes the fixed-size VkPhysicalDeviceProperties; the aligned tail is
// larger than the 1.0 structure and is deliberately not interpreted here.
type physicalProperties struct {
	APIVersion, DriverVersion, VendorID, DeviceID, DeviceType uint32
	DeviceName                                                [256]byte
	PipelineCacheUUID                                         [16]byte
	Tail                                                      [128]uint64
}

type DeviceInfo struct {
	Name                   string         `json:"name"`
	APIVersion             uint32         `json:"api_version"`
	DriverVersion          uint32         `json:"driver_version"`
	VendorID               uint32         `json:"vendor_id"`
	DeviceID               uint32         `json:"device_id"`
	DeviceType             uint32         `json:"device_type"`
	GraphicsQueueFamily    uint32         `json:"graphics_queue_family"`
	Swapchain              bool           `json:"swapchain"`
	ExternalMemoryWin32    bool           `json:"external_memory_win32"`
	ExternalSemaphoreWin32 bool           `json:"external_semaphore_win32"`
	PresentFence           bool           `json:"present_fence"`
	TestSurface            *SwapchainInfo `json:"test_surface,omitempty"`
}

// Host owns the loader, instance, logical device and queue in that order.
// Future rendering resources must be destroyed before Close is called.
type Host struct {
	mu                                sync.Mutex
	dll                               windows.Handle
	instance, physical, device, queue uintptr
	getInstanceProcAddr               uintptr
	destroyInstance, destroyDevice    uintptr
	swapchains                        map[*Swapchain]struct{}
	info                              DeviceInfo
}

//go:uintptrescapes
func call(proc uintptr, args ...uintptr) (uintptr, error) {
	if proc == 0 {
		return 0, errors.New("Vulkan 函数不可用")
	}
	r, _, _ := syscall.SyscallN(proc, args...)
	return r, nil
}
func result(name string, value uintptr) error {
	if int32(value) != vkSuccess {
		return fmt.Errorf("%s 失败：VkResult %d", name, int32(value))
	}
	return nil
}
func cstring(s string) *byte { p, _ := windows.BytePtrFromString(s); return p }
func getInstanceProc(get, instance uintptr, name string) (uintptr, error) {
	n := cstring(name)
	p, err := call(get, instance, uintptr(unsafe.Pointer(n)))
	runtime.KeepAlive(n)
	if err != nil {
		return 0, err
	}
	if p == 0 {
		return 0, fmt.Errorf("Vulkan 函数 %s 不可用", name)
	}
	return p, nil
}
func stringBytes(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// OpenHost initializes a Vulkan 1.0 instance and a hardware graphics queue.
// It requires VK_KHR_swapchain but does not yet create a window or swapchain.
// The returned object must be closed by the caller.
func OpenHost() (_ *Host, err error) {
	h := &Host{}
	defer func() {
		if err != nil {
			_ = h.Close()
		}
	}()
	h.dll, err = windows.LoadLibraryEx("vulkan-1.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("加载系统 Vulkan 失败：%w", err)
	}
	get, err := windows.GetProcAddress(h.dll, "vkGetInstanceProcAddr")
	if err != nil {
		return nil, fmt.Errorf("获取 Vulkan 入口失败：%w", err)
	}
	h.getInstanceProcAddr = get
	// Resolve cleanup before allocating an instance, so every later failure
	// has a valid destroy function even if dispatch lookup fails.
	h.destroyInstance, err = windows.GetProcAddress(h.dll, "vkDestroyInstance")
	if err != nil {
		return nil, fmt.Errorf("获取 Vulkan 清理入口失败：%w", err)
	}
	create, err := getInstanceProc(get, 0, "vkCreateInstance")
	if err != nil {
		return nil, err
	}
	enumerateInstance, err := getInstanceProc(get, 0, "vkEnumerateInstanceExtensionProperties")
	if err != nil {
		return nil, err
	}
	available, err := enumerateInstanceExtensions(enumerateInstance)
	if err != nil {
		return nil, err
	}
	required, err := requiredInstanceExtensions(available)
	if err != nil {
		return nil, err
	}
	enabledInstance := make(map[string]bool, len(required))
	for _, name := range required {
		enabledInstance[name] = true
	}
	instanceNames := make([]*byte, len(required))
	for i, name := range required {
		instanceNames[i] = cstring(name)
	}
	appName := cstring("NekoSimulator233")
	app := applicationInfo{SType: 0, ApplicationName: appName, EngineName: appName, APIVersion: vkAPIVersion10}
	ci := instanceCreateInfo{SType: 1, ApplicationInfo: &app, ExtensionCount: uint32(len(instanceNames)), ExtensionNames: &instanceNames[0]}
	var instance uintptr
	r, err := call(create, uintptr(unsafe.Pointer(&ci)), 0, uintptr(unsafe.Pointer(&instance)))
	runtime.KeepAlive(ci)
	runtime.KeepAlive(app)
	runtime.KeepAlive(appName)
	runtime.KeepAlive(instanceNames)
	if err != nil {
		return nil, err
	}
	if err = result("vkCreateInstance", r); err != nil {
		return nil, err
	}
	if instance == 0 {
		return nil, errors.New("Vulkan 返回空实例")
	}
	h.instance = instance
	enumerate, err := getInstanceProc(get, h.instance, "vkEnumeratePhysicalDevices")
	if err != nil {
		return nil, err
	}
	properties, err := getInstanceProc(get, h.instance, "vkGetPhysicalDeviceProperties")
	if err != nil {
		return nil, err
	}
	queues, err := getInstanceProc(get, h.instance, "vkGetPhysicalDeviceQueueFamilyProperties")
	if err != nil {
		return nil, err
	}
	win32Present, err := getInstanceProc(get, h.instance, "vkGetPhysicalDeviceWin32PresentationSupportKHR")
	if err != nil {
		return nil, err
	}
	extensions, err := getInstanceProc(get, h.instance, "vkEnumerateDeviceExtensionProperties")
	if err != nil {
		return nil, err
	}
	var getFeatures2 uintptr
	if enabledInstance["VK_KHR_get_physical_device_properties2"] {
		getFeatures2, _ = getInstanceProc(get, h.instance, "vkGetPhysicalDeviceFeatures2KHR")
	}
	createDevice, err := getInstanceProc(get, h.instance, "vkCreateDevice")
	if err != nil {
		return nil, err
	}
	getQueue, err := getInstanceProc(get, h.instance, "vkGetDeviceQueue")
	if err != nil {
		return nil, err
	}
	h.destroyDevice, err = getInstanceProc(get, h.instance, "vkDestroyDevice")
	if err != nil {
		return nil, err
	}
	devices, err := enumerateDevices(enumerate, h.instance)
	if err != nil {
		return nil, err
	}
	var candidates []candidate
	for _, physical := range devices {
		var p physicalProperties
		_, _ = call(properties, physical, uintptr(unsafe.Pointer(&p)))
		runtime.KeepAlive(&p)
		if p.DeviceType != vkDiscreteGPU && p.DeviceType != vkIntegratedGPU {
			continue
		}
		families, e := enumerateQueues(queues, physical)
		if e != nil {
			return nil, e
		}
		family, ok := graphicsFamily(families, func(index uint32) bool {
			supported, err := call(win32Present, physical, uintptr(index))
			return err == nil && supported != 0
		})
		if !ok {
			continue
		}
		ext, e := enumerateExtensions(extensions, physical)
		if e != nil {
			return nil, e
		}
		if !ext["VK_KHR_swapchain"] {
			continue
		}
		presentExtension := presentFenceExtension(enabledInstance, ext)
		if presentExtension != "" && getFeatures2 != 0 {
			maintenance := swapchainMaintenanceFeatures{SType: 1000275000}
			features := features2{SType: 1000059000, Next: unsafe.Pointer(&maintenance)}
			_, e = call(getFeatures2, physical, uintptr(unsafe.Pointer(&features)))
			runtime.KeepAlive(features)
			runtime.KeepAlive(maintenance)
			if e != nil || maintenance.SwapchainMaintenance1 == 0 {
				presentExtension = ""
			}
		} else {
			presentExtension = ""
		}
		candidates = append(candidates, candidate{physical: physical, presentExtension: presentExtension, info: DeviceInfo{Name: stringBytes(p.DeviceName[:]), APIVersion: p.APIVersion, DriverVersion: p.DriverVersion, VendorID: p.VendorID, DeviceID: p.DeviceID, DeviceType: p.DeviceType, GraphicsQueueFamily: family, Swapchain: true, ExternalMemoryWin32: ext["VK_KHR_external_memory_win32"], ExternalSemaphoreWin32: ext["VK_KHR_external_semaphore_win32"], PresentFence: presentExtension != ""}})
	}
	chosen, ok := chooseDevice(candidates)
	if !ok {
		return nil, errors.New("未找到支持图形队列和 VK_KHR_swapchain 的硬件 Vulkan 设备")
	}
	h.physical, h.info = chosen.physical, chosen.info
	priority := float32(1)
	qci := queueCreateInfo{SType: 2, FamilyIndex: h.info.GraphicsQueueFamily, QueueCount: 1, Priorities: &priority}
	name := cstring("VK_KHR_swapchain")
	names := []*byte{name}
	var maintenance swapchainMaintenanceFeatures
	if chosen.presentExtension != "" {
		names = append(names, cstring(chosen.presentExtension))
		maintenance = swapchainMaintenanceFeatures{SType: 1000275000, SwapchainMaintenance1: 1}
	}
	dci := deviceCreateInfo{SType: 3, QueueCreateInfoCount: 1, QueueCreateInfos: &qci, ExtensionCount: uint32(len(names)), ExtensionNames: &names[0]}
	if chosen.presentExtension != "" {
		dci.Next = unsafe.Pointer(&maintenance)
	}
	var device uintptr
	r, err = call(createDevice, h.physical, uintptr(unsafe.Pointer(&dci)), 0, uintptr(unsafe.Pointer(&device)))
	runtime.KeepAlive(dci)
	runtime.KeepAlive(qci)
	runtime.KeepAlive(names)
	runtime.KeepAlive(name)
	runtime.KeepAlive(priority)
	runtime.KeepAlive(maintenance)
	if err != nil {
		return nil, err
	}
	if err = result("vkCreateDevice", r); err != nil {
		return nil, err
	}
	if device == 0 {
		return nil, errors.New("Vulkan 返回空逻辑设备")
	}
	h.device = device
	_, err = call(getQueue, h.device, uintptr(h.info.GraphicsQueueFamily), 0, uintptr(unsafe.Pointer(&h.queue)))
	if err != nil {
		return nil, err
	}
	if h.queue == 0 {
		return nil, errors.New("Vulkan 返回空图形队列")
	}
	return h, nil
}

func (h *Host) Info() DeviceInfo { h.mu.Lock(); defer h.mu.Unlock(); return h.info }

func InspectHost(testSurface bool) (DeviceInfo, error) {
	h, err := OpenHost()
	if err != nil {
		return DeviceInfo{}, err
	}
	info := h.Info()
	if testSurface {
		var surface SwapchainInfo
		surface, err = inspectHiddenSurface(h, false, false)
		if err != nil {
			_ = h.Close()
			return DeviceInfo{}, err
		}
		info.TestSurface = &surface
	}
	if err := h.Close(); err != nil {
		return DeviceInfo{}, err
	}
	return info, nil
}

// ProbePresent is only for a short-lived subprocess. On a GPU timeout it
// leaves pending native resources intact and the subprocess must terminate.
func ProbePresent() (DeviceInfo, error) {
	return probePresent(false)
}

// ProbePixels additionally uploads a patterned frame and verifies GPU readback.
// Like ProbePresent it must run in a short-lived subprocess.
func ProbePixels() (DeviceInfo, error) { return probePresent(true) }

// ProbeFrame must run in a disposable subprocess that owns the GPU resources.
func ProbeFrame(frame *image.RGBA) (DeviceInfo, error) {
	if frame == nil || frame.Rect.Dx() <= 0 || frame.Rect.Dy() <= 0 {
		return DeviceInfo{}, errors.New("画面尺寸无效")
	}
	return probeFrame(frame, frame.Rect.Dx(), frame.Rect.Dy(), false, false)
}

// ProbeFittedFrame tests a scaled frame only in a disposable subprocess.
func ProbeFittedFrame(frame *image.RGBA, width, height int) (DeviceInfo, error) {
	if frame == nil || width <= 0 || height <= 0 || width > 8192 || height > 8192 || int64(width)*int64(height) > 32*1024*1024 {
		return DeviceInfo{}, errors.New("画面尺寸无效")
	}
	return probeFrame(frame, width, height, true, false)
}

// ProbeGPUScaledFrame verifies the experimental integer GPU scale path.
func ProbeGPUScaledFrame(frame *image.RGBA, width, height int) (DeviceInfo, error) {
	if frame == nil || width <= 0 || height <= 0 || width > 8192 || height > 8192 || int64(width)*int64(height) > 32*1024*1024 {
		return DeviceInfo{}, errors.New("画面尺寸无效")
	}
	return probeFrame(frame, width, height, true, true)
}

func probeFrame(frame *image.RGBA, width, height int, fit, gpuScale bool) (DeviceInfo, error) {
	h, err := OpenHost()
	if err != nil {
		return DeviceInfo{}, err
	}
	info := h.Info()
	surface, err := inspectHiddenFrame(h, width, height, false, frame, fit, gpuScale)
	if err != nil {
		_ = h.Close()
		return DeviceInfo{}, err
	}
	info.TestSurface = &surface
	if err = h.Close(); err != nil {
		return DeviceInfo{}, err
	}
	return info, nil
}

func probePresent(pixels bool) (DeviceInfo, error) {
	h, err := OpenHost()
	if err != nil {
		return DeviceInfo{}, err
	}
	info := h.Info()
	surface, err := inspectHiddenSurface(h, true, pixels)
	if err != nil {
		_ = h.Close()
		return DeviceInfo{}, err
	}
	info.TestSurface = &surface
	if err = h.Close(); err != nil {
		return DeviceInfo{}, err
	}
	return info, nil
}

func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for swapchain := range h.swapchains {
		if err := swapchain.closeLocked(); err != nil {
			return err
		}
	}
	if h.device != 0 && h.destroyDevice != 0 {
		_, _ = call(h.destroyDevice, h.device, 0)
		h.device = 0
	}
	if h.instance != 0 && h.destroyInstance != 0 {
		_, _ = call(h.destroyInstance, h.instance, 0)
		h.instance = 0
	}
	if h.dll != 0 {
		err := windows.FreeLibrary(h.dll)
		h.dll = 0
		return err
	}
	return nil
}
