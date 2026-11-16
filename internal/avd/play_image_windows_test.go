package avd

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addTestImage(t *testing.T, sdk, tag, build string) string {
	t.Helper()
	dir := filepath.Join(sdk, "system-images", "android-34", tag, "x86_64")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "system.img"), []byte("synthetic image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.prop"), []byte("ro.build.version.incremental="+build+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPlayImageSelectionPreservesExistingInstances(t *testing.T) {
	m := backupFixture(t)
	oldPath := filepath.Join(m.Home, m.Name+".avd", "config.ini")
	old, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	addTestImage(t, m.SDK, "google_apis_playstore", "12077443")
	newerAPI := filepath.Join(m.SDK, "system-images", "android-35", "google_apis", "x86_64", "system.img")
	if err := os.MkdirAll(filepath.Dir(newerAPI), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newerAPI, []byte("synthetic newer API"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateInstance(context.Background(), "PlayNew"); err != nil {
		t.Fatal(err)
	}
	config, err := parseINI(filepath.Join(m.Home, "PlayNew.avd", "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	api, tag, err := systemImageIdentity(config)
	if err != nil || api != 34 || tag != "google_apis_playstore" || config["PlayStore.enabled"] != "true" {
		t.Fatalf("wrong image: %v %s %v", api, tag, err)
	}
	items, err := m.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Name == "PlayNew" && item.ImageTag == "google_apis_playstore" {
			found = true
		}
	}
	if !found {
		t.Fatal("Play image type missing from instance listing")
	}
	after, err := os.ReadFile(oldPath)
	if err != nil || string(after) != string(old) {
		t.Fatal("existing instance metadata changed")
	}
	config["tag.id"] = "google_apis"
	if _, _, err := systemImageIdentity(config); err == nil {
		t.Fatal("mismatched image tag accepted")
	}
	playConfig := filepath.Join(m.Home, "PlayNew.avd", "config.ini")
	b, err := os.ReadFile(playConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playConfig, []byte(strings.Replace(string(b), "tag.id=google_apis_playstore", "tag.id=google_apis", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	bad, err := m.WithName("PlayNew")
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Ensure(); err == nil {
		t.Fatal("mismatched image was accepted for start")
	}
	items, err = m.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	unknown := false
	for _, item := range items {
		if item.Name == "PlayNew" && item.State == "unknown" {
			unknown = true
		}
	}
	if !unknown {
		t.Fatal("mismatched image not reported as unknown")
	}
	config["tag.id"] = "google_apis_playstore"
	config["image.sysdir.1"] = `system-images\android-34\..\google_apis_playstore\x86_64`
	if _, _, err := systemImageIdentity(config); err == nil {
		t.Fatal("noncanonical image path accepted")
	}
}

func TestPlayBackupRequiresSameImageTypeAndBuild(t *testing.T) {
	base := backupFixture(t)
	addTestImage(t, base.SDK, "google_apis_playstore", "12077444")
	if err := base.CreateInstance(context.Background(), "PlaySource"); err != nil {
		t.Fatal(err)
	}
	source, err := base.WithName("PlaySource")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(source.Home, source.Name+".avd")
	if err := os.WriteFile(filepath.Join(dir, "version_num.cache"), []byte("12077444\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "userdata-qemu.img"), []byte("guest-data"), 0600); err != nil {
		t.Fatal(err)
	}
	qcow := filepath.Join(dir, "userdata-qemu.img.qcow2")
	if err := os.WriteFile(qcow, nil, 0600); err != nil {
		t.Fatal(err)
	}
	release, err := source.guardSystemBuild()
	if err != nil {
		t.Fatalf("Play image build rejected: %v", err)
	}
	release()
	playProps := filepath.Join(source.SDK, "system-images", "android-34", "google_apis_playstore", "x86_64", "build.prop")
	if err := os.WriteFile(playProps, []byte("ro.build.version.incremental=99999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if release, err := source.guardSystemBuild(); err == nil {
		release()
		t.Fatal("changed Play image build accepted")
	}
	if err := os.WriteFile(playProps, []byte("ro.build.version.incremental=12077444\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(qcow); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "play.nekobak")
	if err := source.ExportBackup(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backupManifest
	for _, file := range z.File {
		if file.Name != "manifest.json" {
			continue
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || json.Unmarshal(b, &manifest) != nil {
			t.Fatal("invalid generated manifest")
		}
	}
	_ = z.Close()
	if manifest.Format != 4 || manifest.Settings.ImageTag != "google_apis_playstore" {
		t.Fatalf("lost image identity: %+v", manifest.Settings)
	}
	forged := filepath.Join(t.TempDir(), "pretend-v3.nekobak")
	rewriteKeymapArchive(t, archive, forged, "play-as-v3")
	forgedTarget := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "forged"), Name: "Forged"}
	if err := forgedTarget.ImportBackup(context.Background(), forged); err == nil {
		t.Fatal("legacy backup accepted a Play image tag")
	}
	targetSDK := t.TempDir()
	addTestImage(t, targetSDK, "google_apis", "12077444")
	target := Manager{SDK: targetSDK, Home: filepath.Join(t.TempDir(), "target"), Name: "Restored"}
	if err := target.ImportBackup(context.Background(), archive); err == nil || !strings.Contains(err.Error(), "google_apis_playstore") {
		t.Fatalf("accepted wrong image type: %v", err)
	}
	playDir := addTestImage(t, targetSDK, "google_apis_playstore", "99999999")
	if err := target.ImportBackup(context.Background(), archive); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("accepted wrong image build: %v", err)
	}
	if err := os.WriteFile(filepath.Join(playDir, "build.prop"), []byte("ro.build.version.incremental=12077444\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportBackup(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	config, err := parseINI(filepath.Join(target.Home, target.Name+".avd", "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if _, tag, err := systemImageIdentity(config); err != nil || tag != "google_apis_playstore" {
		t.Fatalf("restored wrong image: %s %v", tag, err)
	}
	got, err := os.ReadFile(filepath.Join(target.Home, target.Name+".avd", "userdata-qemu.img"))
	if err != nil || string(got) != "guest-data" {
		t.Fatalf("restored disk: %q %v", got, err)
	}
}
