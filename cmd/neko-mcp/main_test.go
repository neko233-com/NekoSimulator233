package main

import (
	"context"
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
	if len(tools.Tools) != 15 {
		t.Fatalf("got %d tools", len(tools.Tools))
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "neko_status", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("status failed: %v", err)
	}
}
