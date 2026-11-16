//go:build windows

// Package testutil coordinates tests which intentionally share an isolated AVD.
package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func LockAVD(t testing.TB, home string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(home, ".integration-test.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	var o windows.Overlapped
	// Package tests may run concurrently while a full MCP integration test owns
	// the isolated guest. Wait longer than that test's deadline, rather than
	// treating normal serialized use as a lock failure.
	deadline := time.Now().Add(4 * time.Minute)
	for {
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)
		if err == nil {
			t.Cleanup(func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o); _ = f.Close() })
			return
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) || time.Now().After(deadline) {
			f.Close()
			t.Fatalf("lock isolated AVD: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
