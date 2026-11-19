//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

// VboxPlayer-equivalent control tools. Each maps to a reverse-engineered
// nemu-vboxmanager VboxPlayer method (see docs/mumu-reverse-engineering.md).

type PropertyRequest struct {
	Key   string `json:"key" jsonschema:"Property key"`
	Value string `json:"value,omitempty" jsonschema:"Value to set; omit when reading"`
}

type PropertyResult struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Found bool   `json:"found"`
}

type NATRequest struct {
	Enabled   *bool  `json:"enabled,omitempty" jsonschema:"Enable NAT networking"`
	IPAddress string `json:"ip_address,omitempty" jsonschema:"NAT gateway address"`
}

type PortForwardRequest struct {
	Name      string `json:"name" jsonschema:"Forward rule name"`
	HostPort  int    `json:"host_port,omitempty" jsonschema:"Host TCP/UDP port"`
	GuestPort int    `json:"guest_port,omitempty" jsonschema:"Guest TCP/UDP port"`
	Protocol  string `json:"protocol,omitempty" jsonschema:"tcp or udp; defaults to tcp"`
}

type MemoryOptimizationRequest struct {
	Enabled bool `json:"enabled"`
}

type ResetRequest struct {
	Confirm string `json:"confirm" jsonschema:"Must be RESET to confirm destructive userdata reset"`
}

type BridgedInterface struct {
	Name string   `json:"name"`
	IPs  []string `json:"ips"`
}

type EngineIndex struct {
	Index int `json:"index"`
}

func (s *Service) engineManager() (avd.Manager, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manager()
}

func (s *Service) engineGetConfig(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, avd.EngineConfig, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, avd.EngineConfig{}, err
	}
	cfg, err := m.EngineConfig()
	return nil, cfg, err
}

func (s *Service) engineSetConfig(ctx context.Context, _ *mcp.CallToolRequest, cfg avd.EngineConfig) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.SetEngineConfig(cfg); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) engineProperty(ctx context.Context, _ *mcp.CallToolRequest, req PropertyRequest, guest bool) (*mcp.CallToolResult, PropertyResult, error) {
	if req.Key == "" {
		return nil, PropertyResult{}, errors.New("属性键不能为空")
	}
	m, err := s.engineManager()
	if err != nil {
		return nil, PropertyResult{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, PropertyResult{}, err
	}
	set := cfg.ExtraData
	if guest {
		set = cfg.GuestProperties
	}
	if set == nil {
		set = map[string]string{}
	}
	value, found := set[req.Key]
	return nil, PropertyResult{Key: req.Key, Value: value, Found: found}, nil
}

func (s *Service) engineSetProperty(ctx context.Context, _ *mcp.CallToolRequest, req PropertyRequest, guest bool) (*mcp.CallToolResult, Result, error) {
	if req.Key == "" {
		return nil, Result{}, errors.New("属性键不能为空")
	}
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, Result{}, err
	}
	if guest {
		if cfg.GuestProperties == nil {
			cfg.GuestProperties = map[string]string{}
		}
		cfg.GuestProperties[req.Key] = req.Value
	} else {
		if cfg.ExtraData == nil {
			cfg.ExtraData = map[string]string{}
		}
		cfg.ExtraData[req.Key] = req.Value
	}
	if err := m.SetEngineConfig(cfg); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) guestPropertyGet(ctx context.Context, req *mcp.CallToolRequest, r PropertyRequest) (*mcp.CallToolResult, PropertyResult, error) {
	return s.engineProperty(ctx, req, r, true)
}

func (s *Service) guestPropertySet(ctx context.Context, req *mcp.CallToolRequest, r PropertyRequest) (*mcp.CallToolResult, Result, error) {
	return s.engineSetProperty(ctx, req, r, true)
}

func (s *Service) extraDataGet(ctx context.Context, req *mcp.CallToolRequest, r PropertyRequest) (*mcp.CallToolResult, PropertyResult, error) {
	return s.engineProperty(ctx, req, r, false)
}

func (s *Service) extraDataSet(ctx context.Context, req *mcp.CallToolRequest, r PropertyRequest) (*mcp.CallToolResult, Result, error) {
	return s.engineSetProperty(ctx, req, r, false)
}

func (s *Service) engineNAT(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, NATRequest, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, NATRequest{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, NATRequest{}, err
	}
	enabled := cfg.NATNetwork
	return nil, NATRequest{Enabled: &enabled, IPAddress: cfg.NATIPAddress}, nil
}

