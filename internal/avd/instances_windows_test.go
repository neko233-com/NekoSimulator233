//go:build windows

package avd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIndependentInstanceLaunchTargets(t *testing.T) {
	m := backupFixture(t)
	ctx := context.Background()
	for _, name := range []string{"Alpha", "Beta"} {
		if err := m.CreateInstance(ctx, name); err != nil {
			t.Fatal(err)
		}
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
	for _, name := range []string{"Alpha", "Beta"} {
		record := filepath.Join(t.TempDir(), name+"-args.json")
		t.Setenv("NEKO_TEST_FAKE_EMULATOR_RECORD", record)
		candidate, err := m.WithName(name)
		if err != nil {
			t.Fatal(err)
		}
		limited, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err = candidate.Start(limited)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
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
		if !strings.Contains(" "+strings.Join(args, " ")+" ", " -avd "+name+" ") {
			t.Fatalf("wrong AVD target in %q", args)
		}
	}
}

func TestIndependentInstanceCreationAndIsolation(t *testing.T) {
	m := backupFixture(t)
	ctx := context.Background()
	for _, name := range []string{"Alpha", "Beta"} {
		if err := m.CreateInstance(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	alpha, err := m.WithName("Alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := m.WithName("Beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := alpha.SetResources(ctx, Resources{Cores: 2, RAMMiB: 1536}); err != nil {
		t.Fatal(err)
	}
	got, err := beta.GetResources(ctx)
	if err != nil || got != (Resources{Cores: 4, RAMMiB: 2048}) {
		t.Fatalf("settings leaked to another instance: %+v, %v", got, err)
	}
	items, err := m.ListInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, item := range items {
		states[item.Name] = item.State
	}
	if states["Alpha"] != "stopped" || states["Beta"] != "stopped" || states[m.Name] != "stopped" || states[DefaultName] != "not_created" {
		t.Fatalf("unexpected instance list: %+v", items)
	}
	if err := m.CreateInstance(ctx, "alpha"); err == nil {
		t.Fatal("accepted case-insensitive duplicate instance")
	}
}

func TestIndependentInstanceRejectsUnsafeAndIncompleteNames(t *testing.T) {
	m := backupFixture(t)
	for _, name := range []string{"", ".", "..", "../escape", `C:\escape`, "CON", "AUX", "name.", "bad name", "a\nb"} {
		if err := m.CreateInstance(context.Background(), name); err == nil {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
	dir := filepath.Join(m.Home, "Orphan.avd")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "userdata-qemu.img")
	if err := os.WriteFile(data, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateInstance(context.Background(), "Orphan"); err == nil {
		t.Fatal("overwrote orphaned instance")
	}
	if got, err := os.ReadFile(data); err != nil || string(got) != "preserve" {
		t.Fatal("orphaned data changed")
	}
	items, err := m.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name == "Orphan" && item.State == "unknown" && strings.Contains(item.Reason, "不完整") {
			return
		}
	}
	t.Fatalf("orphan not reported as unknown: %+v", items)
}

func TestIndependentInstanceRejectsLinkedDirectory(t *testing.T) {
	m := backupFixture(t)
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "config.ini"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "userdata-qemu.img"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.Home, "Linked.avd")
	if err := os.Symlink(target, dir); err != nil {
		if output, e := exec.Command("cmd.exe", "/c", "mklink", "/J", dir, target).CombinedOutput(); e != nil {
			t.Skipf("directory link unavailable: %v, %s", e, output)
		}
	}
	defer os.Remove(dir) // Remove the test link itself before TempDir cleanup.
	if err := os.WriteFile(filepath.Join(m.Home, "Linked.ini"), []byte("path="+dir+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	linked, err := m.WithName("Linked")
	if err != nil {
		t.Fatal(err)
	}
	if err := linked.Ensure(); err == nil {
		t.Fatal("accepted a linked AVD directory")
	}
	if got, err := os.ReadFile(filepath.Join(target, "userdata-qemu.img")); err != nil || string(got) != "preserve" {
		t.Fatal("linked data changed")
	}
	items, err := m.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name == "Linked" && item.State == "unknown" {
			return
		}
	}
	t.Fatalf("linked instance did not report unknown: %+v", items)
}
