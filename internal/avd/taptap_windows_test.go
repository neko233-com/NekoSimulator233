package avd

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/neko233-com/NekoSimulator233/internal/provision"
)

func TestTapTapInstallIsIdempotentAndCleansStaging(t *testing.T) {
	installed := false
	downloads, installs := 0, 0
	var path string
	ops := tapTapSetupOps{
		installed: func(context.Context) (bool, error) { return installed, nil },
		download: func(_ context.Context, w io.Writer) (provision.DownloadResult, error) {
			downloads++
			_, err := w.Write([]byte("APK"))
			return provision.DownloadResult{Bytes: 3, SHA256: "digest"}, err
		},
		verify: func(_ context.Context, p string) error {
			path = p
			b, err := os.ReadFile(p)
			if err != nil || string(b) != "APK" {
				t.Fatalf("staging: %q %v", b, err)
			}
			return nil
		},
		install: func(_ context.Context, p string) error {
			if p != path {
				t.Fatal("wrong staging file")
			}
			installs++
			installed = true
			return nil
		},
	}
	result, err := installTapTap(context.Background(), ops)
	if err != nil || result.AlreadyInstalled || result.DownloadedBytes != 3 || result.SHA256 != "digest" {
		t.Fatalf("installation: %+v %v", result, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging not removed: %v", err)
	}
	result, err = installTapTap(context.Background(), ops)
	if err != nil || !result.AlreadyInstalled || downloads != 1 || installs != 1 {
		t.Fatal("installed app was downloaded or replaced again")
	}
}

func TestTapTapInstallRejectsInvalidOrIncompletePackage(t *testing.T) {
	for _, bad := range []string{"", "package: name='com.taptap.evil' versionCode='1'", "package: name='com.other' versionCode='1'"} {
		if isTapTapBadging([]byte(bad)) {
			t.Fatalf("accepted %q", bad)
		}
	}
	if !isTapTapBadging([]byte("package: name='com.taptap' versionCode='1'\n")) {
		t.Fatal("valid package rejected")
	}
	var path string
	installed := false
	ops := tapTapSetupOps{
		installed: func(context.Context) (bool, error) { return false, nil },
		download: func(_ context.Context, w io.Writer) (provision.DownloadResult, error) {
			_, err := w.Write([]byte("APK"))
			return provision.DownloadResult{}, err
		},
		verify:  func(_ context.Context, p string) error { path = p; return errors.New("invalid APK") },
		install: func(context.Context, string) error { installed = true; return nil },
	}
	if _, err := installTapTap(context.Background(), ops); err == nil || installed {
		t.Fatal("unverified package was installed")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed staging not removed: %v", err)
	}
}