func (s *Service) engineSetNAT(ctx context.Context, _ *mcp.CallToolRequest, req NATRequest) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, Result{}, err
	}
	if req.Enabled != nil {
		cfg.NATNetwork = *req.Enabled
	}
	if req.IPAddress != "" {
		if net.ParseIP(req.IPAddress) == nil {
			return nil, Result{}, errors.New("NAT IP 地址无效")
		}
		cfg.NATIPAddress = req.IPAddress
	}
	if err := m.SetEngineConfig(cfg); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) enginePortForwards(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
	Forwards map[string]avd.PortForward `json:"forwards"`
}, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, struct {
			Forwards map[string]avd.PortForward `json:"forwards"`
		}{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, struct {
			Forwards map[string]avd.PortForward `json:"forwards"`
		}{}, err
	}
	return nil, struct {
		Forwards map[string]avd.PortForward `json:"forwards"`
	}{Forwards: cfg.PortForwards}, nil
}

func (s *Service) engineSetPortForward(ctx context.Context, _ *mcp.CallToolRequest, req PortForwardRequest) (*mcp.CallToolResult, Result, error) {
	if req.Name == "" || req.HostPort < 1 || req.HostPort > 65535 || req.GuestPort < 1 || req.GuestPort > 65535 {
		return nil, Result{}, errors.New("端口转发名称或端口无效")
	}
	protocol := req.Protocol
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return nil, Result{}, errors.New("协议必须是 tcp 或 udp")
	}
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, Result{}, err
	}
	if cfg.PortForwards == nil {
		cfg.PortForwards = map[string]avd.PortForward{}
	}
	cfg.PortForwards[req.Name] = avd.PortForward{Name: req.Name, HostPort: req.HostPort, GuestPort: req.GuestPort, Protocol: protocol}
	if err := m.SetEngineConfig(cfg); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) engineRemovePortForward(ctx context.Context, _ *mcp.CallToolRequest, req PortForwardRequest) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, Result{}, err
	}
	delete(cfg.PortForwards, req.Name)
	if err := m.SetEngineConfig(cfg); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) engineSetMemoryOptimization(ctx context.Context, _ *mcp.CallToolRequest, req MemoryOptimizationRequest) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	cfg, err := m.EngineConfig()
	if err != nil {
		return nil, Result{}, err
	}
	cfg.MemoryOptimization = req.Enabled
	if err := m.SetEngineConfig(cfg); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) engineBridgedInterfaces(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
	Interfaces []BridgedInterface `json:"interfaces"`
}, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, struct {
			Interfaces []BridgedInterface `json:"interfaces"`
		}{}, err
	}
	result := struct {
		Interfaces []BridgedInterface `json:"interfaces"`
	}{}
	for _, iface := range ifaces {
		entry := BridgedInterface{Name: iface.Name}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			entry.IPs = append(entry.IPs, addr.String())
		}
		result.Interfaces = append(result.Interfaces, entry)
	}
	return nil, result, nil
}

func (s *Service) engineResetVMData(ctx context.Context, _ *mcp.CallToolRequest, req ResetRequest) (*mcp.CallToolResult, Result, error) {
	if req.Confirm != "RESET" {
		return nil, Result{}, errors.New("需要 confirm=RESET 才能重置实例数据")
	}
	s.mu.Lock()
	m, err := s.manager()
	s.mu.Unlock()
	if err != nil {
		return nil, Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := m.Ready(ctx); err == nil {
		return nil, Result{}, errors.New("实例正在运行，请先关闭")
	}
	dir := filepath.Join(m.Home, m.Name+".avd")
	for _, name := range []string{"userdata-qemu.img", "userdata-qemu.img.qcow2", "userdata.img"} {
		path := filepath.Join(dir, name)
		if st, err := os.Lstat(path); err == nil && !st.IsDir() {
			if err := os.Remove(path); err != nil {
				return nil, Result{}, fmt.Errorf("删除 %s 失败：%w", name, err)
			}
		}
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) engineInstanceIndex(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, EngineIndex, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, EngineIndex{}, err
	}
	index, err := m.EngineInstanceIndex()
	if err != nil {
		return nil, EngineIndex{}, err
	}
	return nil, EngineIndex{Index: index}, nil
}

func (s *Service) engineResume(ctx context.Context, req *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, State, error) {
	return s.start(ctx, req, Empty{})
}
