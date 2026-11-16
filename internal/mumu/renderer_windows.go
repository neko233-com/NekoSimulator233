//go:build windows

package mumu

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"unsafe"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"golang.org/x/sys/windows"
)

var androidVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

// Renderer is a compatibility backend for the locally installed MuMu engine.
// It never copies, modifies, or redistributes the vendor's DLL.
type Renderer struct {
	mu                            sync.Mutex
	dll                           *windows.DLL
	capture, down, up, disconnect *windows.Proc
	keyDown, keyUp                *windows.Proc
	pressed                       map[uint16]bool
	cli                           Client
	index                         string
	handle                        uintptr
	width, height                 int32
	buffer                        []byte
}

func OpenRenderer(root string, instance Instance) (*Renderer, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("MuMu 安装路径无效：%w", err)
	}
	if !androidVersion.MatchString(instance.AndroidVersion) {
		return nil, errors.New("不支持的 Android 引擎版本")
	}
	index, err := strconv.ParseUint(instance.Index, 10, 31)
	if err != nil {
		return nil, errors.New("实例索引无效")
	}
	path := filepath.Join(root, "nx_device", instance.AndroidVersion, "shell", "sdk", "external_renderer_ipc.dll")
	h, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS)
	if err != nil {
		return nil, fmt.Errorf("加载 MuMu 渲染接口失败：%w", err)
	}
	dll := &windows.DLL{Name: path, Handle: h}
	r := &Renderer{dll: dll, pressed: make(map[uint16]bool), cli: Client{CLI: filepath.Join(root, "nx_main", "mumu-cli.exe")}, index: instance.Index}
	fail := func(err error) (*Renderer, error) { dll.Release(); return nil, err }
	connect, err := dll.FindProc("nemu_connect")
	if err != nil {
		return fail(err)
	}
	if r.disconnect, err = dll.FindProc("nemu_disconnect"); err != nil {
		return fail(err)
	}
	if r.capture, err = dll.FindProc("nemu_capture_display"); err != nil {
		return fail(err)
	}
	if r.down, err = dll.FindProc("nemu_input_event_touch_down"); err != nil {
		return fail(err)
	}
	if r.up, err = dll.FindProc("nemu_input_event_touch_up"); err != nil {
		return fail(err)
	}
	r.keyDown, _ = dll.FindProc("nemu_input_event_key_down")
	r.keyUp, _ = dll.FindProc("nemu_input_event_key_up")
	root16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return fail(err)
	}
	handle, _, _ := connect.Call(uintptr(unsafe.Pointer(root16)), uintptr(index))
	if int32(handle) <= 0 {
		return fail(errors.New("连接 MuMu 渲染接口失败，请确认实例已启动"))
	}
	r.handle = handle
	return r, nil
}

func (r *Renderer) Capture(dst *image.RGBA) (*image.RGBA, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handle == 0 {
		return nil, errors.New("渲染连接已关闭")
	}
	// Query every frame: the guest may rotate or resize while running.
	result, _, _ := r.capture.Call(r.handle, 0, 0, uintptr(unsafe.Pointer(&r.width)), uintptr(unsafe.Pointer(&r.height)), 0)
	if int32(result) != 0 {
		return nil, fmt.Errorf("查询画面尺寸失败：%d", int32(result))
	}
	if r.width <= 0 || r.height <= 0 || r.width > 8192 || r.height > 8192 || int64(r.width)*int64(r.height) > 32*1024*1024 {
		return nil, errors.New("画面尺寸不合法")
	}
	size := int(r.width) * int(r.height) * 4
	if len(r.buffer) != size {
		r.buffer = make([]byte, size)
	}
	w, h := r.width, r.height
	result, _, _ = r.capture.Call(r.handle, 0, uintptr(size), uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&r.buffer[0])))
	if int32(result) != 0 {
		return nil, fmt.Errorf("读取画面失败：%d", int32(result))
	}
	if w != r.width || h != r.height {
		return nil, errors.New("画面尺寸正在变化")
	}
	rect := image.Rect(0, 0, int(w), int(h))
	if dst == nil || dst.Rect != rect || dst.Stride != int(w)*4 || len(dst.Pix) < size {
		dst = image.NewRGBA(rect)
	}
	stride := int(w) * 4
	for y := 0; y < int(h); y++ {
		copy(dst.Pix[y*stride:(y+1)*stride], r.buffer[(int(h)-1-y)*stride:(int(h)-y)*stride])
	}
	return dst, nil
}

