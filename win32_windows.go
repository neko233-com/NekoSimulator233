//go:build windows

package main

import (
	"math"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/neko233-com/NekoSimulator233/internal/viewlayout"
	"golang.org/x/sys/windows"
)

var (
	user32DLL            = windows.NewLazySystemDLL("user32.dll")
	procSystemParameters = user32DLL.NewProc("SystemParametersInfoW")
	procEnumWindows      = user32DLL.NewProc("EnumWindows")
	procGetWindowTextW   = user32DLL.NewProc("GetWindowTextW")
	procIsWindowVisible  = user32DLL.NewProc("IsWindowVisible")
	procSetWindowPos     = user32DLL.NewProc("SetWindowPos")
	procGetWindowRect    = user32DLL.NewProc("GetWindowRect")
	procGetClientRect    = user32DLL.NewProc("GetClientRect")
	procGetDpiForWindow  = user32DLL.NewProc("GetDpiForWindow")
	procShowWindow       = user32DLL.NewProc("ShowWindow")
	procSetForegroundWnd = user32DLL.NewProc("SetForegroundWindow")
	procGetWindowLongPtr = user32DLL.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr = user32DLL.NewProc("SetWindowLongPtrW")
	procReleaseCapture   = user32DLL.NewProc("ReleaseCapture")
	procSendMessageW     = user32DLL.NewProc("SendMessageW")
	procIsZoomed         = user32DLL.NewProc("IsZoomed")
	procGetAsyncKeyState = user32DLL.NewProc("GetAsyncKeyState")
)

const (
	spiGetWorkArea = 0x0030
	swHide         = 0
	swShow         = 5
	swMinimize     = 6
	swMaximize     = 3
	swRestore      = 9
	swpNoActivate  = 0x0010
	swpNoSize      = 0x0001
	swpNoMove      = 0x0002
	swpNoZOrder    = 0x0004
	swpFrameChanged = 0x0020
	hwndTopmost    = ^uintptr(0)     // (HWND)-1
	hwndNotopmost  = ^uintptr(1)     // (HWND)-2
	gwlStyle       = ^uintptr(15)    // (int)-16
	wsCaption      = 0x00C00000
	wmNCLButtonDown = 0x00A1
	htCaption       = 2
)

func windowDPI(hwnd uintptr) float64 {
	if procGetDpiForWindow.Find() == nil {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi >= 96 {
			return float64(dpi) / 96.0
		}
	}
	return 1.0
}

func windowOverhead(hwnd uintptr, chromeLogicalH int) (extraW, extraH int32) {
	var wr, cr winRect
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	_, _, _ = procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&cr)))
	extraW = (wr.Right - wr.Left) - (cr.Right - cr.Left)
	extraH = (wr.Bottom - wr.Top) - (cr.Bottom - cr.Top) + int32(math.Round(float64(chromeLogicalH)*windowDPI(hwnd)))
	return extraW, extraH
}

// setWindowAspectByTitle snaps the window height proportionally to frameW:frameH
// using thread-safe SetWindowPos so resized windows have zero black bars.
func setWindowAspectByTitle(title string, frameW, frameH, chromeLogicalH int, snap bool) {
	if !snap || frameW <= 0 || frameH <= 0 {
		return
	}
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return
	}
	zoomed, _, _ := procIsZoomed.Call(hwnd)
	if zoomed != 0 {
		return
	}
	var wr winRect
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	extraW, extraH := windowOverhead(hwnd, chromeLogicalH)
	curW := wr.Right - wr.Left
	clientW := curW - extraW
	if clientW > 120 {
		targetH := int32(math.Round(float64(clientW)*float64(frameH)/float64(frameW))) + extraH
		if math.Abs(float64(targetH-(wr.Bottom-wr.Top))) > 3 {
			_, _, _ = procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(curW), uintptr(targetH), swpNoMove|swpNoZOrder|swpNoActivate)
		}
	}
}

type winRect struct {
	Left, Top, Right, Bottom int32
}

func primaryWorkArea() viewlayout.Rect {
	var r winRect
	ok, _, _ := procSystemParameters.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	if ok == 0 || r.Right <= r.Left || r.Bottom <= r.Top {
		return viewlayout.Rect{X: 0, Y: 0, Width: 1920, Height: 1040}
	}
	return viewlayout.Rect{
		X:      int(r.Left),
		Y:      int(r.Top),
		Width:  int(r.Right - r.Left),
		Height: int(r.Bottom - r.Top),
	}
}

func findWindowByTitle(title string) uintptr {
	if title == "" {
		return 0
	}
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1
		}
		var buf [256]uint16
		n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 && windows.UTF16ToString(buf[:n]) == title {
			found = hwnd
			return 0
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	return found
}

func findWindowByTitleAny(title string) uintptr {
	if title == "" {
		return 0
	}
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var buf [256]uint16
		n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 && windows.UTF16ToString(buf[:n]) == title {
			var r winRect
			_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
			if r.Right-r.Left > 200 && r.Bottom-r.Top > 200 {
				found = hwnd
				return 0
			}
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	return found
}

func showOrHideWindowByTitle(title string, show bool) bool {
	hwnd := findWindowByTitleAny(title)
	if hwnd == 0 {
		return false
	}
	if show {
		_, _, _ = procShowWindow.Call(hwnd, swShow)
		_, _, _ = procShowWindow.Call(hwnd, swRestore)
		_, _, _ = procSetForegroundWnd.Call(hwnd)
	} else {
		_, _, _ = procShowWindow.Call(hwnd, swHide)
	}
	return true
}

func makeFramelessTitlebarByTitle(title string) bool {
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return false
	}
	style, _, _ := procGetWindowLongPtr.Call(hwnd, gwlStyle)
	if style == 0 {
		return false
	}
	newStyle := style &^ wsCaption
	_, _, _ = procSetWindowLongPtr.Call(hwnd, gwlStyle, newStyle)
	_, _, _ = procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged)
	return true
}

