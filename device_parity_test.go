package main

import (
	"path/filepath"
	"testing"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func TestAndroidLabel(t *testing.T) {
	cases := map[int]string{0: "Android 设备", 30: "Android 11", 34: "Android 14", 35: "Android 15", 36: "Android API 36"}
	for api, want := range cases {
		if got := androidLabel(api); got != want {
			t.Fatalf("api %d: got %q want %q", api, got, want)
		}
	}
}

func TestVisibleDevicesFilterAndSort(t *testing.T) {
	items := []avd.InstanceSummary{
		{Name: "zeta", API: 34, State: "stopped"},
		{Name: "alpha", API: 35, State: "running"},
		{Name: "beta", API: 30, State: "stopped"},
	}
	got := visibleDevices(items, "", deviceSortName)
	if len(got) != 3 || got[0].Name != "alpha" || got[2].Name != "zeta" {
		t.Fatalf("name sort: %+v", got)
	}
	got = visibleDevices(items, "alpha", deviceSortName)
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("search filter: %+v", got)
	}
	got = visibleDevices(items, "Android 11", deviceSortName)
	if len(got) != 1 || got[0].Name != "beta" {
		t.Fatalf("android label filter: %+v", got)
	}
	got = visibleDevices(items, "", deviceSortSystem)
	if got[0].API != 35 {
		t.Fatalf("system sort: %+v", got)
	}
	if len(visibleDevices(items, "missing", deviceSortName)) != 0 {
		t.Fatal("unmatched search should return empty")
	}
}

func TestSharedFolderPathIsScoped(t *testing.T) {
	p, err := sharedFolder("demo")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "demo" || filepath.Base(filepath.Dir(p)) != "shared" {
		t.Fatalf("unexpected shared folder: %s", p)
	}
	if _, err := sharedFolder("../escape"); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}
