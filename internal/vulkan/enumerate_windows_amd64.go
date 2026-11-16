//go:build windows && amd64

package vulkan

import (
	"fmt"
	"runtime"
	"unsafe"
)

type candidate struct {
	physical uintptr
	info     DeviceInfo
}

func chooseDevice(candidates []candidate) (candidate, bool) {
	for _, c := range candidates {
		if c.info.DeviceType == vkDiscreteGPU {
			return c, true
		}
	}
	for _, c := range candidates {
		if c.info.DeviceType == vkIntegratedGPU {
			return c, true
		}
	}
	return candidate{}, false
}
func graphicsFamily(families []queueFamilyProperties, supportsPresent func(uint32) bool) (uint32, bool) {
	for i, q := range families {
		if q.Count != 0 && q.Flags&vkGraphics != 0 && supportsPresent(uint32(i)) {
			return uint32(i), true
		}
	}
	return 0, false
}

// Device and extension counts can change between calls. Bound both the retry
// loop and data returned by a broken driver before allocating memory.
const maxDriverEntries = 65536
const maxEnumerationAttempts = 4

func enumerateDevices(proc, instance uintptr) ([]uintptr, error) {
	for attempt := 0; attempt < maxEnumerationAttempts; attempt++ {
		var count uint32
		r, err := call(proc, instance, uintptr(unsafe.Pointer(&count)), 0)
		if err != nil {
			return nil, err
		}
		if err = result("vkEnumeratePhysicalDevices", r); err != nil {
			return nil, err
		}
		if count > maxDriverEntries {
			return nil, fmt.Errorf("Vulkan 设备数量异常：%d", count)
		}
		if count == 0 {
			return nil, nil
		}
		items := make([]uintptr, count)
		r, err = call(proc, instance, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&items[0])))
		runtime.KeepAlive(items)
		if err != nil {
			return nil, err
		}
		if int32(r) == vkIncomplete || count > uint32(len(items)) {
			continue
		}
		if err = result("vkEnumeratePhysicalDevices", r); err != nil {
			return nil, err
		}
		return items[:count], nil
	}
	return nil, fmt.Errorf("Vulkan 设备列表持续变化")
}

func enumerateQueues(proc, physical uintptr) ([]queueFamilyProperties, error) {
	for attempt := 0; attempt < maxEnumerationAttempts; attempt++ {
		var count uint32
		if _, err := call(proc, physical, uintptr(unsafe.Pointer(&count)), 0); err != nil {
			return nil, err
		}
		if count > maxDriverEntries {
			return nil, fmt.Errorf("Vulkan 队列数量异常：%d", count)
		}
		if count == 0 {
			return nil, nil
		}
		items := make([]queueFamilyProperties, count)
		if _, err := call(proc, physical, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&items[0]))); err != nil {
			return nil, err
		}
		runtime.KeepAlive(items)
		if count > uint32(len(items)) {
			continue
		}
		return items[:count], nil
	}
	return nil, fmt.Errorf("Vulkan 队列列表持续变化")
}

func enumerateExtensions(proc, physical uintptr) (map[string]bool, error) {
	for attempt := 0; attempt < maxEnumerationAttempts; attempt++ {
		var count uint32
		r, err := call(proc, physical, 0, uintptr(unsafe.Pointer(&count)), 0)
		if err != nil {
			return nil, err
		}
		if err = result("vkEnumerateDeviceExtensionProperties", r); err != nil {
			return nil, err
		}
		if count > maxDriverEntries {
			return nil, fmt.Errorf("Vulkan 扩展数量异常：%d", count)
		}
		if count == 0 {
			return map[string]bool{}, nil
		}
		items := make([]extensionProperties, count)
		r, err = call(proc, physical, 0, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&items[0])))
		runtime.KeepAlive(items)
		if err != nil {
			return nil, err
		}
		if int32(r) == vkIncomplete || count > uint32(len(items)) {
			continue
		}
		if err = result("vkEnumerateDeviceExtensionProperties", r); err != nil {
			return nil, err
		}
		ext := make(map[string]bool, count)
		for _, item := range items[:count] {
			ext[stringBytes(item.Name[:])] = true
		}
		return ext, nil
	}
	return nil, fmt.Errorf("Vulkan 扩展列表持续变化")
}

func enumerateInstanceExtensions(proc uintptr) (map[string]bool, error) {
	for attempt := 0; attempt < maxEnumerationAttempts; attempt++ {
		var count uint32
		r, err := call(proc, 0, uintptr(unsafe.Pointer(&count)), 0)
		if err != nil {
			return nil, err
		}
		if err = result("vkEnumerateInstanceExtensionProperties", r); err != nil {
			return nil, err
		}
		if count > maxDriverEntries {
			return nil, fmt.Errorf("Vulkan 实例扩展数量异常：%d", count)
		}
		if count == 0 {
			return map[string]bool{}, nil
		}
		items := make([]extensionProperties, count)
		r, err = call(proc, 0, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&items[0])))
		runtime.KeepAlive(items)
		if err != nil {
			return nil, err
		}
		if int32(r) == vkIncomplete || count > uint32(len(items)) {
			continue
		}
		if err = result("vkEnumerateInstanceExtensionProperties", r); err != nil {
			return nil, err
		}
		ext := make(map[string]bool, count)
		for _, item := range items[:count] {
			ext[stringBytes(item.Name[:])] = true
		}
		return ext, nil
	}
	return nil, fmt.Errorf("Vulkan 实例扩展列表持续变化")
}

func requiredInstanceExtensions(available map[string]bool) ([]string, error) {
	needed := []string{"VK_KHR_surface", "VK_KHR_win32_surface"}
	for _, name := range needed {
		if !available[name] {
			return nil, fmt.Errorf("Vulkan 缺少实例扩展 %s", name)
		}
	}
	return needed, nil
}
