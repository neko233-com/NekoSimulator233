package avd

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanceledBackupOperationsPreserveData(t *testing.T) {
	source := backupFixture(t)
	archive := filepath.Join(t.TempDir(), "source.nekobak")
	if err := source.ExportBackup(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(source.Home, source.Name+".avd", "userdata-qemu.img")
	before, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unwritten := filepath.Join(t.TempDir(), "canceled.nekobak")
	if err := source.ExportBackup(ctx, unwritten); !errors.Is(err, context.Canceled) {
		t.Fatalf("export was not canceled: %v", err)
	}
	if _, err := os.Lstat(unwritten); !os.IsNotExist(err) {
		t.Fatal("canceled export created an archive")
	}
	target := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "restored"), Name: "Canceled"}
	if err := target.ImportBackup(ctx, archive); !errors.Is(err, context.Canceled) {
		t.Fatalf("import was not canceled: %v", err)
	}
	for _, path := range []string{filepath.Join(target.Home, target.Name+".avd"), filepath.Join(target.Home, target.Name+".ini")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("canceled import created a target instance")
		}
	}
	after, err := os.ReadFile(dataPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("canceled operation changed source data")
	}
}

func TestImportRejectsLiveTargetEvenWithoutItsMetadata(t *testing.T) {
	source := backupFixture(t)
	archive := filepath.Join(t.TempDir(), "source.nekobak")
	if err := source.ExportBackup(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	target := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "target"), Name: "LiveTarget"}
	temp := t.TempDir()
	t.Setenv("TMP", temp)
	running := filepath.Join(temp, "avd", "running")
	if err := os.MkdirAll(running, 0700); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	dir := filepath.Join(target.Home, target.Name+".avd")
	info := fmt.Sprintf("avd.name=%s\navd.dir=%s\nport.serial=5578\ngrpc.port=8559\ngrpc.token=test\n", target.Name, dir)
	if err := os.WriteFile(filepath.Join(running, fmt.Sprintf("pid_%d.ini", pid)), []byte(info), 0600); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportBackup(context.Background(), archive); err == nil || !strings.Contains(err.Error(), "运行") {
		t.Fatalf("live target accepted: %v", err)
	}
	for _, path := range []string{dir, filepath.Join(target.Home, target.Name+".ini")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("created target while its process was live: %s, %v", path, err)
		}
	}
}

func backupFixture(t *testing.T) Manager {
	t.Helper()
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	image := filepath.Join(sdk, "system-images", "android-34", "google_apis", "x86_64", "system.img")
	if err := os.MkdirAll(filepath.Dir(image), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("test system"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(image), "build.prop"), []byte("ro.build.version.incremental=12077443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{SDK: sdk, Home: filepath.Join(root, "source"), Name: "NekoFixture"}
	if err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Home, m.Name+".avd", "version_num.cache"), []byte("12077443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Home, m.Name+".avd", "userdata-qemu.img"), bytes.Repeat([]byte("test guest data"), 10000), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Home, m.Name+".avd", "encryptionkey.img"), []byte("synthetic key data"), 0600); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBackupRoundTripAndNoOverwrite(t *testing.T) {
	source := backupFixture(t)
	backup := filepath.Join(t.TempDir(), "instance.nekobak")
	if err := source.ExportBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.ExportBackup(context.Background(), backup); err == nil {
		t.Fatal("overwrote an existing backup")
	}
	after, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing backup changed")
	}
	target := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "target"), Name: "NekoRestored"}
	if err = target.ImportBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"userdata-qemu.img", "encryptionkey.img"} {
		a, e := os.ReadFile(filepath.Join(source.Home, source.Name+".avd", name))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(target.Home, target.Name+".avd", name))
		if e != nil || !bytes.Equal(a, b) {
			t.Fatalf("restored %s differs: %v", name, e)
		}
	}
	if _, err = ownedAVD(target); err != nil {
		t.Fatal(err)
	}
	if err = target.ImportBackup(context.Background(), backup); err == nil {
		t.Fatal("overwrote an existing AVD")
	}
	if err = target.Ensure(); err != nil {
		t.Fatalf("restored AVD metadata invalid: %v", err)
	}
}

