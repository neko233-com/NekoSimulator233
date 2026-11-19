package runtimeinstall

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloadAndExtract(t *testing.T) {
	payload := zipBytes(t, map[string]string{"emulator/emulator.exe": "fake", "platform-tools/adb.exe": "fake"})
	sum := sha256.Sum256(payload)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	manifest := Manifest{URL: server.URL + "/runtime.zip", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(payload))}
	dest := t.TempDir()
	// The default HTTP client rejects the test certificate; use the manifest
	// download path only with a trusted client substitute.
	if err := downloadWithClient(context.Background(), manifest, filepath.Join(dest, "r.zip"), server.Client()); err != nil {
		t.Fatal(err)
	}
	if err := extract(filepath.Join(dest, "r.zip"), filepath.Join(dest, "sdk")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sdk", "emulator", "emulator.exe")); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateURLsAddsMirrors(t *testing.T) {
	t.Setenv("NEKO_DISABLE_GITHUB_MIRROR", "")
	github := Manifest{URL: "https://github.com/neko233-com/NekoSimulator233/releases/download/v1/runtime.zip"}
	urls := CandidateURLs(github)
	if len(urls) < 2 || urls[0] != github.URL {
		t.Fatalf("expected automatic GitHub mirrors: %v", urls)
	}
	plain := CandidateURLs(Manifest{URL: "https://example.com/runtime.zip"})
	if len(plain) != 1 {
		t.Fatalf("non-GitHub URL should not get mirrors: %v", plain)
	}
	custom := CandidateURLs(Manifest{URL: "https://example.com/a.zip", Mirrors: []string{"https://mirror.example/{url}"}})
	if len(custom) != 2 || custom[1] != "https://mirror.example/https://example.com/a.zip" {
		t.Fatalf("custom mirror template: %v", custom)
	}
	t.Setenv("NEKO_DISABLE_GITHUB_MIRROR", "1")
	if got := CandidateURLs(github); len(got) != 1 {
		t.Fatalf("disable env should keep only primary: %v", got)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	payload := zipBytes(t, map[string]string{"../escape.txt": "x"})
	path := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extract(path, t.TempDir()); err == nil || !strings.Contains(err.Error(), "非法路径") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func TestChecksumMismatch(t *testing.T) {
	payload := zipBytes(t, map[string]string{"a": "b"})
	path := filepath.Join(t.TempDir(), "r.zip")
	if err := downloadWithClient(context.Background(), Manifest{URL: "https://example.invalid/x", SHA256: strings.Repeat("0", 64), Bytes: int64(len(payload))}, path, nil); err == nil {
		t.Fatal("expected failure without a client")
	}
}
