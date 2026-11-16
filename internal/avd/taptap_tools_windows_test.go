package avd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTapTapBuildToolsPreparation(t *testing.T) {
	m, record, tools, jar := fakeSDKManager(t)
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_RUNTIME_ROOT", m.SDK)
	if err := m.installTapTapBuildTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(b, &args); err != nil {
		t.Fatal(err)
	}
	want := []string{"-Dcom.android.sdklib.toolsdir=" + tools, "-classpath", jar, "com.android.sdklib.tool.sdkmanager.SdkManagerCli", "--sdk_root=" + m.SDK, "--install", tapTapBuildToolsPackage, "--channel=0"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("SDK Manager args %q", args)
	}
	if err := m.prepareTapTapBuildTools(context.Background(), func(context.Context, string, string, func(int)) error {
		t.Fatal("existing Build Tools downloaded again")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTapTapBuildToolsPreservesIncompleteAndReportsLicense(t *testing.T) {
	m := Manager{SDK: t.TempDir()}
	path := filepath.Join(m.SDK, "build-tools", "35.0.0", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.prepareTapTapBuildTools(context.Background(), func(context.Context, string, string, func(int)) error {
		t.Fatal("incomplete directory was passed to SDK Manager")
		return nil
	}); err == nil || !strings.Contains(err.Error(), "未覆盖") {
		t.Fatalf("incomplete directory accepted: %v", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep" {
		t.Fatal("existing files changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.installTapTapBuildTools(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	other, _, _, _ := fakeSDKManager(t)
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_MODE", "license")
	if err := other.installTapTapBuildTools(context.Background()); err == nil || !strings.Contains(err.Error(), "许可证") {
		t.Fatalf("license rejection hidden: %v", err)
	}
}
