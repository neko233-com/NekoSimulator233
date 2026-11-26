package main

import (
	"errors"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/display"
)

func runViewerInput(done <-chan struct{}, events <-chan pointerEvent, input *viewerInput, report func(error)) {
	defer input.release()
	var retryTimer *time.Timer
	var retryCh <-chan time.Time
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()
	for {
		select {
		case <-done:
			return
		case ev := <-events:
			if ev.kind == pointerMove {
			coalesce:
				for {
					select {
					case next := <-events:
						if next.kind == pointerMove {
							ev = next
						} else {
							if err := input.handle(ev); err != nil {
								report(err)
							}
							ev = next
							break coalesce
						}
					default:
						break coalesce
					}
				}
			}
			if err := input.handle(ev); err != nil {
				report(err)
			}
		case <-retryCh:
			retryCh = nil
			_ = input.retryReleases()
		}
		if input.hasPendingReleases() && retryCh == nil {
			if retryTimer == nil {
				retryTimer = time.NewTimer(time.Second)
			} else {
				retryTimer.Reset(time.Second)
			}
			retryCh = retryTimer.C
		} else if !input.hasPendingReleases() && retryCh != nil {
			retryTimer.Stop()
			retryCh = nil
		}
	}
}

type viewerInput struct {
	source                                            display.Session
	keyboard                                          display.Keyboard
	multi                                             display.MultiTouch
	pressed                                           map[uint16]bool
	keyReleasePending                                 map[uint16]bool
	touchReleasePending                               bool
	mapped                                            map[uint16]int
	contacts                                          map[int]display.TouchPoint
	touched                                           bool
	stickID                                           int
	stickPressed                                      map[uint16]bool
	stickX, stickY, stickRadius, stickMaxX, stickMaxY int
}

func newViewerInput(source display.Session) *viewerInput {
	v := &viewerInput{source: source, pressed: make(map[uint16]bool), keyReleasePending: make(map[uint16]bool), mapped: make(map[uint16]int), contacts: make(map[int]display.TouchPoint), stickID: -1, stickPressed: make(map[uint16]bool)}
	v.keyboard, _ = source.(display.Keyboard)
	v.multi, _ = source.(display.MultiTouch)
	return v
}

func (v *viewerInput) updateStick() error {
	if v.stickID < 0 {
		return nil
	}
	if len(v.stickPressed) == 0 {
		delete(v.contacts, v.stickID)
		v.stickID = -1
		return v.releaseTouches()
	}
	dx, dy := 0, 0
	if v.stickPressed[0x04] {
		dx--
	}
	if v.stickPressed[0x07] {
		dx++
	}
	if v.stickPressed[0x1a] {
		dy--
	}
	if v.stickPressed[0x16] {
		dy++
	}
	x, y := display.JoystickPosition(v.stickX, v.stickY, v.stickRadius, v.stickMaxX, v.stickMaxY, dx, dy)
	v.contacts[v.stickID] = display.TouchPoint{ID: v.stickID, X: x, Y: y}
	return v.syncTouches()
}

func (v *viewerInput) syncTouches() error {
	points := make([]display.TouchPoint, 0, len(v.contacts))
	for id := 0; id < 10; id++ {
		if p, ok := v.contacts[id]; ok {
			points = append(points, p)
		}
	}
	err := v.multi.SetTouches(points)
	if err == nil {
		v.touchReleasePending = false
	}
	return err
}

func (v *viewerInput) releaseTouches() error {
	err := v.syncTouches()
	if err != nil {
		v.touchReleasePending = true
	}
	return err
}

func (v *viewerInput) releaseKey(usage uint16) error {
	if !v.pressed[usage] {
		delete(v.keyReleasePending, usage)
		return nil
	}
	if err := v.keyboard.KeyUp(usage); err != nil {
		v.keyReleasePending[usage] = true
		return err
	}
	delete(v.pressed, usage)
	delete(v.keyReleasePending, usage)
	return nil
}

func (v *viewerInput) hasPendingReleases() bool {
	return len(v.keyReleasePending) != 0 || v.touchReleasePending
}

// Retry only release obligations. A failed press is not replayed because it
// may already have reached the guest.
func (v *viewerInput) retryReleases() error {
	var result error
	for usage := range v.keyReleasePending {
		result = errors.Join(result, v.releaseKey(usage))
	}
	return errors.Join(result, v.retryTouchRelease())
}

func (v *viewerInput) retryTouchRelease() error {
	if !v.touchReleasePending {
		return nil
	}
	if v.multi != nil {
		return v.releaseTouches()
	}
	if err := v.source.TouchUp(); err != nil {
		return err
	}
	v.touched = false
	v.touchReleasePending = false
	return nil
}

