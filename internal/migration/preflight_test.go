package migration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/mumu"
)

func makeImage(t *testing.T, sdk, level string) {
	t.Helper()
	dir := filepath.Join(sdk, "system-images", "android-"+level, "google_apis", "x86_64")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "system.img"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLiveMuMuPreflightReadOnly(t *testing.T) {
	if os.Getenv("NEKO_TEST_LIVE_MUMU_PREFLIGHT") != "1" {
		t.Skip("requires a local MuMu instance and Android SDK")
	}
	manager, err := avd.DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := Check(ctx, "0", manager.SDK, ListLocal)
	if err != nil {
		t.Fatal(err)
	}
	if r.FullMigrationSupported {
		t.Fatal("migration unexpectedly marked complete")
	}
	t.Logf("source version=%s running_reported=%v expected_api=%d image_file_present=%v", r.AndroidVersion, r.SourceRunningReported, r.MatchingImageAPI, r.MatchingImageFilePresent)
}

func TestPreflightReportsIncompleteMigrationWithoutChangingSource(t *testing.T) {
	sdk := t.TempDir()
	makeImage(t, sdk, "34")
	source := mumu.Instance{Index: "0", AndroidVersion: "15.0", Started: true, AndroidStarted: true}
	called := 0
	list := func(context.Context) ([]mumu.Instance, error) { called++; return []mumu.Instance{source}, nil }
	r, err := Check(context.Background(), "0", sdk, list)
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || r.MatchingImageAPI != 35 || r.MatchingImageFilePresent || !r.SourceRunningReported || r.FullMigrationSupported || len(r.AvailableImages) != 3 || !r.AvailableImages[1].SystemImageFilePresent || len(r.Blockers) != 3 {
		t.Fatalf("unexpected report: %+v", r)
	}
	makeImage(t, sdk, "35")
	r, err = Check(context.Background(), "0", sdk, list)
	if err != nil || !r.MatchingImageFilePresent || r.FullMigrationSupported {
		t.Fatalf("same-version image does not establish migration: %+v %v", r, err)
	}
	source.AndroidVersion = "12.0"
	r, err = Check(context.Background(), "0", sdk, list)
	if err != nil || r.MatchingImageAPI != 0 || !strings.Contains(strings.Join(r.Blockers, " "), "无法") {
		t.Fatalf("ambiguous version accepted: %+v %v", r, err)
	}
}

func TestPreflightRejectsInvalidIndexBeforeQueryAndHonorsCancellation(t *testing.T) {
	sdk := t.TempDir()
	list := func(context.Context) ([]mumu.Instance, error) { t.Fatal("unexpected source query"); return nil, nil }
	for _, index := range []string{"", "all", "../0", "01", "4294967296"} {
		if _, err := Check(context.Background(), index, sdk, list); err == nil {
			t.Fatalf("accepted %q", index)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Check(ctx, "0", sdk, list); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
