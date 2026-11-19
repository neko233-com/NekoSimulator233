//go:build windows

package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func phoneTestService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	return &Service{
		selected: "demo",
		Manager: func() (avd.Manager, error) {
			return avd.Manager{SDK: `D:\Android\Sdk`, Home: filepath.Join(root, "avd"), Name: "demo"}, nil
		},
	}
}

func TestPhoneIdentityTools(t *testing.T) {
	s := phoneTestService(t)
	cs := connectTestServer(t, s)

	set, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "neko_set_phone_identity",
		Arguments: map[string]any{"brand": "OPPO", "manufacturer": "OPPO", "model": "A93s", "imei": "863323045635028"},
	})
	if err != nil || set.IsError {
		t.Fatalf("set failed: %+v %v", set, err)
	}
	get, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "neko_get_phone_identity", Arguments: map[string]any{}})
	if err != nil || get.IsError {
		t.Fatalf("get failed: %+v %v", get, err)
	}
	b, _ := json.Marshal(get.StructuredContent)
	var got avd.PhoneIdentity
	if err := json.Unmarshal(b, &got); err != nil || got.Model != "A93s" || got.IMEI != "863323045635028" {
		t.Fatalf("unexpected identity: %+v %v", got, err)
	}
}