func TestBackupRejectsLiveWriteAndExternalBacking(t *testing.T) {
	m := backupFixture(t)
	dir := filepath.Join(m.Home, m.Name+".avd")
	path := filepath.Join(dir, "userdata-qemu.img")
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = m.ExportBackup(context.Background(), filepath.Join(t.TempDir(), "busy.nekobak"))
	_ = f.Close()
	if err == nil {
		t.Fatal("backed up a disk open for writing")
	}
	base := []byte(`C:\foreign\disk.img`)
	head := make([]byte, 20+len(base))
	copy(head, []byte("QFI\xfb"))
	binary.BigEndian.PutUint32(head[4:8], 3)
	binary.BigEndian.PutUint64(head[8:16], 20)
	binary.BigEndian.PutUint32(head[16:20], uint32(len(base)))
	copy(head[20:], base)
	if err = os.WriteFile(filepath.Join(dir, "userdata-qemu.img.qcow2"), head, 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.ExportBackup(context.Background(), filepath.Join(t.TempDir(), "unsafe.nekobak")); err == nil || !strings.Contains(err.Error(), "基底") {
		t.Fatalf("external backing accepted: %v", err)
	}
}

func TestImportRejectsDifferentSystemImageBuild(t *testing.T) {
	source := backupFixture(t)
	archive := filepath.Join(t.TempDir(), "instance.nekobak")
	if err := source.ExportBackup(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	props := filepath.Join(source.SDK, "system-images", "android-34", "google_apis", "x86_64", "build.prop")
	if err := os.WriteFile(props, []byte("ro.build.version.incremental=99999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := Manager{SDK: source.SDK, Home: filepath.Join(t.TempDir(), "target"), Name: "NekoRestore"}
	if err := target.ImportBackup(context.Background(), archive); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("system image mismatch was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target.Home, target.Name+".avd")); !os.IsNotExist(err) {
		t.Fatal("version mismatch created an instance")
	}
}

func TestStartGuardRejectsSystemImageMismatch(t *testing.T) {
	m := backupFixture(t)
	dir := filepath.Join(m.Home, m.Name+".avd")
	if err := os.WriteFile(filepath.Join(dir, "userdata-qemu.img.qcow2"), []byte("overlay"), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := m.guardSystemBuild()
	if err != nil {
		t.Fatal(err)
	}
	props := filepath.Join(m.SDK, "system-images", "android-34", "google_apis", "x86_64", "build.prop")
	if f, e := os.OpenFile(props, os.O_WRONLY, 0600); e == nil {
		f.Close()
		release()
		t.Fatal("image version was writable during guarded startup")
	}
	release()
	if err = os.WriteFile(props, []byte("ro.build.version.incremental=99999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if unlock, err := m.guardSystemBuild(); err == nil {
		unlock()
		t.Fatal("allowed start with changed system image")
	}
	if err = os.Remove(filepath.Join(dir, "version_num.cache")); err != nil {
		t.Fatal(err)
	}
	if unlock, err := m.guardSystemBuild(); err == nil {
		unlock()
		t.Fatal("allowed start with missing version cache")
	}
}

func TestImportRejectsTraversalAndCorruption(t *testing.T) {
	m := backupFixture(t)
	for _, mode := range []string{"traversal", "corruption"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.nekobak")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(f)
			name := "images/userdata-qemu.img"
			if mode == "traversal" {
				name = "images/../../escape"
			}
			w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte("bad"))
			sum := sha256.Sum256([]byte("not bad"))
			manifest := backupManifest{Format: 2, Settings: backupSettings{API: 34, SystemBuild: 12077443, Cores: 4, RAM: 2048, Width: 720, Height: 1280, Density: 240, DataSize: "6G"}, Files: []backupFile{{Name: "userdata-qemu.img", Size: 3, SHA256: hex.EncodeToString(sum[:])}}}
			b, _ := json.Marshal(manifest)
			w, _ = zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Store})
			_, _ = w.Write(b)
			if err = zw.Close(); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			target := Manager{SDK: m.SDK, Home: filepath.Join(t.TempDir(), "target"), Name: "NekoBad"}
			if err = target.ImportBackup(context.Background(), path); err == nil {
				t.Fatal("accepted invalid archive")
			}
			if _, err = os.Stat(filepath.Join(target.Home, target.Name+".avd")); !os.IsNotExist(err) {
				t.Fatal("invalid archive left a target instance")
			}
		})
	}
}
