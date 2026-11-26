//go:build windows

package avd

import "testing"

func TestWindowsTZToIANA(t *testing.T) {
	if got := windowsTZToIANA("China Standard Time\r\n", 8*3600); got != "Asia/Shanghai" {
		t.Fatalf("unexpected China TZ: %s", got)
	}
	if got := windowsTZToIANA("Unknown", 9*3600); got != "Etc/GMT-9" {
		t.Fatalf("unexpected fallback TZ: %s", got)
	}
	if got := HostTimezoneIANA(); got == "" {
		t.Fatal("HostTimezoneIANA should not be empty")
	}
}
