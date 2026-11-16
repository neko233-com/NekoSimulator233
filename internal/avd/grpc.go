// Package avd provides a project-owned Android Emulator backend. The emulator
// is installed separately; neither its executable nor system images are bundled.
package avd

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/neko233-com/NekoSimulator233/internal/display"
	"golang.org/x/sys/windows"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protowire"
)

const controller = "/android.emulation.control.EmulatorController/"

// rawProto carries only the documented fields that this client uses. Keeping
// the wire codec local avoids distributing the SDK's large generated API.
type rawProto []byte
type protoCodec struct{}

func (protoCodec) Name() string { return "proto" }
func (protoCodec) Marshal(v any) (mem.BufferSlice, error) {
	p, ok := v.(*rawProto)
	if !ok {
		return nil, errors.New("invalid protobuf request")
	}
	return mem.BufferSlice{mem.SliceBuffer(*p)}, nil
}

type frameResponse struct {
	frame      *image.RGBA
	allowEmpty bool
	empty      bool
}

func (protoCodec) Unmarshal(data mem.BufferSlice, v any) error {
	switch p := v.(type) {
	case *rawProto:
		size := data.Len()
		if cap(*p) < size {
			*p = make(rawProto, size)
		} else {
			*p = (*p)[:size]
		}
		data.CopyTo(*p)
		return nil
	case *frameResponse:
		return decodeFragmentedFrame(data, p)
	default:
		return errors.New("invalid protobuf response")
	}
}

func appendInt(dst []byte, field protowire.Number, value uint64) []byte {
	dst = protowire.AppendTag(dst, field, protowire.VarintType)
	return protowire.AppendVarint(dst, value)
}
func appendBytes(dst []byte, field protowire.Number, value []byte) []byte {
	dst = protowire.AppendTag(dst, field, protowire.BytesType)
	return protowire.AppendBytes(dst, value)
}

type Controller struct {
	conn             *grpc.ClientConn
	token            string
	mu               sync.Mutex
	width, height    int
	identifier       uint32
	contacts         map[int]TouchPoint
	touchLockPath    string
	touchLockFile    *os.File
	touchOverlap     windows.Overlapped
	pressed          map[uint16]bool
	uncertainKeys    map[uint16]bool
	keyboardDisabled bool
	closeOnce        sync.Once
	closeErr         error
	navigate         func(display.Action) error
}

// DialController accepts a loopback port only. The emulator's per-process
// token is kept in memory and attached as gRPC metadata, never logged.
func DialController(port int, token string) (*Controller, error) {
	if port < 1 || port > 65535 || token == "" {
		return nil, errors.New("模拟器控制端点无效")
	}
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodecV2(protoCodec{}), grpc.MaxCallRecvMsgSize(128<<20)))
	if err != nil {
		return nil, err
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		conn.Close()
		return nil, err
	}
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("无法定位触屏协调目录: %w", err)
	}
	// Reserve the low bits for per-controller finger IDs; unrelated controller
	// sessions get independent randomized identifier ranges.
	identifier := binary.LittleEndian.Uint32(random[:]) & 0x7ffffff0
	if identifier == 0 {
		identifier = 16
	}
	return &Controller{conn: conn, token: token, identifier: identifier, contacts: make(map[int]TouchPoint), pressed: make(map[uint16]bool), touchLockPath: filepath.Join(localAppData, "NekoSimulator233-data", "touch-locks", fmt.Sprintf("port-%d.lock", port))}, nil
}

func (c *Controller) call(method string, request rawProto, timeout time.Duration) (rawProto, error) {
	var response rawProto
	err := c.invoke(method, request, &response, timeout)
	return response, err
}

func (c *Controller) invoke(method string, request rawProto, response any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
	if err := c.conn.Invoke(ctx, controller+method, &request, response); err != nil {
		return fmt.Errorf("模拟器 %s 失败：%w", method, err)
	}
	return nil
}

