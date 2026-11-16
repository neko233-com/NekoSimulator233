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

func TestTapTapInstallRejectsReusedInstanceEndpoint(t *testing.T) {
	a := Instance{Name: "Alpha", Serial: "emulator-5554", port: 8554, pid: 42, token: "original"}
	if !sameInstance(a, a) {
		t.Fatal("same running instance rejected")
	}
	for _, b := range []Instance{
		{Name: "Alpha", Serial: a.Serial, port: a.port, pid: a.pid, token: "different"},
		{Name: "Alpha", Serial: a.Serial, port: a.port, pid: 43, token: a.token},
		{Name: "Alpha", Serial: "emulator-5556", port: a.port, pid: a.pid, token: a.token},
		{},
	} {
		if sameInstance(a, b) {
			t.Fatal("changed process or endpoint accepted")
		}
	}
}

func TestTapTapInstallDoesNotRunWhenInstanceIsGone(t *testing.T) {
	m := backupFixture(t)
	original := Instance{Name: m.Name, Serial: "emulator-5554", port: 8554, pid: 42, token: "synthetic"}
	called := false
	err := m.withCurrentInstance(context.Background(), original, func(context.Context, Instance) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatal("operation was allowed without the matching live instance")
	}
	if err := m.InstallAPKs(context.Background(), original, []string{"does-not-matter.apk"}, false); err == nil {
		t.Fatal("APK installation accepted a stale instance")
	}
}
