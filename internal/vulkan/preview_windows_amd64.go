//go:build windows && amd64

package vulkan

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/neko233-com/NekoSimulator233/internal/frameprobe"
	"github.com/neko233-com/NekoSimulator233/internal/previewinput"
	"golang.org/x/sys/windows"
)

type previewClass struct {
	Size, Style                                                        uint32
	WndProc                                                            uintptr
	ClassExtra, WindowExtra                                            int32
	Instance, Icon, Cursor, Background, MenuName, ClassName, SmallIcon uintptr
}
type previewMessage struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}
type previewFrame struct {
	frame *image.RGBA
	err   error
}

// RunPreview owns a visible experimental window in a disposable process.
// It reads framed pixels only from input and never opens an Android instance.
// Callers must terminate this process if the driver hangs during cleanup.
func RunPreview(input io.Reader, output io.Writer) (err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var closing atomic.Bool
	events := make(chan previewinput.Event, 64)
	eventDone := make(chan struct{})
	defer close(eventDone)
	go func() {
		enc := json.NewEncoder(output)
		for {
			select {
			case event := <-events:
				if enc.Encode(event) != nil {
					closing.Store(true)
					return
				}
			case <-eventDone:
				return
			}
		}
	}()
	emit := func(event previewinput.Event) {
		select {
		case events <- event:
		default:
			if event.Kind != "move" {
				closing.Store(true)
			}
		}
	}
	var displayedWidth, displayedHeight, frameWidth, frameHeight int
	setCapture := user32.NewProc("SetCapture")
	releaseCapture := user32.NewProc("ReleaseCapture")
	defWindowProc := user32.NewProc("DefWindowProcW")
	wndProc := windows.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x0010:
			closing.Store(true)
			return 0
		case 0x0008: // WM_KILLFOCUS
			emit(previewinput.Event{Kind: "release"})
		case 0x0215: // WM_CAPTURECHANGED
			emit(previewinput.Event{Kind: "up"})
		case 0x0202: // WM_LBUTTONUP
			emit(previewinput.Event{Kind: "up"})
			releaseCapture.Call()
			return 0
		case 0x0201, 0x0200: // WM_LBUTTONDOWN / WM_MOUSEMOVE
			if msg == 0x0200 && wp&1 == 0 {
				break
			}
			var rect winRect
			ok, _, _ := getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
			w, h := int(rect.Right-rect.Left), int(rect.Bottom-rect.Top)
			if ok == 0 || w != displayedWidth || h != displayedHeight || frameWidth == 0 || frameHeight == 0 {
				emit(previewinput.Event{Kind: "up"})
				releaseCapture.Call()
				return 0
			}
			kind := "move"
			if msg == 0x0201 {
				kind = "down"
			}
			e := previewinput.Event{Kind: kind, X: int(int16(lp & 0xffff)), Y: int(int16((lp >> 16) & 0xffff)), Width: w, Height: h, FrameWidth: frameWidth, FrameHeight: frameHeight}
			if e.Valid() {
				if kind == "down" {
					setCapture.Call(hwnd)
				}
				emit(e)
			}
			return 0
		}
		ret, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
		return ret
	})
	module, _, callErr := getModuleHandle.Call(0)
	if module == 0 {
		return fmt.Errorf("获取进程模块失败：%w", callErr)
	}
	class, _ := windows.UTF16PtrFromString("NekoVulkanPreviewWindow")
	title, _ := windows.UTF16PtrFromString("NekoSimulator233 Vulkan 实验画面")
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	wc := previewClass{Size: uint32(unsafe.Sizeof(previewClass{})), WndProc: wndProc, Instance: module, Cursor: cursor, ClassName: uintptr(unsafe.Pointer(class))}
	register := user32.NewProc("RegisterClassExW")
	if atom, _, e := register.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return fmt.Errorf("注册 Vulkan 窗口失败：%w", e)
	}
	create := user32.NewProc("CreateWindowExW")
	hwnd, _, callErr := create.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0x00CF0000, 0x80000000, 0x80000000, 900, 700, 0, 0, module, 0)
	runtime.KeepAlive(class)
	runtime.KeepAlive(title)
	runtime.KeepAlive(wc)
	if hwnd == 0 {
		return fmt.Errorf("创建 Vulkan 窗口失败：%w", callErr)
	}
	destroy := user32.NewProc("DestroyWindow")
	destroyOK := true
	defer func() {
		if destroyOK {
			destroy.Call(hwnd)
		}
	}()
	h, err := OpenHost()
	if err != nil {
		return err
	}
	renderer := NewWindowRenderer(h, hwnd)
	defer func() {
		if e := renderer.Close(); e != nil {
			destroyOK = false
			err = errors.Join(err, e)
			return
		}
		if e := h.Close(); e != nil {
			destroyOK = false
			err = errors.Join(err, e)
		}
	}()
	user32.NewProc("ShowWindow").Call(hwnd, 5)
	user32.NewProc("UpdateWindow").Call(hwnd)

	frames := make(chan previewFrame)
	returned := make(chan *image.RGBA)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		var reuse *image.RGBA
		for {
			frame, readErr := frameprobe.ReadNext(input, reuse)
			select {
			case frames <- previewFrame{frame, readErr}:
			case <-stop:
				return
			}
			if readErr != nil {
				return
			}
			select {
			case reuse = <-returned:
			case <-stop:
				return
			}
		}
	}()
	peek := user32.NewProc("PeekMessageW")
	translate := user32.NewProc("TranslateMessage")
	dispatch := user32.NewProc("DispatchMessageW")
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	for !closing.Load() {
		select {
		case next := <-frames:
			if next.err != nil {
				if errors.Is(next.err, io.EOF) {
					return nil
				}
				return next.err
			}
			if minimized, _, _ := isIconic.Call(hwnd); minimized == 0 {
				if err := renderer.PresentFittedRGBA(next.frame, false); err != nil {
					return err
				}
				info, infoErr := renderer.Info()
				if infoErr != nil {
					return infoErr
				}
				displayedWidth, displayedHeight = int(info.Width), int(info.Height)
				frameWidth, frameHeight = next.frame.Bounds().Dx(), next.frame.Bounds().Dy()
			}
			select {
			case returned <- next.frame:
			case <-stop:
				return nil
			}
		case <-ticker.C:
			var msg previewMessage
			for {
				ok, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
				if ok == 0 {
					break
				}
				translate.Call(uintptr(unsafe.Pointer(&msg)))
				dispatch.Call(uintptr(unsafe.Pointer(&msg)))
			}
		}
	}
	return nil
}