func parseImage(data []byte) (int, int, []byte, error) {
	var width, height, oldWidth, oldHeight uint64
	var imageFormat uint64
	var pixels []byte
	for len(data) > 0 {
		n, t, tagLen := protowire.ConsumeTag(data)
		if tagLen < 0 {
			return 0, 0, nil, errors.New("画面响应格式无效")
		}
		data = data[tagLen:]
		switch {
		case n == 1 && t == protowire.BytesType:
			format, k := protowire.ConsumeBytes(data)
			if k < 0 {
				return 0, 0, nil, errors.New("画面格式字段无效")
			}
			data = data[k:]
			for len(format) > 0 {
				fn, ft, fl := protowire.ConsumeTag(format)
				if fl < 0 {
					return 0, 0, nil, errors.New("画面尺寸格式无效")
				}
				format = format[fl:]
				if (fn == 1 || fn == 3 || fn == 4) && ft == protowire.VarintType {
					value, vl := protowire.ConsumeVarint(format)
					if vl < 0 {
						return 0, 0, nil, errors.New("画面尺寸无效")
					}
					if fn == 1 {
						imageFormat = value
					} else if fn == 3 {
						width = value
					} else {
						height = value
					}
					format = format[vl:]
				} else {
					skip := protowire.ConsumeFieldValue(fn, ft, format)
					if skip < 0 {
						return 0, 0, nil, errors.New("画面格式无效")
					}
					format = format[skip:]
				}
			}
		case (n == 2 || n == 3) && t == protowire.VarintType:
			value, k := protowire.ConsumeVarint(data)
			if k < 0 {
				return 0, 0, nil, errors.New("画面尺寸字段无效")
			}
			if n == 2 {
				oldWidth = value
			} else {
				oldHeight = value
			}
			data = data[k:]
		case n == 4 && t == protowire.BytesType:
			value, k := protowire.ConsumeBytes(data)
			if k < 0 {
				return 0, 0, nil, errors.New("画面数据字段无效")
			}
			pixels = value
			data = data[k:]
		default:
			k := protowire.ConsumeFieldValue(n, t, data)
			if k < 0 {
				return 0, 0, nil, errors.New("画面响应字段无效")
			}
			data = data[k:]
		}
	}
	if width == 0 {
		width = oldWidth
	}
	if height == 0 {
		height = oldHeight
	}
	if imageFormat != 1 || width == 0 || height == 0 || width > 8192 || height > 8192 || width*height > 32*1024*1024 || uint64(len(pixels)) != width*height*4 {
		return 0, 0, nil, errors.New("模拟器画面尺寸或像素数据无效")
	}
	return int(width), int(height), pixels, nil
}

func (c *Controller) Capture(dst *image.RGBA) (*image.RGBA, error) {
	request := rawProto(appendInt(nil, 1, 1)) // RGBA8888
	response := frameResponse{frame: dst}
	if err := c.invoke("getScreenshot", request, &response, 3*time.Second); err != nil {
		return nil, err
	}
	dst = response.frame
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	c.mu.Lock()
	c.width, c.height = w, h
	c.mu.Unlock()
	return dst, nil
}

const MaxTouches = 10

type TouchPoint = display.TouchPoint

func (c *Controller) appendTouch(dst []byte, p TouchPoint, pressure int) []byte {
	var contact []byte
	contact = appendInt(contact, 1, uint64(p.X))
	contact = appendInt(contact, 2, uint64(p.Y))
	contact = appendInt(contact, 3, uint64(c.identifier|uint32(p.ID)))
	contact = appendInt(contact, 4, uint64(pressure))
	return appendBytes(dst, 1, contact)
}

