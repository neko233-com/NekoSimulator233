package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioExecutable(t *testing.T) {
	exe := os.Getenv("NEKO_TEST_MCP_EXE")
	if exe == "" {
		t.Skip("set NEKO_TEST_MCP_EXE to a built MCP executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "neko-stdio-test", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(exe)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 27 {
		t.Fatalf("got %d tools", len(tools.Tools))
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "neko_status", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("status failed: %v", err)
	}
	if os.Getenv("NEKO_TEST_HOST_VULKAN") == "1" {
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "neko_host_vulkan", Arguments: map[string]any{"test_pixels": true}})
		if err != nil || result.IsError {
			t.Fatalf("host Vulkan surface failed: %v", err)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Surface struct {
				Width          int  `json:"width"`
				Height         int  `json:"height"`
				ImageCount     int  `json:"image_count"`
				ClearSubmitted bool `json:"clear_submitted"`
				PixelsVerified bool `json:"pixels_verified"`
			} `json:"test_surface"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil || decoded.Surface.Width == 0 || decoded.Surface.Height == 0 || decoded.Surface.ImageCount == 0 || !decoded.Surface.ClearSubmitted || !decoded.Surface.PixelsVerified {
			t.Fatalf("invalid Vulkan surface response %s: %v", data, err)
		}
	}
}
