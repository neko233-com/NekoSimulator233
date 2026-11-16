//go:build windows

package avd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResourceOverridesPreserveConfigurationAndBackup(t *testing.T) {
	m := backupFixture(t)
	dir := filepath.Join(m.Home, m.Name+".avd")
	config := filepath.Join(dir, "config.ini")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	want := Resources{Cores: 2, RAMMiB: 1536}
	if err := m.SetResources(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetResources(context.Background())
	if err != nil || got != want {
		t.Fatalf("resource settings = %+v, %v", got, err)
	}
	after, err := os.ReadFile(config)
	if err != nil || string(after) != string(before) {
		t.Fatal("resource change modified the AVD configuration")
	}
	backup := filepath.Join(t.TempDir(), "resources.nekobak")
	if err := m.ExportBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	target := Manager{SDK: m.SDK, Home: filepath.Join(t.TempDir(), "restored"), Name: "NekoRestore"}
	if err := target.ImportBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	got, err = target.GetResources(context.Background())
	if err != nil || got != want {
		t.Fatalf("restored resource settings = %+v, %v", got, err)
	}
}

func TestResourceOverridesReachEmulatorCommand(t *testing.T) {
	m := backupFixture(t)
	if err := m.SetResources(context.Background(), Resources{Cores: 2, RAMMiB: 1536}); err != nil {
		t.Fatal(err)
	}
	emulator := filepath.Join(m.SDK, "emulator", "emulator.exe")
	if err := os.MkdirAll(filepath.Dir(emulator), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(self, emulator); err != nil {
		t.Skipf("cannot link isolated emulator helper: %v", err)
	}
	adb := filepath.Join(m.SDK, "platform-tools", "adb.exe")
	if err := os.MkdirAll(filepath.Dir(adb), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adb, []byte("unused fake adb"), 0600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "emulator-args.json")
	t.Setenv("NEKO_TEST_FAKE_EMULATOR_RECORD", record)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := m.Start(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fake emulator unexpectedly started: %v", err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	argsText := " " + strings.Join(args, " ") + " "
	for _, want := range []string{" -memory 1536 ", " -cores 2 ", " -feature Vulkan "} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("emulator arguments %q missing %q", args, want)
		}
	}
}

func TestResourceOverridesRejectInvalidAndRunning(t *testing.T) {
	m := backupFixture(t)
	dir := filepath.Join(m.Home, m.Name+".avd")
	path := filepath.Join(dir, "neko-resources.json")
	if err := m.SetResources(context.Background(), Resources{Cores: 0, RAMMiB: 2048}); err == nil {
		t.Fatal("accepted invalid CPU count")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid configuration wrote a file")
	}
	bad := []byte(`{"cores":2,"ram_mib":1536,"unknown":1}`)
	if err := os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.SetResources(context.Background(), Resources{Cores: 2, RAMMiB: 1536}); err == nil {
		t.Fatal("overwrote malformed existing settings")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(bad) {
		t.Fatal("malformed settings changed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	t.Setenv("TMP", temp)
	running := filepath.Join(temp, "avd", "running")
	if err := os.MkdirAll(running, 0700); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	info := fmt.Sprintf("avd.name=%s\navd.dir=%s\nport.serial=5578\ngrpc.port=8559\ngrpc.token=test\n", m.Name, dir)
	if err := os.WriteFile(filepath.Join(running, fmt.Sprintf("pid_%d.ini", pid)), []byte(info), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.SetResources(context.Background(), Resources{Cores: 2, RAMMiB: 1536}); err == nil || !strings.Contains(err.Error(), "运行") {
		t.Fatalf("running instance allowed configuration change: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("running instance settings changed")
	}
}

func TestResourceSettingsDoNotCreateNonPlayFallback(t *testing.T) {
	m := backupFixture(t)
	candidate, err := m.WithName("FreshWithoutPlay")
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.SetResources(context.Background(), Resources{Cores: 2, RAMMiB: 1536}); err == nil {
		t.Fatal("created an instance without a Play image")
	}
	if _, err := os.Stat(filepath.Join(m.Home, "FreshWithoutPlay.avd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected instance directory: %v", err)
	}
}
