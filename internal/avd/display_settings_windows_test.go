package avd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisplaySettingsPersistAndSurviveBackup(t *testing.T) {
	m := backupFixture(t)
	dir := filepath.Join(m.Home, m.Name+".avd")
	config := filepath.Join(dir, "config.ini")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	// Duplicate keys must all change because Android uses the last value.
	if err := os.WriteFile(config, append(before, []byte("hw.lcd.width=720\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	want := DisplaySettings{Width: 1920, Height: 1080, Density: 320}
	if err := m.SetDisplaySettings(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetDisplaySettings(context.Background())
	if err != nil || got != want {
		t.Fatalf("display settings %+v %v", got, err)
	}
	updated, err := os.ReadFile(config)
	if err != nil || strings.Count(string(updated), "hw.lcd.width=1920") != 2 || !strings.Contains(string(updated), "hw.gpu.mode=host") {
		t.Fatalf("configuration not preserved: %v", err)
	}
	backup := filepath.Join(t.TempDir(), "display.nekobak")
	if err := m.ExportBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	target := Manager{SDK: m.SDK, Home: filepath.Join(t.TempDir(), "restored"), Name: "DisplayRestore"}
	if err := target.ImportBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	if got, err := target.GetDisplaySettings(context.Background()); err != nil || got != want {
		t.Fatalf("restored display %+v %v", got, err)
	}
}

func TestDisplaySettingsRejectInvalidRunningAndMalformed(t *testing.T) {
	m := backupFixture(t)
	dir := filepath.Join(m.Home, m.Name+".avd")
	config := filepath.Join(dir, "config.ini")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []DisplaySettings{{Width: 319, Height: 720, Density: 240}, {Width: 8192, Height: 8192, Density: 240}, {Width: 720, Height: 1280, Density: 71}} {
		if err := m.SetDisplaySettings(context.Background(), value); err == nil {
			t.Fatalf("accepted %+v", value)
		}
	}
	if err := os.WriteFile(config, append(before, []byte("hw.lcd.width=invalid\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	bad, _ := os.ReadFile(config)
	if err := m.SetDisplaySettings(context.Background(), DisplaySettings{720, 1280, 240}); err == nil {
		t.Fatal("malformed config overwritten")
	}
	if after, _ := os.ReadFile(config); string(after) != string(bad) {
		t.Fatal("malformed config changed")
	}
	if err := os.WriteFile(config, before, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMP", t.TempDir())
	running := filepath.Join(os.TempDir(), "avd", "running")
	if err := os.MkdirAll(running, 0700); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	data := fmt.Sprintf("avd.name=%s\navd.dir=%s\nport.serial=5578\ngrpc.port=8559\ngrpc.token=test\n", m.Name, dir)
	if err := os.WriteFile(filepath.Join(running, fmt.Sprintf("pid_%d.ini", pid)), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDisplaySettings(context.Background(), DisplaySettings{1280, 720, 240}); err == nil || !strings.Contains(err.Error(), "运行") {
		t.Fatalf("running instance was changed: %v", err)
	}
	if after, _ := os.ReadFile(config); string(after) != string(before) {
		t.Fatal("running config modified")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.SetDisplaySettings(ctx, DisplaySettings{720, 1280, 240}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
