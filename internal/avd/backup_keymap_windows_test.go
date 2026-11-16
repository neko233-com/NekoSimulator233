//go:build windows

package avd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neko233-com/NekoSimulator233/internal/keymap"
)

func TestBackupKeymapsRoundTrip(t *testing.T) {
	source := backupFixture(t)
	base := keymap.LegacyPath(filepath.Dir(source.Home), source.Name, defaultName)
	profiles := keymap.ProfilesDir(base)
	if err := os.MkdirAll(profiles, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"":      []byte(`{"version":1,"keys":{"5":{"X":0.2,"Y":0.3}}}`),
		"战斗":    []byte(`{"version":2,"keys":{"5":{"X":0.7,"Y":0.6}},"joystick":{"center":{"X":0.2,"Y":0.8},"radius":0.1}}`),
		"Other": []byte(`{"version":1,"keys":{}}`),
	}
	for name, data := range files {
		path := base
		if name != "" {
			path = filepath.Join(profiles, name+".json")
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "backup.nekobak")
	if err := source.ExportBackup(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	target := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "avd"), Name: "Restored"}
	legacy := keymap.LegacyPath(filepath.Dir(target.Home), target.Name, defaultName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("existing unrelated keymap")
	if err := os.WriteFile(legacy, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportBackup(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	check := func(m Manager) {
		t.Helper()
		dir := filepath.Join(m.Home, m.Name+".avd")
		gotBase, err := keymap.Resolve(filepath.Dir(m.Home), dir, m.Name, defaultName)
		if err != nil || gotBase != filepath.Join(dir, keymap.EmbeddedDirectory, "default.json") {
			t.Fatalf("wrong restored keymap path %s: %v", gotBase, err)
		}
		for name, want := range files {
			path := gotBase
			if name != "" {
				path = filepath.Join(keymap.ProfilesDir(gotBase), name+".json")
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("keymap %q differs: %v", name, err)
			}
		}
	}
	check(target)
	got, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(got, sentinel) {
		t.Fatalf("existing keymap changed: %v", err)
	}
	again := filepath.Join(t.TempDir(), "again.nekobak")
	if err := target.ExportBackup(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	target2 := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "avd"), Name: "RestoredAgain"}
	if err := target2.ImportBackup(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	check(target2)
}

// Rewrite a synthetic archive without modifying the original fixture.
func rewriteKeymapArchive(t *testing.T, source, destination, mode string) {
	t.Helper()
	z, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	f, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, entry := range z.File {
		if mode == "v2" && strings.HasPrefix(entry.Name, "keymaps/") {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "manifest.json" {
			var m backupManifest
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if mode == "v2" {
				m.Format = 2
				m.Keymaps = nil
			}
			if mode == "play-as-v3" {
				m.Format = 3
			}
			if mode == "checksum" {
				m.Keymaps[0].SHA256 = strings.Repeat("0", 64)
			}
			data, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
		}
		name := entry.Name
		if mode == "traversal" && name == "keymaps/default.json" {
			name = "keymaps/profiles/../../escape.json"
		}
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(0600)
		out, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackupKeymapLegacyAndCorruption(t *testing.T) {
	source := backupFixture(t)
	base := keymap.LegacyPath(filepath.Dir(source.Home), source.Name, defaultName)
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte(`{"version":1,"keys":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(t.TempDir(), "source.nekobak")
	if err := source.ExportBackup(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"v2", "checksum", "traversal"} {
		t.Run(mode, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "input.nekobak")
			rewriteKeymapArchive(t, original, archive, mode)
			target := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "avd"), Name: "Target"}
			legacy := keymap.LegacyPath(filepath.Dir(target.Home), target.Name, defaultName)
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			err := target.ImportBackup(context.Background(), archive)
			if mode == "v2" {
				if err != nil {
					t.Fatal(err)
				}
				resolved, err := keymap.Resolve(filepath.Dir(target.Home), filepath.Join(target.Home, target.Name+".avd"), target.Name, defaultName)
				if err != nil || resolved == legacy {
					t.Fatalf("legacy path selected: %s %v", resolved, err)
				}
				if _, err := os.Stat(resolved); !os.IsNotExist(err) {
					t.Fatalf("unexpected restored keymap: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid archive accepted")
			}
			got, err := os.ReadFile(legacy)
			if err != nil || string(got) != "keep" {
				t.Fatalf("existing keymap changed: %v", err)
			}
		})
	}
}

func TestBackupKeymapRejectsInvalidOrBusySource(t *testing.T) {
	source := backupFixture(t)
	base := keymap.LegacyPath(filepath.Dir(source.Home), source.Name, defaultName)
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "output.nekobak")
	if err := source.ExportBackup(context.Background(), output); err == nil {
		t.Fatal("invalid keymap was backed up")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed backup left an output: %v", err)
	}
	if err := os.WriteFile(base, []byte(`{"version":1,"keys":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := keymap.Lock()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- source.ExportBackup(context.Background(), output) }()
	err = <-result
	unlock()
	if err == nil {
		t.Fatal("backup accepted keymap being saved")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("busy backup left an output: %v", err)
	}
}
