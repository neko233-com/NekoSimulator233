package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/neko233-com/NekoSimulator233/internal/update"
	"golang.org/x/sys/windows"
)

const updateCheckInterval = 10 * time.Minute
const updateRequiredIdle = 2 * time.Minute

type lastInputInfo struct{ Size, Tick uint32 }

func sessionIdle() (time.Duration, error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	info := lastInputInfo{Size: uint32(unsafe.Sizeof(lastInputInfo{}))}
	ok, _, err := user32.NewProc("GetLastInputInfo").Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0, fmt.Errorf("读取会话空闲状态失败：%w", err)
	}
	now, _, _ := kernel32.NewProc("GetTickCount").Call()
	return idleDuration(uint32(now), info.Tick)
}

func idleDuration(now, last uint32) (time.Duration, error) {
	delta := now - last
	// A future or ambiguous 32-bit timestamp cannot safely establish idleness.
	if delta > 0x7fffffff {
		return 0, errors.New("会话空闲时间不确定")
	}
	return time.Duration(delta) * time.Millisecond, nil
}

func updateKey(m *update.Manifest) string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("%d:%s%d:%s%d:%s", len(m.Version), m.Version, len(m.SHA256), m.SHA256, len(m.MSI), m.MSI)
}

type periodicUpdateOps struct {
	idle        func() (time.Duration, error)
	lookup      func() (*update.Manifest, string, string)
	checkOthers func(string) error
	start       func(*update.Manifest, string, string, func() error) error
}

var errUpdateDeferred = errors.New("自动更新条件已变化")

// attemptPeriodicUpdate is called off the UI thread after the UI is marked
// busy. All state that can change while lookup runs is checked again before
// starting the updater; the worker repeats process checks after parent exit.
func attemptPeriodicUpdate(suppressed string, ops periodicUpdateOps) (bool, string) {
	idle, err := ops.idle()
	if err != nil || idle < updateRequiredIdle {
		return false, ""
	}
	m, exe, message := ops.lookup()
	if m == nil {
		return false, message
	}
	if updateKey(m) == suppressed {
		return false, "上次自动更新未完成；更改本地更新清单后再试"
	}
	if err := ops.checkOthers(exe); err != nil {
		return false, "更新暂缓：" + err.Error()
	}
	idle, err = ops.idle()
	if err != nil || idle < updateRequiredIdle {
		return false, ""
	}
	guard := func() error {
		idle, err := ops.idle()
		if err != nil || idle < updateRequiredIdle {
			return errUpdateDeferred
		}
		if err := ops.checkOthers(exe); err != nil {
			return fmt.Errorf("%w：%v", errUpdateDeferred, err)
		}
		return nil
	}
	if err := ops.start(m, version, exe, guard); err != nil {
		if errors.Is(err, errUpdateDeferred) {
			return false, "更新暂缓，将在空闲后重试"
		}
		return false, "更新准备失败: " + err.Error()
	}
	return true, ""
}

func (a *app) periodicUpdateEligible() bool {
	return !a.busy && !a.updateInProgress && a.backupCancel == nil && len(a.viewers) == 0 && len(a.resources) == 0 && strings.TrimSpace(a.newInstance) == ""
}

func (a *app) startPeriodicUpdates() {
	done := make(chan struct{})
	a.window.OnClosed(func() { close(done) })
	go func() {
		ticker := time.NewTicker(updateCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				a.window.Update(func() { a.tryPeriodicUpdate() })
			}
		}
	}()
}

func (a *app) tryPeriodicUpdate() {
	if !a.periodicUpdateEligible() {
		return
	}
	a.busy, a.updateInProgress = true, true
	suppressed := a.suppressedUpdate
	go func() {
		started, message := attemptPeriodicUpdate(suppressed, periodicUpdateOps{
			idle:        sessionIdle,
			lookup:      func() (*update.Manifest, string, string) { return lookupUpdate(false) },
			checkOthers: update.EnsureNoOtherProcesses,
			start:       update.StartGuarded,
		})
		a.window.Update(func() {
			a.busy, a.updateInProgress = false, false
			if message != "" {
				a.updateMessage = message
			}
			if started {
				mygo.App.Quit()
				return
			}
			if a.closeAfterUpdate {
				a.closeAfterUpdate = false
				a.window.Close()
			}
		})
	}()
}
