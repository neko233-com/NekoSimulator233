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

func setupKeymapEnv(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	data := filepath.Join(root, "NekoSimulator233-data")
	if err := os.MkdirAll(filepath.Join(data, "avd", avd.DefaultName+".avd"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || res.IsError {
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				t.Logf("%s error: %s", name, tc.Text)
			}
		}
		t.Fatalf("%s failed: %+v %v", name, res, err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestKeymapToolsRoundTrip(t *testing.T) {
	setupKeymapEnv(t)
	cs := connectTestServer(t, &Service{selected: avd.DefaultName})

	profiles := callTool(t, cs, "neko_keymap_profiles", map[string]any{})
	list, _ := profiles["profiles"].([]any)
	if len(list) == 0 || list[0] != "默认" {
		t.Fatalf("unexpected profiles: %v", list)
	}

	setArgs := map[string]any{
		"keys": map[string]any{
			"60": map[string]any{"x": 0.12, "y": 0.34},
			"61": map[string]any{"x": 0.5, "y": 0.6},
		},
		"joystick": map[string]any{
			"center": map[string]any{"x": 0.2, "y": 0.8},
			"radius": 0.1,
		},
	}
	callTool(t, cs, "neko_keymap_set", setArgs)

	got := callTool(t, cs, "neko_keymap_get", map[string]any{})
	keys, _ := got["keys"].(map[string]any)
	w, ok := keys["60"].(map[string]any)
	if !ok || w["x"] != 0.12 || w["y"] != 0.34 {
		t.Fatalf("keymap readback mismatch: %v", keys)
	}
	if got["joystick"] == nil {
		t.Fatalf("joystick missing: %v", got)
	}
}

func TestKeymapShooterTemplate(t *testing.T) {
	setupKeymapEnv(t)
	cs := connectTestServer(t, &Service{selected: avd.DefaultName})

	callTool(t, cs, "neko_keymap_shooter", map[string]any{
		"joystick": map[string]any{"x": 0.18, "y": 0.78},
		"radius":   0.12,
		"fire":     map[string]any{"x": 0.85, "y": 0.55},
		"scope":    map[string]any{"x": 0.72, "y": 0.4},
	})
	got := callTool(t, cs, "neko_keymap_get", map[string]any{})
	keys, _ := got["keys"].(map[string]any)
	if keys["40"] == nil || keys["44"] == nil {
		t.Fatalf("fire/scope bindings missing: %v", keys)
	}
	for _, wasd := range []string{"26", "4", "22", "7"} {
		if keys[wasd] != nil {
			t.Fatalf("WASD occupied by a single binding: %v", keys)
		}
	}
}

func TestKeymapRejectsInvalidInput(t *testing.T) {
	setupKeymapEnv(t)
	cs := connectTestServer(t, &Service{selected: avd.DefaultName})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "neko_keymap_set",
		Arguments: map[string]any{
			"keys": map[string]any{"999": map[string]any{"x": 0.1, "y": 0.2}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for invalid USB usage")
	}
}
