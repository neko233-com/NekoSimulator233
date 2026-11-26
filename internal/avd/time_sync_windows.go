//go:build windows

package avd

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var windowsToIANATZ = map[string]string{
	"China Standard Time":          "Asia/Shanghai",
	"Taipei Standard Time":         "Asia/Taipei",
	"Tokyo Standard Time":          "Asia/Tokyo",
	"Korea Standard Time":          "Asia/Seoul",
	"Singapore Standard Time":      "Asia/Singapore",
	"UTC":                          "UTC",
	"Greenwich Standard Time":      "Europe/London",
	"GMT Standard Time":            "Europe/London",
	"W. Europe Standard Time":      "Europe/Berlin",
	"Central Europe Standard Time": "Europe/Budapest",
	"Romance Standard Time":        "Europe/Paris",
	"Pacific Standard Time":        "America/Los_Angeles",
	"Mountain Standard Time":       "America/Denver",
	"Central Standard Time":        "America/Chicago",
	"Eastern Standard Time":        "America/New_York",
	"AUS Eastern Standard Time":    "Australia/Sydney",
}

func windowsTZToIANA(winTZ string, offsetSec int) string {
	if iana, ok := windowsToIANATZ[strings.TrimSpace(winTZ)]; ok {
		return iana
	}
	hours := offsetSec / 3600
	if hours == 0 {
		return "UTC"
	}
	if hours == 8 {
		return "Asia/Shanghai"
	}
	// POSIX Etc/GMT signs are inverted relative to ISO-8601 offsets.
	if hours > 0 {
		return fmt.Sprintf("Etc/GMT-%d", hours)
	}
	return fmt.Sprintf("Etc/GMT+%d", -hours)
}

// HostTimezoneIANA returns the Windows host's current IANA timezone name.
func HostTimezoneIANA() string {
	_, offset := time.Now().Zone()
	cmd := exec.Command("tzutil", "/g")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err == nil {
		return windowsTZToIANA(string(out), offset)
	}
	return windowsTZToIANA("", offset)
}

// SyncHostTimeAndLocale synchronizes the Android guest system clock, timezone,
// and locale with the Windows host PC.
func (m Manager) SyncHostTimeAndLocale(ctx context.Context, instance Instance) error {
	now := time.Now()
	tz := HostTimezoneIANA()
	epochMS := strconv.FormatInt(now.UnixMilli(), 10)

	_, _ = m.adb(ctx, instance.Serial, "shell", "settings", "put", "global", "auto_time", "0")
	_, _ = m.adb(ctx, instance.Serial, "shell", "settings", "put", "global", "auto_time_zone", "0")
	_, _ = m.adb(ctx, instance.Serial, "shell", "service", "call", "alarm", "4", "s16", tz)
	_, _ = m.adb(ctx, instance.Serial, "shell", "setprop", "persist.sys.timezone", tz)
	if tz == "Asia/Shanghai" || tz == "Asia/Taipei" {
		_, _ = m.adb(ctx, instance.Serial, "shell", "setprop", "persist.sys.locale", "zh-CN")
	}
	if _, err := m.adb(ctx, instance.Serial, "shell", "service", "call", "alarm", "3", "i64", epochMS); err != nil {
		dateArg := now.Format("010215042006.05")
		if _, err2 := m.adb(ctx, instance.Serial, "shell", "date", dateArg); err2 != nil {
			return err
		}
	}
	return nil
}
