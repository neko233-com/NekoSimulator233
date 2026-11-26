//go:build windows

package avd

import (
	"strings"
	"testing"
)

func TestEmbeddedNekoLauncherAndSlimScript(t *testing.T) {
	if len(embeddedNekoLauncherAPK) < 1024 {
		t.Fatalf("expected embedded neko-launcher.apk to be populated, got %d bytes", len(embeddedNekoLauncherAPK))
	}
	pkgs := SlimDebloatPackages()
	if len(pkgs) < 15 {
		t.Fatalf("expected debloat package list, got %d", len(pkgs))
	}
	script := BuildSlimGuestScript(pkgs)
	if !strings.Contains(script, "set-home-activity com.neko233.launcher/.LauncherActivity") {
		t.Fatalf("missing set-home-activity in script: %s", script)
	}
	if !strings.Contains(script, "com.google.android.apps.nexuslauncher") {
		t.Fatalf("missing nexuslauncher disable in script: %s", script)
	}
	if !strings.Contains(script, "window_animation_scale 0.0") {
		t.Fatalf("missing zero-animation tweak in script: %s", script)
	}
}
