//go:build windows

package avd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEngineConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "avd")
	dir := filepath.Join(home, "demo.avd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := Manager{SDK: `D:\Android\Sdk`, Home: home, Name: "demo"}
	cfg, err := m.EngineConfig()
	if err != nil || !cfg.NATNetwork {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg.GuestProperties = map[string]string{"ro.neko.test": "ok"}
	cfg.ExtraData = map[string]string{"owner": "neko"}
	cfg.MemoryOptimization = true
	cfg.NATIPAddress = "10.0.0.2"
	cfg.PortForwards = map[string]PortForward{"adb": {Name: "adb", HostPort: 5555, GuestPort: 5555, Protocol: "tcp"}}
	if err := m.SetEngineConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := m.EngineConfig()
	if err != nil || got.GuestProperties["ro.neko.test"] != "ok" || !got.MemoryOptimization || got.PortForwards["adb"].HostPort != 5555 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if err := m.SetEngineConfig(EngineConfig{PortForwards: map[string]PortForward{"bad": {HostPort: 0, GuestPort: 1}}}); err == nil {
		t.Fatal("invalid port must be rejected")
	}
}

func TestEngineInstanceIndex(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "avd")
	for _, name := range []string{"alpha.avd", "beta.avd", "gamma.avd"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	m := Manager{SDK: `D:\Android\Sdk`, Home: home, Name: "gamma"}
	index, err := m.EngineInstanceIndex()
	if err != nil || index != 2 {
		t.Fatalf("index=%d err=%v", index, err)
	}
}
