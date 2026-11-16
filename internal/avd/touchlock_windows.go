package avd

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// A controller owns the emulator's touch slots for the duration of a held
// gesture. This prevents independent GUI and MCP processes from unknowingly
// overflowing the emulator's ten globally shared slots.
func (c *Controller) lockTouch() error {
	if err := os.MkdirAll(filepath.Dir(c.touchLockPath), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(c.touchLockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &c.touchOverlap); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return errors.New("其他窗口或 Agent 正在操作触屏，请在其释放后重试")
		}
		return err
	}
	c.touchLockFile = f
	return nil
}

func (c *Controller) unlockTouch() {
	if c.touchLockFile == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(c.touchLockFile.Fd()), 0, 1, 0, &c.touchOverlap)
	_ = c.touchLockFile.Close()
	c.touchLockFile = nil
}
