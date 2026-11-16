//go:build windows

package avd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryDoesNotTreatUnknownStateAsStopped(t *testing.T) {
	dir := t.TempDir()
	m := Manager{SDK: t.TempDir(), Home: t.TempDir(), Name: "NekoUnit"}
	path := filepath.Join(dir, "pid_42.ini")
	owned := filepath.Join(m.Home, m.Name+".avd")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	good := fmt.Sprintf("avd.name=%s\navd.dir=%s\nport.serial=5578\ngrpc.port=8559\ngrpc.token=test-token\n", m.Name, owned)
	alive := func(uint32) (bool, error) { return true, nil }
	write(good)
	instance, err := m.discoverIn(dir, alive)
	if err != nil || instance.Serial != "emulator-5578" || instance.pid != 42 {
		t.Fatalf("valid discovery failed: %v", err)
	}
	_, err = m.discoverIn(dir, func(uint32) (bool, error) { return false, nil })
	if !errors.Is(err, errInstanceNotRunning) {
		t.Fatalf("dead process not recognized: %v", err)
	}
	for _, invalid := range []string{
		fmt.Sprintf("avd.name=%s\navd.dir=%s\nport.serial=5578\ngrpc.port=8559\n", m.Name, owned),
		fmt.Sprintf("avd.name=%s\n", m.Name),
		"port.serial=5578\n",
	} {
		write(invalid)
		_, err = m.discoverIn(dir, alive)
		if err == nil || errors.Is(err, errInstanceNotRunning) {
			t.Fatalf("ambiguous live discovery treated as stopped: %v", err)
		}
	}
	write(good)
	_, err = m.discoverIn(dir, func(uint32) (bool, error) { return false, errors.New("access denied") })
	if err == nil || errors.Is(err, errInstanceNotRunning) {
		t.Fatalf("inaccessible process treated as stopped: %v", err)
	}
	write(fmt.Sprintf("avd.name=%s\navd.dir=%s\n", m.Name, filepath.Join(t.TempDir(), "foreign.avd")))
	_, err = m.discoverIn(dir, alive)
	if !errors.Is(err, errInstanceNotRunning) {
		t.Fatalf("foreign instance confused with owned one: %v", err)
	}
}

func TestWaitForOriginalProcessExit(t *testing.T) {
	pid := uint32(os.Getpid())
	alive, err := processRunning(pid)
	if err != nil || !alive {
		t.Fatalf("current process not detected: %v", err)
	}
	if err = waitForProcessExit(context.Background(), 42, func(got uint32) (bool, error) {
		if got != 42 {
			t.Fatal("wrong pid")
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	probeErr := errors.New("probe failed")
	if err = waitForProcessExit(context.Background(), 42, func(uint32) (bool, error) { return false, probeErr }); !errors.Is(err, probeErr) {
		t.Fatalf("process check failure ignored: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = waitForProcessExit(ctx, 42, func(uint32) (bool, error) { return true, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("live process treated as exited: %v", err)
	}
}

func TestEnsurePreservesExistingMetadata(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	img := filepath.Join(sdk, "system-images", "android-34", "google_apis", "x86_64", "system.img")
	if err := os.MkdirAll(filepath.Dir(img), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{SDK: sdk, Home: filepath.Join(root, "avds"), Name: "NekoUnit"}
	if err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(m.Home, m.Name+".avd", "config.ini")
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hw.keyboard=yes") {
		t.Fatal("new instance has no keyboard")
	}
	old := strings.Replace(string(data), "hw.keyboard=yes", "hw.keyboard=no", 1) + "custom.setting=retained\n"
	if err = os.WriteFile(config, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.enableKeyboard(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hw.keyboard=yes") || !strings.Contains(string(data), "custom.setting=retained") {
		t.Fatal("keyboard migration lost settings")
	}
	if err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(m.Home, m.Name+".ini")
	before, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ini, []byte("path=C:\\unrelated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(); err == nil {
		t.Fatal("foreign AVD path accepted")
	}
	after, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) == string(before) || !strings.Contains(string(after), "unrelated") {
		t.Fatal("existing metadata overwritten")
	}
}

func TestEnsureRejectsOrphanedInstanceDirectory(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	image := filepath.Join(sdk, "system-images", "android-34", "google_apis", "x86_64", "system.img")
	if err := os.MkdirAll(filepath.Dir(image), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{SDK: sdk, Home: filepath.Join(root, "avds"), Name: "NekoUnit"}
	dir := filepath.Join(m.Home, m.Name+".avd")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(dir, "userdata-qemu.img")
	if err := os.WriteFile(disk, []byte("guest data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(); err == nil {
		t.Fatal("accepted an existing AVD directory without metadata")
	}
	if got, err := os.ReadFile(disk); err != nil || string(got) != "guest data" {
		t.Fatalf("existing guest disk changed: %q, %v", got, err)
	}
	for _, path := range []string{filepath.Join(dir, "config.ini"), filepath.Join(m.Home, m.Name+".ini")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("created metadata in incomplete AVD: %s, %v", path, err)
		}
	}
}

func TestLifecycleLockHonorsCancellation(t *testing.T) {
	m := Manager{SDK: t.TempDir(), Home: filepath.Join(t.TempDir(), "avds"), Name: "NekoUnit"}
	release, err := m.lockLifecycle(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if unlock, err := m.lockLifecycle(ctx, true); err == nil {
		unlock()
		release()
		t.Fatal("concurrent lifecycle lock succeeded")
	}
	release()
	unlock, err := m.lockLifecycle(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestLiveStartVulkan(t *testing.T) {
	home := os.Getenv("NEKO_TEST_AVD_START_HOME")
	if home == "" {
		t.Skip("set NEKO_TEST_AVD_START_HOME for an isolated emulator launch")
	}
	m, err := DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	m.Home = home
	m.Name = "NekoIntegration"
	t.Cleanup(func() {
		if _, err := m.discover(); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := m.Stop(ctx); err != nil {
			t.Errorf("stop test emulator: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	instance, err := m.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !instance.KeyboardEnabled {
		t.Fatal("new Vulkan instance did not enable hardware keyboard")
	}
	t.Logf("booted owned %s using Vulkan device %s", instance.Serial, instance.VulkanDevice)
	c, err := instance.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	frame, err := c.Capture(nil)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Bounds().Dx() != 720 || frame.Bounds().Dy() != 1280 {
		t.Fatal("unexpected framebuffer dimensions")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	guestData := filepath.Join(m.Home, m.Name+".avd", "userdata-qemu.img")
	if _, err := os.Stat(guestData); err != nil {
		t.Fatalf("guest data missing before shutdown: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(guestData); err != nil {
		t.Fatalf("guest data missing after shutdown: %v", err)
	}
}
