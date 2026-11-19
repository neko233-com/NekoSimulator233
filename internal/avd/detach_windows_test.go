//go:build windows

package avd

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestEmulatorDetachedFromConsole(t *testing.T) {
	attr := emulatorProcAttr()
	if !attr.HideWindow {
		t.Fatal("emulator window should stay hidden")
	}
	if attr.CreationFlags&windows.DETACHED_PROCESS == 0 {
		t.Fatalf("emulator must be detached from the launching console, flags=%x", attr.CreationFlags)
	}
	if attr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("emulator should own a new process group, flags=%x", attr.CreationFlags)
	}
}