// SetTouches replaces this controller's contact snapshot. A missing ID is
// released, and Close releases all IDs that may have reached the emulator.
func (c *Controller) SetTouches(points []TouchPoint) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(points) > MaxTouches {
		return errors.New("触点不能超过 10 个")
	}
	next := make(map[int]TouchPoint, len(points))
	for _, p := range points {
		if p.ID < 0 || p.ID >= MaxTouches {
			return errors.New("触点编号必须在 0 到 9 之间")
		}
		if _, exists := next[p.ID]; exists {
			return errors.New("触点编号重复")
		}
		if p.X < 0 || p.Y < 0 || p.X >= c.width || p.Y >= c.height {
			return errors.New("触点超出范围")
		}
		next[p.ID] = p
	}
	if len(next) > 0 && c.touchLockFile == nil {
		if err := c.lockTouch(); err != nil {
			return err
		}
	}
	var request []byte
	for id := 0; id < MaxTouches; id++ {
		if old, active := c.contacts[id]; active {
			if _, keep := next[id]; !keep {
				request = c.appendTouch(request, old, 0)
			}
		}
	}
	for _, p := range points {
		request = c.appendTouch(request, p, 1024)
	}
	if len(request) == 0 {
		return nil
	}
	// A timeout can occur after the emulator applies the event. Track the union
	// before sending so a later Close also releases possibly delivered contacts.
	for id, p := range next {
		c.contacts[id] = p
	}
	if _, err := c.call("sendTouch", rawProto(request), 2*time.Second); err != nil {
		return err
	}
	c.contacts = next
	if len(next) == 0 {
		c.unlockTouch()
	}
	return nil
}
func (c *Controller) TouchDown(x, y int) error {
	return c.SetTouches([]TouchPoint{{ID: 0, X: x, Y: y}})
}
func (c *Controller) TouchUp() error { return c.SetTouches(nil) }
func (c *Controller) key(usage uint16, down bool) error {
	if !display.ValidKeyboardUsage(usage) {
		return errors.New("不支持的键盘按键")
	}
	request := appendInt(nil, 3, uint64(0x70000|uint32(usage))) // USB keyboard usage page
	if !down {
		request = appendInt(request, 2, 1)
	}
	_, err := c.call("sendKey", rawProto(request), 2*time.Second)
	return err
}
func (c *Controller) KeyDown(usage uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !display.ValidKeyboardUsage(usage) {
		return errors.New("不支持的键盘按键")
	}
	if c.keyboardDisabled {
		return errors.New("实例未启用硬件键盘；请关闭后重新启动独立实例")
	}
	if c.pressed[usage] {
		if c.uncertainKeys[usage] {
			return errors.New("按键状态不确定，请先释放后重试")
		}
		return nil
	}
	// A failed RPC may have delivered the press before its response was lost.
	// Keep it in the release set until an explicit key-up succeeds.
	c.pressed[usage] = true
	if err := c.key(usage, true); err != nil {
		if c.uncertainKeys == nil {
			c.uncertainKeys = make(map[uint16]bool)
		}
		c.uncertainKeys[usage] = true
		return err
	}
	return nil
}
func (c *Controller) KeyUp(usage uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pressed[usage] {
		return nil
	}
	if err := c.key(usage, false); err != nil {
		return err
	}
	delete(c.pressed, usage)
	delete(c.uncertainKeys, usage)
	return nil
}
func (c *Controller) Close() error {
	c.closeOnce.Do(func() {
		_ = c.TouchUp()
		c.mu.Lock()
		c.unlockTouch()
		for usage := range c.pressed {
			_ = c.key(usage, false)
			delete(c.pressed, usage)
		}
		c.mu.Unlock()
		c.closeErr = c.conn.Close()
	})
	return c.closeErr
}

func (c *Controller) Navigate(action display.Action) error {
	if c.navigate == nil {
		return errors.New("当前连接不支持导航操作")
	}
	return c.navigate(action)
}

// Clipboard and SetClipboard address the Android emulator clipboard. Values
// are only held in memory and are never written to logs or command lines.
func (c *Controller) Clipboard() (string, error) {
	response, err := c.call("getClipboard", nil, 2*time.Second)
	if err != nil {
		return "", err
	}
	var text string
	for len(response) > 0 {
		n, t, k := protowire.ConsumeTag(response)
		if k < 0 {
			return "", errors.New("剪贴板响应无效")
		}
		response = response[k:]
		if n == 1 && t == protowire.BytesType {
			value, k := protowire.ConsumeBytes(response)
			if k < 0 || !utf8.Valid(value) {
				return "", errors.New("剪贴板文本无效")
			}
			text = string(value)
			response = response[k:]
		} else {
			k := protowire.ConsumeFieldValue(n, t, response)
			if k < 0 {
				return "", errors.New("剪贴板响应无效")
			}
			response = response[k:]
		}
	}
	return text, nil
}
func (c *Controller) SetClipboard(text string) error {
	if !utf8.ValidString(text) || len(text) > 64<<10 {
		return errors.New("剪贴板文本无效或超过 64 KiB")
	}
	_, err := c.call("setClipboard", rawProto(appendBytes(nil, 1, []byte(text))), 2*time.Second)
	return err
}
