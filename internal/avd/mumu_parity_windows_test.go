//go:build windows

package avd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstanceSettingsPersistenceAndValidation(t *testing.T) {
	home := t.TempDir()
	m := Manager{SDK: "offline", Home: home, Name: "TestDev"}
	got, err := m.InstanceSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.FPSLimit != 60 || got.RendererMode != "vk" || !got.RightClickBack {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	got.PerformanceMode = "high"
	got.ResolutionMode = "tablet"
	got.FPSLimit = 120
	got.VSync = true
	got.ShowFPS = true
	got.Brightness = 65
	got.MACAddress = RandomMACAddress()
	got.AndroidID = RandomAndroidID()
	if err := m.SetInstanceSettings(got); err != nil {
		t.Fatal(err)
	}
	reloaded, err := m.InstanceSettings()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded != got {
		t.Fatalf("mismatch:\ngot  %+v\nwant %+v", reloaded, got)
	}
	bad := got
	bad.FPSLimit = 77
	if err := m.SetInstanceSettings(bad); err == nil {
		t.Fatal("expected invalid FPS error")
	}
	bad2 := got
	bad2.MACAddress = "not-a-mac"
	if err := m.SetInstanceSettings(bad2); err == nil {
		t.Fatal("expected invalid MAC error")
	}
}

func TestPhonePresetsAndRandomizer(t *testing.T) {
	presets := PhonePresets()
	if len(presets) < 5 {
		t.Fatalf("expected presets, got %d", len(presets))
	}
	id := RandomPhoneIdentity(&presets[0])
	if id.Brand != presets[0].Brand || len(id.IMEI) != 15 || id.VDID == "" {
		t.Fatalf("unexpected random identity: %+v", id)
	}
	if err := validatePhoneIdentity(id); err != nil {
		t.Fatalf("generated identity failed validation: %v", err)
	}
}

func TestCloneRenameDeleteAndDiskUsage(t *testing.T) {
	home := t.TempDir()
	avdDir := filepath.Join(home, "Alpha.avd")
	if err := os.MkdirAll(avdDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "AvdId=Alpha\navd.ini.displayname=Alpha\nimage.sysdir.1=system-images\\android-35\\google_apis\\x86_64\\\nhw.cpu.ncore=4\nhw.ramSize=4096\nhw.lcd.width=1080\nhw.lcd.height=1920\nhw.lcd.density=480\n"
	if err := os.WriteFile(filepath.Join(avdDir, "config.ini"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(avdDir, "userdata.img"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	ini := "avd.ini.encoding=UTF-8\npath=" + avdDir + "\ntarget=android-35\n"
	if err := os.WriteFile(filepath.Join(home, "Alpha.ini"), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}

	m := Manager{SDK: "offline", Home: home, Name: "Alpha"}
	ctx := context.Background()

	usage, err := m.DiskUsage()
	if err != nil || usage.Bytes < 4096 {
		t.Fatalf("DiskUsage failed: %+v %v", usage, err)
	}

	if err := m.CloneInstance(ctx, "Alpha", "Beta"); err != nil {
		t.Fatalf("CloneInstance failed: %v", err)
	}
	betaCfg, err := os.ReadFile(filepath.Join(home, "Beta.avd", "config.ini"))
	if err != nil || !strings.Contains(string(betaCfg), "AvdId=Beta") {
		t.Fatalf("cloned config.ini not rewritten: %s %v", string(betaCfg), err)
	}

	if err := m.RenameInstance(ctx, "Beta", "Gamma"); err != nil {
		t.Fatalf("RenameInstance failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Beta.avd")); !os.IsNotExist(err) {
		t.Fatal("old Beta.avd still exists after rename")
	}
	gammaCfg, err := os.ReadFile(filepath.Join(home, "Gamma.avd", "config.ini"))
	if err != nil || !strings.Contains(string(gammaCfg), "AvdId=Gamma") {
		t.Fatalf("renamed config.ini not updated: %s %v", string(gammaCfg), err)
	}

	if err := m.DeleteInstance(ctx, "Gamma"); err != nil {
		t.Fatalf("DeleteInstance failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Gamma.avd")); !os.IsNotExist(err) {
		t.Fatal("Gamma.avd still exists after delete")
	}
}

func TestParseRunningAppsFiltersLauncherAndSystem(t *testing.T) {
	winDump := `  mCurrentFocus=Window{123 u0 com.example.vpn233/com.example.vpn233.MainActivity}`
	recentsDump := `
  * Recent #0: Task{id=1 A=10123:com.neko233.launcher}
  * Recent #1: Task{id=2 A=10124:com.example.vpn233 realActivity=com.example.vpn233/.Main}
  * Recent #2: Task{id=3 A=10125:com.tencent.tmgp.game realActivity=com.tencent.tmgp.game/.GameActivity}
  * Recent #3: Task{id=4 A=1000:com.android.systemui}
`
	apps := ParseRunningApps(winDump, recentsDump)
	if len(apps) != 2 {
		t.Fatalf("expected 2 non-launcher apps, got %+v", apps)
	}
	if apps[0].Package != "com.example.vpn233" || !apps[0].Foreground {
		t.Fatalf("unexpected first app: %+v", apps[0])
	}
	if apps[1].Package != "com.tencent.tmgp.game" || apps[1].Foreground {
		t.Fatalf("unexpected second app: %+v", apps[1])
	}
}