func (v *viewerInput) handle(ev pointerEvent) error {
	switch ev.kind {
	case navigate:
		if nav, ok := v.source.(display.Navigator); ok {
			return nav.Navigate(ev.action)
		}
		return errors.New("当前实例不支持导航操作")
	case releaseKeys:
		var result error
		if v.keyboard != nil {
			for usage := range v.pressed {
				if err := v.releaseKey(usage); err != nil {
					result = err
				}
			}
		}
		for key, id := range v.mapped {
			delete(v.contacts, id)
			delete(v.mapped, key)
		}
		if v.stickID >= 0 {
			delete(v.contacts, v.stickID)
			v.stickID = -1
		}
		clear(v.stickPressed)
		if v.multi != nil {
			if err := v.releaseTouches(); err != nil {
				result = err
			}
		}
		return result
	case keyDown:
		if v.keyboard == nil {
			return errors.New("当前实例不支持键盘输入")
		}
		if v.keyReleasePending[ev.usage] {
			if err := v.releaseKey(ev.usage); err != nil {
				return err
			}
		}
		if v.pressed[ev.usage] {
			return nil
		}
		// A failed send can still have reached the guest; retain the release
		// obligation so key-up or focus loss retries it.
		v.pressed[ev.usage] = true
		if err := v.keyboard.KeyDown(ev.usage); err != nil {
			return err
		}
		return nil
	case keyUp:
		if v.keyboard == nil || !v.pressed[ev.usage] {
			return nil
		}
		return v.releaseKey(ev.usage)
	case mappedDown:
		if v.multi == nil {
			return errors.New("当前实例不支持游戏键位")
		}
		if _, held := v.mapped[ev.usage]; held {
			return nil
		}
		if err := v.retryTouchRelease(); err != nil {
			return err
		}
		for id := 1; id < 10; id++ {
			if _, used := v.contacts[id]; !used {
				v.mapped[ev.usage] = id
				v.contacts[id] = display.TouchPoint{ID: id, X: ev.x, Y: ev.y}
				return v.syncTouches()
			}
		}
		return errors.New("同时按住的游戏键位不能超过 9 个")
	case mappedUp:
		if id, ok := v.mapped[ev.usage]; ok {
			delete(v.mapped, ev.usage)
			delete(v.contacts, id)
			return v.releaseTouches()
		}
		return nil
	case stickDown:
		if v.multi == nil || !joystickKey(ev.usage) {
			return errors.New("当前实例不支持方向摇杆")
		}
		if v.stickPressed[ev.usage] {
			return nil
		}
		if err := v.retryTouchRelease(); err != nil {
			return err
		}
		if v.stickID < 0 {
			for id := 1; id < 10; id++ {
				if _, used := v.contacts[id]; !used {
					v.stickID = id
					break
				}
			}
			if v.stickID < 0 {
				return errors.New("触点已满，无法按下方向摇杆")
			}
			v.stickX, v.stickY, v.stickRadius, v.stickMaxX, v.stickMaxY = ev.x, ev.y, ev.radius, ev.maxX, ev.maxY
			v.stickPressed[ev.usage] = true
			v.contacts[v.stickID] = display.TouchPoint{ID: v.stickID, X: ev.x, Y: ev.y}
			if err := v.syncTouches(); err != nil {
				return err
			}
		} else {
			v.stickPressed[ev.usage] = true
		}
		return v.updateStick()
	case stickUp:
		if !v.stickPressed[ev.usage] {
			return nil
		}
		delete(v.stickPressed, ev.usage)
		if err := v.updateStick(); err != nil {
			v.touchReleasePending = true
			return err
		}
		return nil
	case pointerUp:
		if !v.touched {
			return nil
		}
		if v.multi == nil {
			if err := v.source.TouchUp(); err != nil {
				v.touchReleasePending = true
				return err
			}
			v.touched = false
			v.touchReleasePending = false
			return nil
		}
		v.touched = false
		delete(v.contacts, 0)
		return v.releaseTouches()
	case pointerDown, pointerMove:
		if v.touchReleasePending {
			if err := v.retryTouchRelease(); err != nil {
				return err
			}
		}
		v.touched = true
		if v.multi == nil {
			return v.source.TouchDown(ev.x, ev.y)
		}
		v.contacts[0] = display.TouchPoint{ID: 0, X: ev.x, Y: ev.y}
		return v.syncTouches()
	}
	return errors.New("未知输入事件")
}

func (v *viewerInput) release() {
	_ = v.handle(pointerEvent{kind: releaseKeys})
	if v.multi != nil {
		_ = v.multi.SetTouches(nil)
	} else if v.touched {
		_ = v.source.TouchUp()
	}
}
