package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.json")
	if m, err := Load(path, "0.1.4"); err != nil || m != nil {
		t.Fatalf("missing manifest: %v %v", m, err)
	}
	data := []byte("example MSI bytes")
	digest := sha256.Sum256(data)
	m := Manifest{Version: "0.1.5", MSI: filepath.Join(dir, "test.msi"), SHA256: hex.EncodeToString(digest[:])}
	write := func(m Manifest) {
		t.Helper()
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(m)
	if got, err := Load(path, "0.1.4"); err != nil || got == nil {
		t.Fatalf("valid update: %v %v", got, err)
	}
	if got, err := Load(path, "0.1.5"); err != nil || got != nil {
		t.Fatalf("same version: %v %v", got, err)
	}
	if err := VerifyHash(strings.NewReader(string(data)), m.SHA256); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHash(strings.NewReader("changed"), m.SHA256); err == nil {
		t.Fatal("accepted modified MSI")
	}
	for _, bad := range []Manifest{
		{Version: "0.1.5.2", MSI: m.MSI, SHA256: m.SHA256},
		{Version: "256.1.5", MSI: m.MSI, SHA256: m.SHA256},
		{Version: "0.1.5", MSI: "relative.msi", SHA256: m.SHA256},
		{Version: "0.1.5", MSI: m.MSI, SHA256: "00"},
	} {
		write(bad)
		if _, err := Load(path, "0.1.4"); err == nil {
			t.Fatalf("accepted invalid manifest: %+v", bad)
		}
	}
}

func TestVerifyActualMSI(t *testing.T) {
	path := os.Getenv("NEKO_TEST_MSI")
	if path == "" {
		t.Skip("set NEKO_TEST_MSI to a built MSI")
	}
	want := os.Getenv("NEKO_TEST_MSI_VERSION")
	if want == "" {
		want = "0.1.4"
	}
	if err := VerifyPackage(path, want); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPackage(path, "9.9.9"); err == nil {
		t.Fatal("accepted incorrect version")
	}
}

func TestInstalledProductVersion(t *testing.T) {
	want := os.Getenv("NEKO_TEST_INSTALLED_VERSION")
	if want == "" {
		t.Skip("set NEKO_TEST_INSTALLED_VERSION for an installed test product")
	}
	got, err := installedProductVersion()
	if err != nil || got != want {
		t.Fatalf("installed version = %q, %v; want %q", got, err, want)
	}
}

func TestRebootMarker(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(markerPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if pending, err := PendingReboot(); err != nil || pending {
		t.Fatalf("no marker: %v %v", pending, err)
	}
	if err := markRebootRequired(); err != nil {
		t.Fatal(err)
	}
	if pending, err := PendingReboot(); err != nil || !pending {
		t.Fatalf("same boot: %v %v", pending, err)
	}
	current := bootID()
	if current == nil {
		t.Skip("Windows boot ID unavailable")
	}
	other := *current + 1
	b, _ := json.Marshal(rebootMarker{BootID: &other})
	if err := os.WriteFile(markerPath(), b, 0600); err != nil {
		t.Fatal(err)
	}
	if pending, err := PendingReboot(); err != nil || pending {
		t.Fatalf("new boot: %v %v", pending, err)
	}
}

func TestProcessGuardHelper(t *testing.T) {
	if os.Getenv("NEKO_TEST_GUARD_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestUpdateDefersForRunningApplication(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "NekoSimulator233.exe")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(exe)
	if err != nil {
		_ = src.Close()
		t.Fatal(err)
	}
	_, err = io.Copy(dst, src)
	_ = src.Close()
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestProcessGuardHelper$")
	cmd.Env = append(os.Environ(), "NEKO_TEST_GUARD_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	if err = ensureNotRunning(exe); err == nil {
		t.Fatal("update did not detect running application")
	}
}