func (r *Renderer) TouchDown(x, y int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handle == 0 {
		return errors.New("渲染连接已关闭")
	}
	if x < 0 || y < 0 || x >= int(r.width) || y >= int(r.height) {
		return errors.New("触点超出画面")
	}
	result, _, _ := r.down.Call(r.handle, 0, uintptr(x), uintptr(y))
	if int32(result) != 0 {
		return fmt.Errorf("发送触点失败：%d", int32(result))
	}
	return nil
}

func (r *Renderer) TouchUp() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handle == 0 {
		return errors.New("渲染连接已关闭")
	}
	result, _, _ := r.up.Call(r.handle, 0)
	if int32(result) != 0 {
		return fmt.Errorf("释放触点失败：%d", int32(result))
	}
	return nil
}

func (r *Renderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handle == 0 {
		return nil
	}
	if r.keyUp != nil {
		for usage := range r.pressed {
			if code := linuxKey(usage); code != 0 {
				r.keyUp.Call(r.handle, 0, uintptr(code))
			}
		}
	}
	r.pressed = nil
	r.disconnect.Call(r.handle)
	r.handle = 0
	r.buffer = nil
	return r.dll.Release()
}

// These values are Linux input key codes used by the MuMu renderer API.
// The mapping covers the usual US physical keyboard and modifiers.
func linuxKey(usage uint16) uint16 {
	if usage >= 0x04 && usage <= 0x1d {
		return [...]uint16{30, 48, 46, 32, 18, 33, 34, 35, 23, 36, 37, 38, 50, 49, 24, 25, 16, 19, 31, 20, 22, 47, 17, 45, 21, 44}[usage-0x04]
	}
	if usage >= 0x1e && usage <= 0x26 {
		return usage - 0x1e + 2
	}
	if usage == 0x27 {
		return 11
	}
	switch usage {
	case 0x28:
		return 28
	case 0x29:
		return 1
	case 0x2a:
		return 14
	case 0x2b:
		return 15
	case 0x2c:
		return 57
	case 0x2d:
		return 12
	case 0x2e:
		return 13
	case 0x2f:
		return 26
	case 0x30:
		return 27
	case 0x31:
		return 43
	case 0x33:
		return 39
	case 0x34:
		return 40
	case 0x35:
		return 41
	case 0x36:
		return 51
	case 0x37:
		return 52
	case 0x38:
		return 53
	case 0x39:
		return 58
	case 0x4a:
		return 102
	case 0x4b:
		return 104
	case 0x4c:
		return 111
	case 0x4d:
		return 107
	case 0x4e:
		return 109
	case 0x4f:
		return 106
	case 0x50:
		return 105
	case 0x51:
		return 108
	case 0x52:
		return 103
	case 0xe0:
		return 29
	case 0xe1:
		return 42
	case 0xe2:
		return 56
	case 0xe3:
		return 125
	case 0xe4:
		return 97
	case 0xe5:
		return 54
	case 0xe6:
		return 100
	case 0xe7:
		return 126
	}
	if usage >= 0x3a && usage <= 0x43 {
		return usage - 0x3a + 59
	}
	if usage == 0x44 {
		return 87
	}
	if usage == 0x45 {
		return 88
	}
	return 0
}

func (r *Renderer) key(usage uint16, down bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handle == 0 {
		return errors.New("渲染连接已关闭")
	}
	code := linuxKey(usage)
	if code == 0 || r.keyDown == nil || r.keyUp == nil {
		return errors.New("MuMu 不支持此键盘按键")
	}
	if down && r.pressed[usage] || !down && !r.pressed[usage] {
		return nil
	}
	proc := r.keyUp
	if down {
		proc = r.keyDown
	}
	result, _, _ := proc.Call(r.handle, 0, uintptr(code))
	if int32(result) != 0 {
		return fmt.Errorf("发送键盘事件失败：%d", int32(result))
	}
	if down {
		r.pressed[usage] = true
	} else {
		delete(r.pressed, usage)
	}
	return nil
}
func (r *Renderer) KeyDown(usage uint16) error { return r.key(usage, true) }
func (r *Renderer) KeyUp(usage uint16) error   { return r.key(usage, false) }

func (r *Renderer) Navigate(action display.Action) error {
	names := map[display.Action]string{display.Back: "go_back", display.Home: "go_home", display.VolumeUp: "volume_up", display.VolumeDown: "volume_down"}
	name, ok := names[action]
	if !ok {
		return errors.New("MuMu 当前版本不支持此导航操作")
	}
	r.mu.Lock()
	active := r.handle != 0
	r.mu.Unlock()
	if !active {
		return errors.New("渲染连接已关闭")
	}
	_, err := r.cli.run("control", "--vmindex", r.index, "tool", "func", "--name", name)
	return err
}