func dragWindowByTitle(title string) {
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return
	}
	var startPt trayPoint
	var startRect winRect
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&startPt)))
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&startRect)))

	go func() {
		ticker := time.NewTicker(8 * time.Millisecond)
		defer ticker.Stop()
		lastX, lastY := startPt.X, startPt.Y
		for range ticker.C {
			state, _, _ := procGetAsyncKeyState.Call(0x01) // VK_LBUTTON
			if state&0x8000 == 0 {
				return
			}
			var cur trayPoint
			_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&cur)))
			if cur.X == lastX && cur.Y == lastY {
				continue
			}
			lastX, lastY = cur.X, cur.Y
			nx := startRect.Left + (cur.X - startPt.X)
			ny := startRect.Top + (cur.Y - startPt.Y)
			_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(nx), uintptr(ny), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
		}
	}()
}

func minimizeWindowByTitle(title string) {
	hwnd := findWindowByTitle(title)
	if hwnd != 0 {
		_, _, _ = procShowWindow.Call(hwnd, swMinimize)
	}
}

var (
	restoreMu    sync.Mutex
	restoreRects = make(map[uintptr]winRect)
)

func toggleMaximizeWindowByTitle(title string) {
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return
	}
	restoreMu.Lock()
	prev, isMax := restoreRects[hwnd]
	if isMax {
		delete(restoreRects, hwnd)
		restoreMu.Unlock()
		_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(prev.Left), uintptr(prev.Top), uintptr(prev.Right-prev.Left), uintptr(prev.Bottom-prev.Top), swpNoZOrder|swpNoActivate)
		return
	}
	var cur winRect
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&cur)))
	restoreRects[hwnd] = cur
	restoreMu.Unlock()

	wa := primaryWorkArea()
	_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(wa.X), uintptr(wa.Y), uintptr(wa.Width), uintptr(wa.Height), swpNoZOrder|swpNoActivate)
}

func setWindowBoundsByTitle(title string, x, y, w, h int) bool {
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return false
	}
	_, _, _ = procShowWindow.Call(hwnd, swRestore)
	ok, _, _ := procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoActivate)
	return ok != 0
}

func setWindowTopmostByTitle(title string, topmost bool) bool {
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return false
	}
	insertAfter := hwndNotopmost
	if topmost {
		insertAfter = hwndTopmost
	}
	ok, _, _ := procSetWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	return ok != 0
}

func focusWindowByTitle(title string) {
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, swShow)
	_, _, _ = procSetForegroundWnd.Call(hwnd)
}

func getWindowBoundsByTitle(title string) (viewerGeometry, bool) {
	hwnd := findWindowByTitle(title)
	if hwnd == 0 {
		return viewerGeometry{}, false
	}
	var r winRect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 || r.Right-r.Left < 320 || r.Bottom-r.Top < 240 {
		return viewerGeometry{}, false
	}
	return viewerGeometry{
		Left:   int(r.Left),
		Top:    int(r.Top),
		Width:  int(r.Right - r.Left),
		Height: int(r.Bottom - r.Top),
	}, true
}

// gpuiKeyToHID maps GPUI KeyEvent.Key strings to USB HID keyboard usage IDs.
func gpuiKeyToHID(key string) uint16 {
	k := strings.ToLower(strings.TrimSpace(key))
	if len(k) == 1 {
		ch := k[0]
		if ch >= 'a' && ch <= 'z' {
			return uint16(0x04 + (ch - 'a'))
		}
		if ch >= '1' && ch <= '9' {
			return uint16(0x1e + (ch - '1'))
		}
		if ch == '0' {
			return 0x27
		}
	}
	switch k {
	case "enter", "return":
		return 0x28
	case "escape", "esc":
		return 0x29
	case "backspace":
		return 0x2a
	case "tab":
		return 0x2b
	case "space", " ":
		return 0x2c
	case "-", "minus":
		return 0x2d
	case "=", "equal":
		return 0x2e
	case "[", "bracketleft":
		return 0x2f
	case "]", "bracketright":
		return 0x30
	case "\\", "backslash":
		return 0x31
	case ";", "semicolon":
		return 0x33
	case "'", "quote":
		return 0x34
	case "`", "grave":
		return 0x35
	case ",", "comma":
		return 0x36
	case ".", "period":
		return 0x37
	case "/", "slash":
		return 0x38
	case "f1":
		return 0x3a
	case "f2":
		return 0x3b
	case "f3":
		return 0x3c
	case "f4":
		return 0x3d
	case "f5":
		return 0x3e
	case "f6":
		return 0x3f
	case "f7":
		return 0x40
	case "f8":
		return 0x41
	case "f9":
		return 0x42
	case "f10":
		return 0x43
	case "f11":
		return 0x44
	case "f12":
		return 0x45
	case "delete":
		return 0x4c
	case "right", "arrowright":
		return 0x4f
	case "left", "arrowleft":
		return 0x50
	case "down", "arrowdown":
		return 0x51
	case "up", "arrowup":
		return 0x52
	}
	return 0
}
