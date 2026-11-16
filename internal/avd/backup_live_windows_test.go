//go:build windows

package avd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/testutil"
)

func waitTestStorage(t *testing.T, ctx context.Context, m Manager, serial string) {
	t.Helper()
	for attempt := 0; attempt < 60; attempt++ {
		if _, err := m.adb(ctx, serial, "shell", "test", "-d", "/sdcard/Download"); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("isolated guest external storage did not become ready")
}

func pushTestMarker(t *testing.T, ctx context.Context, m Manager, serial, local, target string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, filepath.Join(m.SDK, "platform-tools", "adb.exe"), "-s", serial, "push", local, target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push synthetic marker failed: %v: %s", err, output)
	}
}

func TestLiveColdBootPreservesGuestFile(t *testing.T) {
	home := os.Getenv("NEKO_TEST_BACKUP_SOURCE_HOME")
	if home == "" {
		t.Skip("set isolated backup source")
	}
	if !strings.HasPrefix(filepath.Base(home), "avd-probe-") {
		t.Fatal("refusing non-validation source")
	}
	testutil.LockAVD(t, home)
	m, err := DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	m.Home = home
	m.Name = "NekoValidation"
	if _, err = m.discover(); err == nil {
		t.Fatal("validation instance was already running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		cleanCtx, c := context.WithTimeout(context.Background(), 20*time.Second)
		defer c()
		_ = m.Stop(cleanCtx)
	})
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "neko-coldboot-" + hex.EncodeToString(random[:]) + ".txt"
	local := filepath.Join(t.TempDir(), "marker.txt")
	if err = os.WriteFile(local, []byte("cold boot synthetic marker"), 0600); err != nil {
		t.Fatal(err)
	}
	i, err := m.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitTestStorage(t, ctx, m, i.Serial)
	pushTestMarker(t, ctx, m, i.Serial, local, "/sdcard/Download/"+name)
	before, err := m.adb(ctx, i.Serial, "shell", "cat", "/sdcard/Download/"+name)
	if err != nil || strings.TrimSpace(string(before)) != "cold boot synthetic marker" {
		t.Fatalf("synthetic marker not readable before shutdown: length=%d error=%v", len(before), err)
	}
	if err = m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	i, err = m.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitTestStorage(t, ctx, m, i.Serial)
	b, err := m.adb(ctx, i.Serial, "shell", "cat", "/sdcard/Download/"+name)
	if err != nil || strings.TrimSpace(string(b)) != "cold boot synthetic marker" {
		t.Fatalf("cold boot lost guest data: length=%d error=%v", len(b), err)
	}
	_, _ = m.adb(ctx, i.Serial, "shell", "rm", "-f", "/sdcard/Download/"+name)
	if err = m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// This test writes only synthetic data into the dedicated validation AVD.
// The input and restored VMs are both controlled by identity, never by a
// hard-coded user-device serial.
func TestLiveBackupRestoreGuestData(t *testing.T) {
	home := os.Getenv("NEKO_TEST_BACKUP_SOURCE_HOME")
	root := os.Getenv("NEKO_TEST_BACKUP_ROOT")
	apk := os.Getenv("NEKO_TEST_APK")
	if home == "" || root == "" || apk == "" {
		t.Skip("set isolated backup source, backup root and synthetic APK")
	}
	if !strings.HasPrefix(filepath.Base(home), "avd-probe-") {
		t.Fatal("refusing a non-validation source home")
	}
	testutil.LockAVD(t, home)
	source, err := DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	source.Home = home
	source.Name = "NekoValidation"
	if _, err = source.discover(); err == nil {
		t.Fatal("validation instance was already running")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	runDir, err := os.MkdirTemp(root, "migration-probe-")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(runDir, "fixture.nekobak")
	target := Manager{SDK: source.SDK, Home: filepath.Join(runDir, "restored"), Name: "NekoRestoredTest"}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	marker := "neko-migration-" + hex.EncodeToString(random[:]) + ".txt"
	localMarker := filepath.Join(runDir, "synthetic-marker.txt")
	if err = os.WriteFile(localMarker, []byte("Neko migration test data"), 0600); err != nil {
		t.Fatal(err)
	}
	const pkg = "invalid.example.nekoagentfixture"
	privateMarker := "files/" + marker
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	sourceDirty := false
	cleanup := func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cleanCancel()
		_ = target.Stop(cleanCtx)
		if sourceDirty {
			i, e := source.Start(cleanCtx)
			if e == nil {
				_, _ = source.adb(cleanCtx, i.Serial, "uninstall", pkg)
				_, _ = source.adb(cleanCtx, i.Serial, "shell", "rm", "-f", "/sdcard/Download/"+marker)
			}
		}
		_ = source.Stop(cleanCtx)
	}
	t.Cleanup(cleanup)
	i, err := source.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := source.adb(ctx, i.Serial, "shell", "pm", "list", "packages", pkg)
	if err != nil || strings.TrimSpace(string(before)) != "" {
		t.Fatal("fixture already exists or package list unavailable")
	}
	if err = source.InstallAPK(ctx, i, apk, false); err != nil {
		t.Fatal(err)
	}
	sourceDirty = true
	if _, err = source.adb(ctx, i.Serial, "shell", "run-as", pkg, "mkdir", "-p", "files"); err != nil {
		t.Fatal(err)
	}
	if _, err = source.adb(ctx, i.Serial, "shell", "run-as", pkg, "touch", privateMarker); err != nil {
		t.Fatal(err)
	}
	waitTestStorage(t, ctx, source, i.Serial)
	pushTestMarker(t, ctx, source, i.Serial, localMarker, "/sdcard/Download/"+marker)
	dataBefore, err := source.adb(ctx, i.Serial, "shell", "cat", "/sdcard/Download/"+marker)
	if err != nil || strings.TrimSpace(string(dataBefore)) != "Neko migration test data" {
		t.Fatalf("synthetic source data not readable: length=%d error=%v", len(dataBefore), err)
	}
	if err = source.ExportBackup(ctx, backup); err == nil {
		t.Fatal("allowed backup while source was running")
	}
	if err = source.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err = source.ExportBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err = target.ImportBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restored, err := target.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitTestStorage(t, ctx, target, restored.Serial)
	data, err := target.adb(ctx, restored.Serial, "shell", "cat", "/sdcard/Download/"+marker)
	if err != nil || strings.TrimSpace(string(data)) != "Neko migration test data" {
		t.Fatalf("shared test data not restored: %v", err)
	}
	if _, err = target.adb(ctx, restored.Serial, "shell", "run-as", pkg, "ls", privateMarker); err != nil {
		t.Fatalf("fixture application private data not restored: %v", err)
	}
	if err = target.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(backup); err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("restored isolated Android instance with synthetic shared and private app data; archive at %s", backup))
}
