// Package display defines the replaceable interface between the host UI and a
// running Android display. Backends own their transport and frame buffers.
package display

import (
	"context"
	"errors"
	"image"
	"math"
)

// ErrDisplayInactive indicates that a streaming display currently has no
// visible image. The stream remains usable when the guest becomes active.
var ErrDisplayInactive = errors.New("模拟器画面暂不可见")

type FrameStream interface {
	Next(dst *image.RGBA) (*image.RGBA, error)
}

// FrameStreamer uses ctx to stop the stream, including a blocked Next call.
type FrameStreamer interface {
	OpenFrameStream(ctx context.Context) (FrameStream, error)
}

type Session interface {
	// Capture fills dst when its size matches. The caller must not provide a
	// buffer still being read by a renderer; it owns the returned frame. Capture
	// may run concurrently with input methods. Close runs after both finish.
	Capture(dst *image.RGBA) (*image.RGBA, error)
	TouchDown(x, y int) error
	TouchUp() error
	Close() error
}

// Keyboard accepts USB HID keyboard-page usage IDs. Implementations must
// release any held keys when closed.
type Keyboard interface {
	KeyDown(usage uint16) error
	KeyUp(usage uint16) error
}

// TouchPoint uses a session-local ID, not a global emulator tracking ID.
type TouchPoint struct{ ID, X, Y int }

// MultiTouch replaces the current set of held fingers for this session.
type MultiTouch interface{ SetTouches([]TouchPoint) error }

type Action uint8

const (
	Back Action = iota + 1
	Home
	Overview
	VolumeUp
	VolumeDown
)

type Navigator interface{ Navigate(action Action) error }

func ValidKeyboardUsage(usage uint16) bool {
	return usage >= 0x04 && usage <= 0xa4 || usage >= 0xe0 && usage <= 0xe7
}

// JoystickPosition converts a digital direction to a circular, screen-clamped
// displacement. The caller validates direction, radius and display bounds.
func JoystickPosition(x, y, radius, maxX, maxY, dx, dy int) (int, int) {
	fx, fy := float64(dx), float64(dy)
	if dx != 0 && dy != 0 {
		fx /= math.Sqrt2
		fy /= math.Sqrt2
	}
	return max(0, min(maxX, x+int(math.Round(fx*float64(radius))))), max(0, min(maxY, y+int(math.Round(fy*float64(radius)))))
}
