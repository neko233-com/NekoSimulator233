//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/neko233-com/NekoSimulator233/internal/syncctl"
)

func newProfileTestViewer(t *testing.T) *emulatorViewer {
	t.Helper()
	store := &keymapStore{path: filepath.Join(t.TempDir(), "keymap.json")}
	bindings, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	return &emulatorViewer{
		app:      &app{syncHub: syncctl.NewHub()},
		name:     "TestDev",
		keymap:   store,
		bindings: bindings,
		width:    100,
		height:   100,
		events:   make(chan pointerEvent, 16),
		done:     make(chan struct{}),
	}
}

func TestKeymapProfilesPreserveAndIsolateBindings(t *testing.T) {
	v := newProfileTestViewer(t)
	original := mappedPoint{X: 0.2, Y: 0.3}
	if err := v.setBinding(5, &original); err != nil {
		t.Fatal(err)
	}
	stick := &joystickConfig{Center: mappedPoint{X: 0.5, Y: 0.5}, Radius: 0.12}
	if err := v.setJoystick(stick); err != nil {
		t.Fatal(err)
	}
	if err := v.createKeymapProfile("游戏方案"); err != nil {
		t.Fatal(err)
	}
	profilePath := v.keymap.path
	changed := mappedPoint{X: 0.7, Y: 0.8}
	if err := v.setBinding(5, &changed); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.createKeymapProfile("游戏方案"); err == nil {
		t.Fatal("existing profile was overwritten")
	}
	after, err := os.ReadFile(profilePath)
	if err != nil || string(after) != string(before) {
		t.Fatal("duplicate create changed profile data")
	}
	v.held = true
	v.keys = map[uint16]bool{4: true}
	if err := v.switchKeymapProfile(defaultKeymapProfile); err != nil {
		t.Fatal(err)
	}
	if len(v.events) != 2 || (<-v.events).kind != pointerUp || (<-v.events).kind != releaseKeys {
		t.Fatal("switch did not release held mouse and keyboard input")
	}
	if v.bindings[5] != original || v.joystick == nil || *v.joystick != *stick {
		t.Fatalf("default profile changed: %+v %+v", v.bindings, v.joystick)
	}
	if err := v.switchKeymapProfile("游戏方案"); err != nil {
		t.Fatal(err)
	}
	if v.bindings[5] != changed || v.joystick == nil || *v.joystick != *stick {
		t.Fatal("saved profile did not retain its own bindings")
	}
}

func TestKeymapProfileRejectsLinkedDirectory(t *testing.T) {
	v := newProfileTestViewer(t)
	base := v.keymapBase()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Dir(keymapProfilesDir(base))
	if err := os.Symlink(outside, link); err != nil {
		if output, e := exec.Command("cmd.exe", "/c", "mklink", "/J", link, outside).CombinedOutput(); e != nil {
			t.Skipf("directory link unavailable: %v, %s", e, output)
		}
	}
	defer os.Remove(link)
	if err := v.createKeymapProfile("Example"); err == nil {
		t.Fatal("created a profile through a linked directory")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("linked directory changed: %v %v", entries, err)
	}
}

func TestKeymapProfilesRejectUnsafeAndMissingPaths(t *testing.T) {
	v := newProfileTestViewer(t)
	for _, name := range []string{"", defaultKeymapProfile, "..", "../escape", "CON", "name.", "a/b", "a:b", "a\nb"} {
		if err := v.createKeymapProfile(name); err == nil {
			t.Fatalf("accepted unsafe profile name %q", name)
		}
	}
	old := v.keymap
	if err := v.switchKeymapProfile("missing"); err == nil || v.keymap != old {
		t.Fatal("switch to missing profile changed the active profile")
	}
	path := filepath.Join(t.TempDir(), "new.json")
	store := &keymapStore{path: path}
	if err := os.WriteFile(path, []byte("external user data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.save(nil); err == nil {
		t.Fatal("overwrote a profile created by another process")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "external user data" {
		t.Fatal("external profile data changed")
	}
}
