//go:build windows

package avd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDeviceInfoReadsSelectedGuestProperties(t *testing.T) {
	root := t.TempDir()
	adb := filepath.Join(root, "platform-tools", "adb.exe")
	if err := os.MkdirAll(filepath.Dir(adb), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(self, adb); err != nil {
		t.Skipf("cannot link isolated fake adb executable: %v", err)
	}
	t.Setenv("NEKO_TEST_FAKE_ADB_RECORD", filepath.Join(root, "args.json"))
	t.Setenv("NEKO_TEST_FAKE_ADB_PROPERTIES", "1")
	m := Manager{SDK: root}
	info, err := m.DeviceInfo(context.Background(), Instance{Serial: "emulator-5554"})
	if err != nil || info.API != 30 || info.NativeBridge != "libndk_translation.so" || !slices.Equal(info.ABIs, []string{"x86_64", "x86", "arm64-v8a", "armeabi-v7a", "armeabi"}) {
		t.Fatalf("device info = %+v, %v", info, err)
	}
}

func TestParseABIListRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", ",", "x86_64,broken value", "x86_64\narm64-v8a"} {
		if _, err := parseABIList(input); err == nil {
			t.Fatalf("accepted invalid ABI list %q", input)
		}
	}
}
