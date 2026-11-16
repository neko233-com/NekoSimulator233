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
	presentCalls := 0
	s := &Service{hostProbe: func(testSurface bool) (vulkan.DeviceInfo, error) {
		called++
		if !testSurface {
			t.Error("surface flag was not forwarded")
		}
		return vulkan.DeviceInfo{Name: "synthetic GPU", Swapchain: true, GraphicsQueueFamily: 3}, nil
	}, presentProbe: func(_ context.Context, pixels bool) (vulkan.DeviceInfo, error) {
		presentCalls++
		if !pixels {
			t.Error("pixel flag was not forwarded")
		}
		return vulkan.DeviceInfo{Name: "synthetic GPU", PresentFence: true, TestSurface: &vulkan.SwapchainInfo{Width: 64, Height: 64, ImageCount: 3, ClearSubmitted: true, PixelsVerified: true}}, nil
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
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_host_vulkan", Arguments: map[string]any{"test_surface": true}})
	if err != nil || result.IsError || called != 1 {
		t.Fatalf("host probe call: %v, calls=%d", err, called)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil || !strings.Contains(string(data), "synthetic GPU") || !strings.Contains(string(data), `"graphics_queue_family":3`) {
		t.Fatalf("unexpected host result %s: %v", data, err)
	}
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_host_vulkan", Arguments: map[string]any{"test_pixels": true}})
	if err != nil || result.IsError || presentCalls != 1 || called != 1 {
		t.Fatalf("present probe call: %v host=%d present=%d", err, called, presentCalls)
	}
	data, err = json.Marshal(result.StructuredContent)
	if err != nil || !strings.Contains(string(data), `"clear_submitted":true`) || !strings.Contains(string(data), `"pixels_verified":true`) {
		t.Fatalf("unexpected present result %s: %v", data, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err := s.hostVulkan(canceled, nil, HostVulkanRequest{}); !errors.Is(err, context.Canceled) || called != 1 {
		t.Fatalf("canceled call invoked probe: %v", err)
	}
}
