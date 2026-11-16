package avd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

const (
	tapTapPendingFile    = "neko-taptap-auto.pending"
	tapTapDoneFile       = "neko-taptap-auto.done"
	tapTapFailureFile    = "neko-taptap-auto.failed"
	tapTapLockFile       = "neko-taptap-auto.lock"
	tapTapMarkerContents = "taptap-v1\n"
)

func readTapTapMarker(path string) (bool, error) {
	f, err := openOfflineImage(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65))
	if err != nil || string(data) != tapTapMarkerContents {
		return false, errors.New("TapTap 自动安装状态无效")
	}
	return true, nil
}

// TapTapAutoStatus reports only this project's first-run task, not the
// current Android package state. Legacy and restored instances are untouched.
func (m Manager) TapTapAutoStatus() (string, error) {
	dir, err := ownedAVD(m)
	if err != nil {
		return "", err
	}
	pending, err := readTapTapMarker(filepath.Join(dir, tapTapPendingFile))
	if err != nil {
		return "", err
	}
	if !pending {
		return "not_scheduled", nil
	}
	done, err := readTapTapMarker(filepath.Join(dir, tapTapDoneFile))
	if err != nil {
		return "", err
	}
	if done {
		return "complete", nil
	}
	failed, err := readTapTapMarker(filepath.Join(dir, tapTapFailureFile))
	if err != nil {
		return "", err
	}
	if failed {
		return "retry_pending", nil
	}
	return "pending", nil
}

// The per-instance lock avoids duplicate downloads across GUI and MCP
// processes. It never follows an existing link and is held outside the VM
// lifecycle lock so downloading does not prevent stopping the emulator.
func (m Manager) tryTapTapAutoLock(dir string) (func(), error) {
	path, err := windows.UTF16PtrFromString(filepath.Join(dir, tapTapLockFile))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("TapTap 自动安装锁不可用或包含链接")
	}
	var overlap windows.Overlapped
	if err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, &overlap); _ = windows.CloseHandle(h) }, nil
}

func (m Manager) markTapTapAutoDone() error {
	status, err := m.TapTapAutoStatus()
	if err != nil || status == "not_scheduled" || status == "complete" {
		return err
	}
	dir, err := ownedAVD(m)
	if err != nil {
		return err
	}
	err = writeNew(filepath.Join(dir, tapTapDoneFile), tapTapMarkerContents)
	if errors.Is(err, os.ErrExist) {
		done, readErr := readTapTapMarker(filepath.Join(dir, tapTapDoneFile))
		if readErr == nil && done {
			return nil
		}
	}
	return err
}

func (m Manager) runPendingTapTap(ctx context.Context, instance Instance, install func(context.Context, Instance) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status, err := m.TapTapAutoStatus()
	if err != nil || status == "not_scheduled" || status == "complete" {
		return err
	}
	dir, err := ownedAVD(m)
	if err != nil {
		return err
	}
	unlock, err := m.tryTapTapAutoLock(dir)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil // Another process is already doing the work.
	}
	if err != nil {
		return err
	}
	defer unlock()
	status, err = m.TapTapAutoStatus()
	if err != nil || status == "not_scheduled" || status == "complete" {
		return err
	}
	if err := install(ctx, instance); err != nil {
		// A fixed marker avoids persisting network URLs or sensitive errors.
		if !errors.Is(err, context.Canceled) {
			_ = writeNew(filepath.Join(dir, tapTapFailureFile), tapTapMarkerContents)
		}
		return err
	}
	return m.markTapTapAutoDone()
}

func (m Manager) scheduleTapTap(instance Instance) {
	status, err := m.TapTapAutoStatus()
	if err != nil || status == "not_scheduled" || status == "complete" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = m.runPendingTapTap(ctx, instance, func(ctx context.Context, i Instance) error {
			_, err := m.InstallTapTap(ctx, i)
			return err
		})
	}()
}
