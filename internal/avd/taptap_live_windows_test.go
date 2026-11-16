package avd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/provision"
)

// This opt-in check downloads the real APK but does not boot or access an AVD.
func TestLiveTapTapDownloadAndPackageIdentity(t *testing.T) {
	if testing.Short() || os.Getenv("NEKO_TEST_TAPTAP_DOWNLOAD") != "1" {
		t.Skip("set NEKO_TEST_TAPTAP_DOWNLOAD=1 to download and inspect the official APK")
	}
	m, err := DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	aapt, err := findAAPT(m.SDK)
	if err != nil || aapt == "" {
		t.Fatalf("an existing SDK aapt is required: %v", err)
	}
	path := filepath.Join(t.TempDir(), "official-taptap.apk")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, downloadErr := provision.DownloadTapTap(ctx, f)
	closeErr := f.Close()
	if downloadErr != nil {
		t.Fatal(downloadErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != result.Bytes || len(result.SHA256) != 64 {
		t.Fatalf("download size or digest invalid: %+v %v", result, err)
	}
	if err := m.verifyTapTapAPK(ctx, path); err != nil {
		t.Fatal(err)
	}
	badging, err := exec.CommandContext(ctx, aapt, "dump", "badging", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(badging), "\n") {
		if strings.HasPrefix(line, "package: ") || strings.HasPrefix(line, "sdkVersion:") || strings.HasPrefix(line, "native-code:") {
			t.Log(strings.TrimSpace(line))
		}
	}
	t.Logf("downloaded %d bytes; sha256=%s", result.Bytes, result.SHA256)
}
