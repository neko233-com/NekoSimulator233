//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32DLL          = windows.NewLazySystemDLL("shell32.dll")
	kernel32DLL         = windows.NewLazySystemDLL("kernel32.dll")
	procShellNotifyIcon = shell32DLL.NewProc("Shell_NotifyIconW")
	procGetModuleHandle = kernel32DLL.NewProc("GetModuleHandleW")
	procRegisterClassEx = user32DLL.NewProc("RegisterClassExW")
	procCreateWindowEx  = user32DLL.NewProc("CreateWindowExW")
	procDefWindowProc   = user32DLL.NewProc("DefWindowProcW")
	procDestroyWindow   = user32DLL.NewProc("DestroyWindow")
	procGetMessage      = user32DLL.NewProc("GetMessageW")
	procTranslateMsg    = user32DLL.NewProc("TranslateMessage")
	procDispatchMsg     = user32DLL.NewProc("DispatchMessageW")
	procPostMessage     = user32DLL.NewProc("PostMessageW")
	procPostQuitMessage = user32DLL.NewProc("PostQuitMessage")
	procLoadIcon        = user32DLL.NewProc("LoadIconW")
	procCreatePopupMenu = user32DLL.NewProc("CreatePopupMenu")
	procAppendMenu      = user32DLL.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32DLL.NewProc("TrackPopupMenu")
	procDestroyMenu     = user32DLL.NewProc("DestroyMenu")
	procGetCursorPos    = user32DLL.NewProc("GetCursorPos")
)

const (
	wmDestroy       = 0x0002
	wmCommand       = 0x0111
	wmClose         = 0x0010
	wmApp           = 0x8000
	wmTrayCallback  = wmApp + 42
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	nimAdd    = 0x00000000
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	tpmLeftAlign  = 0x0000
	tpmBottomAlign = 0x0020
	tpmRightButton = 0x0002

	cmdTrayShow    = 1001
	cmdTrayHide    = 1002
	cmdTrayStopAll = 1003
	cmdTrayExit    = 1004
)

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type notifyIconDataW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
}

type trayPoint struct {
	X, Y int32
}

type trayMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      trayPoint
}

type trayController struct {
	hwnd uintptr
}

func (t *trayController) Close() {
	if t != nil && t.hwnd != 0 {
		_, _, _ = procPostMessage.Call(t.hwnd, wmClose, 0, 0)
	}
}

func startSystemTray(onShow, onHide, onStopAll, onExit func()) *trayController {
	tc := &trayController{}
	ready := make(chan struct{})

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hInst, _, _ := procGetModuleHandle.Call(0)
		// Load embedded application icon (resource ID 1 from rsrc_windows_amd64.syso),
		// falling back to IDI_APPLICATION (32512).
		hIcon, _, _ := procLoadIcon.Call(hInst, 1)
		if hIcon == 0 {
			hIcon, _, _ = procLoadIcon.Call(0, 32512)
		}

		className, _ := windows.UTF16PtrFromString("NekoSimulator233TrayClass")
		winTitle, _ := windows.UTF16PtrFromString("NekoSimulator233Tray")

		var nid notifyIconDataW
		showMenu := func(hwnd uintptr) {
			menu, _, _ := procCreatePopupMenu.Call()
			if menu == 0 {
				return
			}
			defer procDestroyMenu.Call(menu)

			addItem := func(id uintptr, label string) {
				p, _ := windows.UTF16PtrFromString(label)
				_, _, _ = procAppendMenu.Call(menu, mfString, id, uintptr(unsafe.Pointer(p)))
			}
			addItem(cmdTrayShow, "显示主界面 (NekoSimulator233)")
			addItem(cmdTrayHide, "隐藏到后台托盘")
			addItem(cmdTrayStopAll, "一键关闭所有模拟器")
			_, _, _ = procAppendMenu.Call(menu, mfSeparator, 0, 0)
			addItem(cmdTrayExit, "退出 NekoSimulator233")

			var pt trayPoint
			_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			_, _, _ = procSetForegroundWnd.Call(hwnd)
			_, _, _ = procTrackPopupMenu.Call(menu, tpmLeftAlign|tpmBottomAlign|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
			_, _, _ = procPostMessage.Call(hwnd, 0, 0, 0)
		}

		wndProc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
			switch msg {
			case wmTrayCallback:
				switch uint32(lParam) {
				case wmLButtonUp, wmLButtonDblClk:
					if onShow != nil {
						onShow()
					}
				case wmRButtonUp:
					showMenu(hwnd)
				}
				return 0
			case wmCommand:
				switch uint32(wParam & 0xffff) {
				case cmdTrayShow:
					if onShow != nil {
						onShow()
					}
				case cmdTrayHide:
					if onHide != nil {
						onHide()
					}
				case cmdTrayStopAll:
					if onStopAll != nil {
						onStopAll()
					}
				case cmdTrayExit:
					if onExit != nil {
						onExit()
					}
				}
				return 0
			case wmClose:
				_, _, _ = procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
				_, _, _ = procDestroyWindow.Call(hwnd)
				return 0
			case wmDestroy:
				_, _, _ = procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
				_, _, _ = procPostQuitMessage.Call(0)
				return 0
			}
			ret, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
			return ret
		})

		wcx := wndClassExW{
			CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			LpfnWndProc:   wndProc,
			HInstance:     hInst,
			LpszClassName: className,
		}
		_, _, _ = procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wcx)))
		hwnd, _, _ := procCreateWindowEx.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(winTitle)),
			0, 0, 0, 0, 0,
			0, 0, hInst, 0,
		)
		tc.hwnd = hwnd
		close(ready)

		if hwnd == 0 {
			return
		}

		nid = notifyIconDataW{
			CbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
			HWnd:             hwnd,
			UID:              1,
			UFlags:           nifMessage | nifIcon | nifTip,
			UCallbackMessage: wmTrayCallback,
			HIcon:            hIcon,
		}
		tip := windows.StringToUTF16("NekoSimulator233 - 安卓模拟器后台服务")
		copy(nid.SzTip[:len(nid.SzTip)-1], tip)
		_, _, _ = procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))

		var m trayMsg
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			_, _, _ = procTranslateMsg.Call(uintptr(unsafe.Pointer(&m)))
			_, _, _ = procDispatchMsg.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()

	<-ready
	return tc
}
