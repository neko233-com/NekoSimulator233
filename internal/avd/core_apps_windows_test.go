package avd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCoreAppStatusesIncludeSystemPackagesAndDisabled(t *testing.T) {
	adb := func(_ context.Context, args ...string) ([]byte, error) {
		switch args[len(args)-1] {
		case "-e":
			return []byte("package:android\r\npackage:com.android.vending\r\npackage:com.google.android.gms\r\npackage:com.taptap\r\n"), nil
		case "-d":
			return []byte("package:com.android.chrome\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	apps, err := queryCoreApps(context.Background(), adb)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, app := range apps {
		got[app.ID] = app.State
	}
	want := map[string]string{"chrome": "disabled", "play": "enabled", "play_services": "enabled", "google_services_framework": "missing", "taptap": "enabled", "wechat": "missing", "douyin": "missing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	_, err = queryCoreApps(context.Background(), func(context.Context, ...string) ([]byte, error) {
		return []byte("Error: package manager unavailable"), nil
	})
	if err == nil {
		t.Fatal("error response treated as missing packages")
	}
	_, err = queryCoreApps(context.Background(), func(context.Context, ...string) ([]byte, error) { return []byte("package:com.android.chrome"), nil })
	if err == nil {
		t.Fatal("inconsistent status accepted")
	}
}

func TestOpenCoreAppAndChromePlayFallback(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, disabled, id, response, action string
		wantErr                                       bool
	}{
		{"installed", "package:com.android.chrome\n", "", "chrome", "Events injected: 1", "opened", false},
		{"play listing", "package:com.android.vending\n", "", "chrome", "Status: ok", "play_listing", false},
		{"disabled", "package:com.android.vending\n", "package:com.android.chrome\n", "chrome", "", "", true},
		{"missing play", "", "", "chrome", "", "", true},
		{"failed intent", "package:com.android.vending\n", "", "chrome", "Error: no activity", "", true},
		{"invalid id", "", "", "anything", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var command []string
			adb := func(_ context.Context, args ...string) ([]byte, error) {
				if len(args) == 5 && args[1] == "pm" {
					if args[4] == "-e" {
						return []byte(tc.enabled), nil
					}
					return []byte(tc.disabled), nil
				}
				command = append([]string(nil), args...)
				return []byte(tc.response), nil
			}
			result, err := openCoreApp(context.Background(), tc.id, adb)
			if (err != nil) != tc.wantErr || result.Action != tc.action {
				t.Fatalf("result %+v, err %v", result, err)
			}
			if tc.action == "play_listing" && (!strings.Contains(strings.Join(command, " "), "https://play.google.com/store/apps/details?id=com.android.chrome") || command[len(command)-1] != "com.android.vending") {
				t.Fatalf("unexpected listing intent: %q", command)
			}
			if tc.action == "opened" && !reflect.DeepEqual(command, []string{"shell", "monkey", "-p", "com.android.chrome", "-c", "android.intent.category.LAUNCHER", "1"}) {
				t.Fatalf("unexpected launch: %q", command)
			}
		})
	}
}
