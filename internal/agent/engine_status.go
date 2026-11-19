//go:build windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

// EngineStatus is the Neko equivalent of MuMu's nemu-vboxmanager VboxPlayer
// surface (runtime state, CPU exec cap, memory, NAT, shared folder), exposed
// as one MCP tool. Field names mirror the reverse-engineered VboxPlayer API.
type EngineStatus struct {
	Name               string `json:"name"`
	RuntimeState       string `json:"runtime_state"`
	Serial             string `json:"serial,omitempty"`
	CPUExecCap         int    `json:"cpu_exec_cap"`
	MemoryMiB          int    `json:"memory_mib"`
	MemoryOptimization bool   `json:"memory_optimization"`
	Width              int    `json:"width"`
	Height             int    `json:"height"`
	DPI                int    `json:"dpi"`
	ImageTag           string `json:"image_tag,omitempty"`
	ADBPort            int    `json:"adb_port,omitempty"`
	GRPCPort           int    `json:"grpc_port,omitempty"`
	NATNetwork         bool   `json:"nat_network"`
	SharedFolder       string `json:"shared_folder"`
	HardwareVulkan     bool   `json:"hardware_vulkan"`
	VTEnabled          bool   `json:"vt_enabled"`
}

func (s *Service) engineStatus(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, EngineStatus, error) {
	s.mu.Lock()
	m, err := s.manager()
	s.mu.Unlock()
	if err != nil {
		return nil, EngineStatus{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	instance, instErr := m.Ready(ctx)
	instances, listErr := m.ListInstances(ctx)
	if listErr != nil {
		return nil, EngineStatus{}, listErr
	}
	var summary avd.InstanceSummary
	for _, item := range instances {
		if strings.EqualFold(item.Name, m.Name) {
			summary = item
			break
		}
	}
	status := EngineStatus{
		Name:               m.Name,
		RuntimeState:       "stopped",
		CPUExecCap:         summary.Cores,
		MemoryMiB:          summary.RAMMiB,
		MemoryOptimization: false,
		Width:              summary.Width,
		Height:             summary.Height,
		DPI:                summary.Density,
		ImageTag:           summary.ImageTag,
		NATNetwork:         true,
		SharedFolder:       filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "shared", m.Name),
	}
	if instErr == nil {
		status.RuntimeState = "running"
		status.Serial = instance.Serial
		status.HardwareVulkan = instance.VulkanDevice != ""
		if n, err := strconv.Atoi(strings.TrimPrefix(instance.Serial, "emulator-")); err == nil {
			status.ADBPort = n
		}
	}
	status.VTEnabled = status.HardwareVulkan
	return nil, status, nil
}
