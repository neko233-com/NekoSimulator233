//go:build windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func engineTestService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "avd")
	if err := os.MkdirAll(filepath.Join(home, "demo.avd"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Service{
		selected: "demo",
		Manager: func() (avd.Manager, error) {
			return avd.Manager{SDK: `D:\Android\Sdk`, Home: home, Name: "demo"}, nil
		},
	}
}

func TestVboxPlayerTools(t *testing.T) {
	cs := connectTestServer(t, engineTestService(t))
	ctx := context.Background()
	calls := []struct {
		name string
		args map[string]any
	}{
		{"neko_engine_guest_property_set", map[string]any{"key": "ro.neko.test", "value": "ok"}},
		{"neko_engine_guest_property_get", map[string]any{"key": "ro.neko.test"}},
		{"neko_engine_extra_data_set", map[string]any{"key": "owner", "value": "neko"}},
		{"neko_engine_set_nat", map[string]any{"enabled": true, "ip_address": "10.0.0.2"}},
		{"neko_engine_set_port_forward", map[string]any{"name": "adb", "host_port": 5555, "guest_port": 5555, "protocol": "tcp"}},
		{"neko_engine_memory_optimization", map[string]any{"enabled": true}},
		{"neko_engine_instance_index", map[string]any{}},
		{"neko_engine_bridged_interfaces", map[string]any{}},
		{"neko_engine_nat", map[string]any{}},
		{"neko_engine_port_forwards", map[string]any{}},
		{"neko_engine_get_config", map[string]any{}},
	}
	for _, call := range calls {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Arguments: call.args})
		if err != nil || res.IsError {
			t.Fatalf("%s failed: %+v %v", call.name, res, err)
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_engine_reset_vm_data", Arguments: map[string]any{"confirm": "NO"}})
	if err != nil || !res.IsError {
		t.Fatalf("reset must require confirmation: %+v %v", res, err)
	}
}
