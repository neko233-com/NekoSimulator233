//go:build windows && amd64

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

func TestHostVulkanMCP(t *testing.T) {
	called := 0
	s := &Service{hostProbe: func() (vulkan.DeviceInfo, error) {
		called++
		return vulkan.DeviceInfo{Name: "synthetic GPU", Swapchain: true, GraphicsQueueFamily: 3}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := newServer(s).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_host_vulkan", Arguments: map[string]any{}})
	if err != nil || result.IsError || called != 1 {
		t.Fatalf("host probe call: %v, calls=%d", err, called)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil || !strings.Contains(string(data), "synthetic GPU") || !strings.Contains(string(data), `"graphics_queue_family":3`) {
		t.Fatalf("unexpected host result %s: %v", data, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err := s.hostVulkan(canceled, nil, Empty{}); !errors.Is(err, context.Canceled) || called != 1 {
		t.Fatalf("canceled call invoked probe: %v", err)
	}
}
