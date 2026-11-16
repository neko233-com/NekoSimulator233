//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neko233-com/NekoSimulator233/internal/keymap"
)

func TestRestoredInstanceUsesEmbeddedKeymap(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	name := "Restored"
	legacy := keymapPathForInstance(name)
	resolved, err := resolveKeymapPathForInstance(name)
	if err != nil || resolved != legacy {
		t.Fatalf("original instance path changed: %s, %v", resolved, err)
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "avd", name+".avd", keymap.EmbeddedDirectory)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err = resolveKeymapPathForInstance(name)
	if err != nil || resolved != filepath.Join(dir, "default.json") {
		t.Fatalf("restored instance did not use embedded path: %s, %v", resolved, err)
	}
}
