//go:build windows

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func TestEngineStatusTool(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "avd")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		selected: "demo",
		Manager: func() (avd.Manager, error) {
			return avd.Manager{SDK: `D:\Android\Sdk`, Home: home, Name: "demo"}, nil
		},
	}
	cs := connectTestServer(t, svc)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "neko_engine_status", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("engine status failed: %+v %v", res, err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var got EngineStatus
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo" || got.RuntimeState != "stopped" || !got.NATNetwork {
		t.Fatalf("unexpected engine status: %+v", got)
	}
	if got.SharedFolder == "" {
		t.Fatal("shared folder path missing")
	}
}
