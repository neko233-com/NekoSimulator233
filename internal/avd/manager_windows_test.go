//go:build windows

package avd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if record := os.Getenv("NEKO_TEST_FAKE_SDKMANAGER_RECORD"); record != "" {
		args, err := json.Marshal(os.Args[1:])
		if err != nil || os.WriteFile(record, args, 0600) != nil {
			os.Exit(2)
		}
		input, err := io.ReadAll(os.Stdin)
		if err != nil || len(input) != 0 {
			os.Exit(3)
		}
		fmt.Fprintln(os.Stdout, "Downloading 10%")
		switch os.Getenv("NEKO_TEST_FAKE_SDKMANAGER_MODE") {
		case "license":
			fmt.Fprintln(os.Stderr, "License for package not accepted")
			os.Exit(1)
		case "wait":
			time.Sleep(30 * time.Second)
			os.Exit(0)
		}
		image := os.Getenv("NEKO_TEST_FAKE_SDKMANAGER_IMAGE")
		if image == "" || os.MkdirAll(filepath.Dir(image), 0700) != nil || os.WriteFile(image, []byte("fake system image"), 0600) != nil {
			os.Exit(4)
		}
		metadata := map[string]string{
			"source.properties": "Pkg.Revision=1\nAndroidVersion.ApiLevel=34\nSystemImage.Abi=x86_64\nSystemImage.TagId=google_apis_playstore\n",
			"build.prop":        "ro.build.version.incremental=12077443\n",
			"package.xml":       "<fake-package/>\n",
		}
		for name, data := range metadata {
			if os.WriteFile(filepath.Join(filepath.Dir(image), name), []byte(data), 0600) != nil {
				os.Exit(5)
			}
		}
		fmt.Fprintln(os.Stdout, "Installing 100%")
		os.Exit(0)
	}
	if record := os.Getenv("NEKO_TEST_FAKE_EMULATOR_RECORD"); record != "" {
		args, err := json.Marshal(os.Args[1:])
		if err != nil || os.WriteFile(record, args, 0600) != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if record := os.Getenv("NEKO_TEST_FAKE_ADB_RECORD"); record != "" {
		args, err := json.Marshal(os.Args[1:])
		if err != nil || os.WriteFile(record, args, 0600) != nil {
			os.Exit(2)
		}
		if os.Getenv("NEKO_TEST_FAKE_ADB_FAILURE") == "1" {
			fmt.Fprintln(os.Stderr, "Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE]")
			os.Exit(1)
		}
		if os.Getenv("NEKO_TEST_FAKE_ADB_INTENT") != "" {
			fmt.Println(os.Getenv("NEKO_TEST_FAKE_ADB_INTENT"))
			os.Exit(0)
		}
		if os.Getenv("NEKO_TEST_FAKE_ADB_PROPERTIES") == "1" {
			props := map[string]string{"ro.build.version.sdk": "30", "ro.product.cpu.abilist": "x86_64,x86,arm64-v8a,armeabi-v7a,armeabi", "ro.dalvik.vm.native.bridge": "libndk_translation.so"}
			fmt.Println(props[os.Args[len(os.Args)-1]])
			os.Exit(0)
		}
		fmt.Println("Success")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInstallAPKUsesExplicitReplacementAndReportsFailure(t *testing.T) {
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
	record := filepath.Join(root, "args.json")
	t.Setenv("NEKO_TEST_FAKE_ADB_RECORD", record)
	apk := filepath.Join(root, "app with space.apk")
	if err := os.WriteFile(apk, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{SDK: root}
	instance := Instance{Serial: "emulator-5554"}
	split := filepath.Join(root, "split config.apk")
	if err := os.WriteFile(split, []byte("test split"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		replace       bool
		flag, command string
		paths         []string
	}{{false, "-R", "install", []string{apk}}, {true, "-r", "install", []string{apk}},
		{false, "-R", "install-multiple", []string{apk, split}}, {true, "-r", "install-multiple", []string{apk, split}}} {
		if err := m.installAPKsUnchecked(context.Background(), instance, tc.paths, tc.replace); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		if err := json.Unmarshal(data, &args); err != nil {
			t.Fatal(err)
		}
		if want := append([]string{"-s", instance.Serial, tc.command, tc.flag}, tc.paths...); !slices.Equal(args, want) {
			t.Fatalf("adb args %q, want %q", args, want)
		}
	}
	t.Setenv("NEKO_TEST_FAKE_ADB_FAILURE", "1")
	if err := m.installAPKsUnchecked(context.Background(), instance, []string{apk}, true); err == nil || !strings.Contains(err.Error(), "INSTALL_FAILED_UPDATE_INCOMPATIBLE") {
		t.Fatalf("missing Android package manager failure: %v", err)
	}
	t.Setenv("NEKO_TEST_FAKE_ADB_FAILURE", "")
	t.Setenv("NEKO_TEST_FAKE_ADB_INTENT", "Failure [INSTALL_FAILED_MISSING_SPLIT]")
	if err := m.installAPKsUnchecked(context.Background(), instance, []string{apk, split}, false); err == nil || !strings.Contains(err.Error(), "INSTALL_FAILED_MISSING_SPLIT") {
		t.Fatalf("missing Android package manager failure on successful process exit: %v", err)
	}
	t.Setenv("NEKO_TEST_FAKE_ADB_INTENT", "Success\nFailure [INSTALL_FAILED_MISSING_SPLIT]")
	if err := m.installAPKsUnchecked(context.Background(), instance, []string{apk, split}, false); err == nil {
		t.Fatal("failure following success was accepted")
	}
	for _, paths := range [][]string{nil, {apk, apk}, {apk, filepath.Join(root, "missing.apk")}} {
		if err := m.installAPKsUnchecked(context.Background(), instance, paths, false); err == nil {
			t.Fatalf("invalid APK selection accepted: %q", paths)
		}
	}
}

func TestAPKInstallErrorIncludesPackageManagerReason(t *testing.T) {
	underlying := errors.New("exit status 1")
	err := apkInstallError([]byte("Failure [INSTALL_FAILED_ALREADY_EXISTS]"), underlying)
	if !errors.Is(err, underlying) || !strings.Contains(err.Error(), "INSTALL_FAILED_ALREADY_EXISTS") {
		t.Fatalf("package manager reason missing: %v", err)
	}
	err = apkInstallError([]byte("Failure [INSTALL_FAILED_NO_MATCHING_ABIS]"), underlying)
	if !strings.Contains(err.Error(), "Android 11") || !strings.Contains(err.Error(), "INSTALL_FAILED_NO_MATCHING_ABIS") {
		t.Fatalf("ABI mismatch has no actionable guidance: %v", err)
	}
	err = apkInstallError([]byte(strings.Repeat("较", 3000)+"签名错误"), underlying)
	if !strings.Contains(err.Error(), "签名错误") || len([]rune(err.Error())) > 2100 {
		t.Fatalf("unbounded or truncated useful reason: %v", err)
	}
}

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
