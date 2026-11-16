//go:build windows

package avd

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func makeRuntimeArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, content := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBundledRuntimeExtractionAndDiscovery(t *testing.T) {
	image := "system-images/android-34/google_apis_playstore/x86_64/"
	archive := makeRuntimeArchive(t, map[string]string{
		"emulator/emulator.exe": "emulator", "platform-tools/adb.exe": "adb", "build-tools/35.0.0/aapt.exe": "aapt",
		image + "system.img": "image", image + "package.xml": "package",
		image + "source.properties": "AndroidVersion.ApiLevel=34\nSystemImage.Abi=x86_64\nSystemImage.TagId=google_apis_playstore\nPkg.Revision=1\n",
		image + "build.prop":        "ro.build.version.incremental=1\n",
	})
	local := t.TempDir()
	if err := PrepareBundledRuntime(context.Background(), archive, local); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	m, err := DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(local, "NekoSimulator233-data", "runtime", "sdk"); m.SDK != want {
		t.Fatalf("SDK = %q; want %q", m.SDK, want)
	}
	if err := os.WriteFile(filepath.Join(m.SDK, "keep"), []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareBundledRuntime(context.Background(), archive, local); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(m.SDK, "keep")); err != nil || string(data) != "user" {
		t.Fatal("existing runtime not preserved")
	}
}

func TestBundledRuntimeRejectsTraversalAndIncompletePayload(t *testing.T) {
	for _, files := range []map[string]string{{"../outside": "bad"}, {"emulator/emulator.exe": "incomplete"}} {
		local := t.TempDir()
		if err := PrepareBundledRuntime(context.Background(), makeRuntimeArchive(t, files), local); err == nil {
			t.Fatal("invalid archive accepted")
		}
		if _, err := os.Stat(filepath.Join(local, "NekoSimulator233-data", "runtime", "sdk")); !os.IsNotExist(err) {
			t.Fatal("invalid runtime published")
		}
	}
}
