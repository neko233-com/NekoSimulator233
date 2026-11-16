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

func TestDiscoverManagerUsesToolsOnlySDKAfterCompleteCandidates(t *testing.T) {
	toolsOnly := t.TempDir()
	jar := filepath.Join(toolsOnly, "cmdline-tools", "latest", "lib", "sdkmanager-classpath.jar")
	if err := os.MkdirAll(filepath.Dir(jar), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jar, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_HOME", toolsOnly)
	t.Setenv("ANDROID_SDK_ROOT", "")
	t.Setenv("LOCALAPPDATA", t.TempDir())
	m, err := DiscoverManager()
	if err != nil || m.SDK != toolsOnly {
		t.Fatalf("tools-only SDK: %+v %v", m, err)
	}
	complete := t.TempDir()
	for _, name := range []string{"platform-tools/adb.exe", "emulator/emulator.exe"} {
		p := filepath.Join(complete, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ANDROID_SDK_ROOT", complete)
	m, err = DiscoverManager()
	if err != nil || m.SDK != complete {
		t.Fatalf("complete SDK not preferred: %+v %v", m, err)
	}
}

func TestSDKRuntimeInstallPreflightProgressAndCancellation(t *testing.T) {
	root := t.TempDir()
	m := Manager{SDK: root}
	var installed []string
	var progress []int
	install := func(_ context.Context, root, pkg string, notify func(int)) error {
		installed = append(installed, pkg)
		if notify != nil {
			notify(50)
			notify(100)
		}
		exe := "adb.exe"
		if pkg == "emulator" {
			exe = "emulator.exe"
		}
		dir := filepath.Join(root, pkg)
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, exe), []byte("fake"), 0600)
	}
	if err := m.installSDKRuntime(context.Background(), func(n int) { progress = append(progress, n) }, install); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(installed, []string{"platform-tools", "emulator"}) || !reflect.DeepEqual(progress, []int{25, 50, 75, 100, 100}) {
		t.Fatalf("packages %v progress %v", installed, progress)
	}
	if err := m.installSDKRuntime(context.Background(), nil, func(context.Context, string, string, func(int)) error {
		t.Fatal("installed runtime downloaded again")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	other := Manager{SDK: t.TempDir()}
	dir := filepath.Join(other.SDK, "emulator")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := other.installSDKRuntime(context.Background(), nil, func(context.Context, string, string, func(int)) error {
		t.Fatal("preflight missed incomplete directory")
		return nil
	}); err == nil {
		t.Fatal("incomplete folder accepted")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
		t.Fatal("incomplete folder modified")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := other.installSDKRuntime(ctx, nil, install); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestCompleteSDKRuntimeDoesNotCreateInstallLock(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"platform-tools/adb.exe", "emulator/emulator.exe"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("existing"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := Manager{SDK: root}
	if err := m.installSDKRuntime(context.Background(), nil, func(context.Context, string, string, func(int)) error {
		t.Fatal("complete SDK was modified")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".neko-sdk-install.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("complete SDK was written: %v", err)
	}
}

func TestSDKRuntimeUsesSDKManagerWithoutAcceptingLicenses(t *testing.T) {
	m, record, tools, jar := fakeSDKManager(t)
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_RUNTIME_ROOT", m.SDK)
	if err := m.InstallSDKRuntime(context.Background(), nil); err != nil {
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
	want := []string{"-Dcom.android.sdklib.toolsdir=" + tools, "-classpath", jar, "com.android.sdklib.tool.sdkmanager.SdkManagerCli", "--sdk_root=" + m.SDK, "--install", "emulator", "--channel=0"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("SDK Manager args %q", args)
	}
	other, _, _, _ := fakeSDKManager(t)
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_MODE", "license")
	if err := other.InstallSDKRuntime(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "许可证") {
		t.Fatalf("license rejection hidden: %v", err)
	}
	t.Setenv("NEKO_TEST_FAKE_SDKMANAGER_MODE", "license-exit0")
	if err := other.InstallSDKRuntime(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "许可证") {
		t.Fatalf("successful exit hid a license rejection: %v", err)
	}
}
