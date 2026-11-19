//go:build windows

package avd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// EngineConfig is the Neko equivalent of MuMu's nemu-vboxmanager VboxPlayer
// control surface: guest properties, extra data, NAT, port forwarding and
// memory optimization. It is stored per instance alongside the AVD metadata.
type EngineConfig struct {
	GuestProperties    map[string]string      `json:"guest_properties,omitempty"`
	ExtraData          map[string]string      `json:"extra_data,omitempty"`
	MemoryOptimization bool                   `json:"memory_optimization,omitempty"`
	NATNetwork         bool                   `json:"nat_network"`
	NATIPAddress       string                 `json:"nat_ip_address,omitempty"`
	PortForwards       map[string]PortForward `json:"port_forwards,omitempty"`
}

type PortForward struct {
	Name      string `json:"name"`
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
	Protocol  string `json:"protocol,omitempty"`
}

var engineKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_./:-]{1,128}$`)

func engineConfigPath(m Manager) (string, error) {
	if err := m.validate(); err != nil {
		return "", err
	}
	return filepath.Join(m.Home, m.Name+".avd", "neko-engine.json"), nil
}

// EngineConfig returns the stored config; a missing file means defaults.
func (m Manager) EngineConfig() (EngineConfig, error) {
	path, err := engineConfigPath(m)
	if err != nil {
		return EngineConfig{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return EngineConfig{NATNetwork: true}, nil
	}
	if err != nil {
		return EngineConfig{}, err
	}
	if len(b) > 64<<10 {
		return EngineConfig{}, errors.New("引擎配置文件过大")
	}
	var cfg EngineConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return EngineConfig{}, err
	}
	if err := validateEngineConfig(cfg); err != nil {
		return EngineConfig{}, err
	}
	return cfg, nil
}

func validateEngineConfig(cfg EngineConfig) error {
	for _, set := range []map[string]string{cfg.GuestProperties, cfg.ExtraData} {
		for key, value := range set {
			if !engineKeyPattern.MatchString(key) || len(value) > 4096 {
				return fmt.Errorf("属性键或值无效: %q", key)
			}
		}
	}
	for name, pf := range cfg.PortForwards {
		if !engineKeyPattern.MatchString(name) || pf.HostPort < 1 || pf.HostPort > 65535 || pf.GuestPort < 1 || pf.GuestPort > 65535 {
			if !engineKeyPattern.MatchString(name) {
				return fmt.Errorf("端口转发名称无效: %q", name)
			}
			return fmt.Errorf("端口转发 %q 端口无效", name)
		}
		if pf.Protocol == "" {
			pf.Protocol = "tcp"
		}
		if pf.Protocol != "tcp" && pf.Protocol != "udp" {
			return fmt.Errorf("端口转发协议无效: %q", pf.Protocol)
		}
	}
	return nil
}

// SetEngineConfig atomically replaces the per-instance engine config.
func (m Manager) SetEngineConfig(cfg EngineConfig) error {
	if err := validateEngineConfig(cfg); err != nil {
		return err
	}
	path, err := engineConfigPath(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".engine-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// EngineInstanceIndex mirrors VboxPlayer::GetInstanceIndex. It reads the AVD
// directory directly to avoid nesting the lifecycle lock used by ListInstances.
func (m Manager) EngineInstanceIndex() (int, error) {
	entries, err := os.ReadDir(m.Home)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{strings.ToLower(DefaultName): true}
	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".ini" || ext == ".avd" {
			seen[strings.ToLower(strings.TrimSuffix(entry.Name(), ext))] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if name == strings.ToLower(m.Name) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("实例 %s 不在实例列表中", m.Name)
}
