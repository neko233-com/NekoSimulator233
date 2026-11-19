package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/desktopctl"
)

func connectTestServer(t *testing.T, s *Service) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := newServer(s).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func fakeDesktop(t *testing.T) *desktopctl.Client {
	t.Helper()
	return desktopctl.NewWithRunner("agent-desktop", func(ctx context.Context, path string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "version" {
			return []byte(`{"ok":true,"data":{"version":"1.3.1"}}`), nil
		}
		if len(args) > 0 && args[0] == "list-windows" {
			return []byte(`{"ok":true,"data":{"windows":[{"id":"w-42","title":"demo - NekoSimulator233","app_name":"NekoSimulator233.exe","pid":7}]}}`), nil
		}
		return nil, nil
	})
}

func TestDesktopToolsAreListed(t *testing.T) {
	cs := connectTestServer(t, &Service{})
	result, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range result.Tools {
		found[tool.Name] = true
	}
	for _, name := range []string{"neko_desktop_status", "neko_desktop_windows"} {
		if !found[name] {
			t.Fatalf("tool not registered: %s", name)
		}
	}
	if found["neko_desktop_game"] {
		t.Fatal("obsolete game tool must not be exposed (agent-desktop has no game command)")
	}
}

func TestDesktopStatusTool(t *testing.T) {
	cs := connectTestServer(t, &Service{desktop: fakeDesktop(t), selected: "demo"})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "neko_desktop_status", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("tool failed: %+v %v", res, err)
	}
	var out DesktopStatus
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || out.Version != "1.3.1" || out.Target != "demo"+desktopctl.DefaultViewerSuffix {
		t.Fatalf("unexpected status: %+v", out)
	}
}

func TestDesktopWindowsTool(t *testing.T) {
	cs := connectTestServer(t, &Service{desktop: fakeDesktop(t)})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "neko_desktop_windows", Arguments: map[string]any{"app": "NekoSimulator233.exe"},
	})
	if err != nil || res.IsError {
		t.Fatalf("tool failed: %+v %v", res, err)
	}
	var out DesktopWindowList
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil || len(out.Windows) != 1 || out.Windows[0].ID != "w-42" {
		t.Fatalf("unexpected windows: %+v %v", out, err)
	}
}
