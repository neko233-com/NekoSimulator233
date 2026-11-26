//go:build windows

package avd

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIsMissingSDKError(t *testing.T) {
	if IsMissingSDKError(nil) {
		t.Fatal("nil error should not report missing SDK")
	}
	if !IsMissingSDKError(errors.New("未找到可用的 Android SDK 或 Command-line Tools，请设置 ANDROID_HOME")) {
		t.Fatal("expected missing SDK error to be detected")
	}
	if IsMissingSDKError(errors.New("permission denied")) {
		t.Fatal("unrelated error should not report missing SDK")
	}
}

func TestDownloadAndExtractZipStripsPrefix(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("cmdline-tools/bin/sdkmanager.bat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("@echo off\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	dest := t.TempDir()
	if err := downloadAndExtractZip(context.Background(), srv.URL, dest, "cmdline-tools/", nil); err != nil {
		t.Fatalf("downloadAndExtractZip: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "bin", "sdkmanager.bat"))
	if err != nil {
		t.Fatalf("expected extracted file: %v", err)
	}
	if string(got) != "@echo off\n" {
		t.Fatalf("unexpected contents: %q", string(got))
	}
}

func TestWriteStandardSDKAcceptLicenses(t *testing.T) {
	root := t.TempDir()
	if err := writeStandardSDKAcceptLicenses(root); err != nil {
		t.Fatalf("writeStandardSDKAcceptLicenses: %v", err)
	}
	if !fileExists(filepath.Join(root, "licenses", "android-sdk-license")) {
		t.Fatal("expected android-sdk-license file")
	}
}
