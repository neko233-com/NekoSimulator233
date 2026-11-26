//go:build windows

package avd

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
)

//go:embed assets/neko-launcher.apk
var embeddedNekoLauncherAPK []byte

const slimAndroidProfileVersion = "neko-slim-v2"

// SlimDebloatPackages lists stock Pixel, GMS background, and phone telephony
// bloatware disabled in user 0 so the Android guest runs lean and fast like MuMu.
func SlimDebloatPackages() []string {
	return []string{
		"com.google.android.apps.nexuslauncher",
		"com.android.launcher3",
		"com.google.android.googlequicksearchbox",
		"com.google.android.apps.wellbeing",
		"com.google.android.as",
		"com.google.android.as.oss",
		"com.google.android.apps.turbo",
		"com.google.android.setupwizard",
		"com.android.provision",
		"com.google.android.youtube",
		"com.google.android.apps.youtube.music",
		"com.google.android.apps.maps",
		"com.google.android.gm",
		"com.google.android.videos",
		"com.google.android.apps.tachyon",
		"com.google.android.apps.docs",
		"com.google.android.calendar",
		"com.android.calendar",
		"com.google.android.contacts",
		"com.android.contacts",
		"com.google.android.dialer",
		"com.android.dialer",
		"com.google.android.apps.messaging",
		"com.android.messaging",
		"com.android.stk",
		"com.google.android.apps.safetyhub",
		"com.google.android.apps.restore",
	}
}

// BuildSlimGuestScript constructs the idempotent guest shell script that sets
// Neko Launcher as default HOME, disables stock bloatware, and applies zero-animation
// gaming performance tuning.
func BuildSlimGuestScript(pkgs []string) string {
	var b strings.Builder
	b.WriteString("cmd package set-home-activity com.neko233.launcher/.LauncherActivity >/dev/null 2>&1 || true\n")
	b.WriteString("cmd settings put global window_animation_scale 0.0 >/dev/null 2>&1 || true\n")
	b.WriteString("cmd settings put global transition_animation_scale 0.0 >/dev/null 2>&1 || true\n")
	b.WriteString("cmd settings put global animator_duration_scale 0.15 >/dev/null 2>&1 || true\n")
	b.WriteString("cmd settings put system time_12_24 24 >/dev/null 2>&1 || true\n")
	b.WriteString("cmd settings put secure immersive_mode_confirmations confirmed >/dev/null 2>&1 || true\n")
	b.WriteString("cmd settings put global heads_up_notifications_enabled 0 >/dev/null 2>&1 || true\n")
	b.WriteString("cmd settings put system screen_off_timeout 2147483647 >/dev/null 2>&1 || true\n")
	b.WriteString("wm dismiss-keyguard >/dev/null 2>&1 || true\n")
	if len(pkgs) > 0 {
		b.WriteString("for p in ")
		b.WriteString(strings.Join(pkgs, " "))
		b.WriteString("; do pm disable-user --user 0 \"$p\" >/dev/null 2>&1 || true; cmd activity force-stop \"$p\" >/dev/null 2>&1 || true; done\n")
	}
	b.WriteString("echo '" + slimAndroidProfileVersion + "' > /data/local/tmp/.neko_slim_profile\n")
	return b.String()
}

// CustomizeSlimAndroidSystem installs the embedded ad-free Neko Launcher APK,
// sets it as the sole default HOME desktop, disables stock Pixel/GMS bloatware,
// and applies zero-animation gaming tweaks. Subsequent boots verify the marker
// in a single fast check (~30ms).
func (m Manager) CustomizeSlimAndroidSystem(ctx context.Context, instance Instance, force bool) error {
	if instance.Serial == "" {
		return errors.New("实例序列号为空")
	}
	if len(embeddedNekoLauncherAPK) == 0 {
		return errors.New("内置 Neko 桌面 APK 为空")
	}

	if !force {
		out, err := m.adb(ctx, instance.Serial, "shell", "sh", "-c",
			"cat /data/local/tmp/.neko_slim_profile 2>/dev/null; pm path com.neko233.launcher 2>/dev/null")
		s := string(out)
		if err == nil && strings.Contains(s, slimAndroidProfileVersion) && strings.Contains(s, "package:") {
			_, _ = m.adb(ctx, instance.Serial, "shell", "cmd", "package", "set-home-activity", "com.neko233.launcher/.LauncherActivity")
			return nil
		}
	}

	tmp, err := os.CreateTemp("", "neko-launcher-*.apk")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(embeddedNekoLauncherAPK); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if out, err := m.adb(ctx, instance.Serial, "install", "-r", "-t", tmpPath); err != nil {
		return fmt.Errorf("安装内置 Neko 桌面失败：%s %w", strings.TrimSpace(string(out)), err)
	}

	script := BuildSlimGuestScript(SlimDebloatPackages())
	if _, err := m.adb(ctx, instance.Serial, "shell", "sh", "-c", script); err != nil {
		return fmt.Errorf("精简定制 Android 系统失败：%w", err)
	}

	// Bring Neko Launcher front if the user was sitting on the disabled stock launcher.
	_, _ = m.adb(ctx, instance.Serial, "shell", "cmd", "activity", "start-activity", "-f", "0x10200000", "com.neko233.launcher/.LauncherActivity")
	return nil
}
