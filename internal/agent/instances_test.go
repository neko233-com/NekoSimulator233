package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func TestMCPSelectsAndIsolatesIndependentInstances(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	image := filepath.Join(sdk, "system-images", "android-34", "google_apis", "x86_64", "system.img")
	if err := os.MkdirAll(filepath.Dir(image), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyImage := filepath.Join(sdk, "system-images", "android-30", "google_apis", "x86_64", "system.img")
	if err := os.MkdirAll(filepath.Dir(legacyImage), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyImage, []byte("test legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := avd.Manager{SDK: sdk, Home: filepath.Join(root, "avd"), Name: avd.DefaultName}
	server := New(func() (avd.Manager, error) { return manager, nil })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args any, expectError bool) *mcp.CallToolResult {
		t.Helper()
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError != expectError {
			t.Fatalf("%s result %+v: %v", name, result, err)
		}
		return result
	}
	call("neko_create_instance", map[string]any{"name": "Alpha"}, false)
	call("neko_create_instance", map[string]any{"name": "Legacy", "api": 30}, false)
	config, err := os.ReadFile(filepath.Join(manager.Home, "Legacy.avd", "config.ini"))
	if err != nil || !strings.Contains(string(config), "system-images\\android-30\\") {
		t.Fatalf("MCP did not select Android 11: %s %v", config, err)
	}
	call("neko_create_instance", map[string]any{"name": "BadAPI", "api": 29}, true)
	call("neko_install_play_image", map[string]any{"api": 29}, true)
	call("neko_create_instance", map[string]any{"name": "alpha"}, true)
	call("neko_select_instance", map[string]any{"name": "Alpha"}, false)
	setup := call("neko_taptap_setup_status", map[string]any{}, false)
	setupJSON, _ := json.Marshal(setup.StructuredContent)
	var setupStatus TapTapSetupStatus
	if err := json.Unmarshal(setupJSON, &setupStatus); err != nil || setupStatus.Status != "pending" {
		t.Fatalf("new instance setup status: %s %v", setupJSON, err)
	}
	call("neko_set_resources", map[string]any{"cores": 2, "ram_mib": 1536}, false)
	call("neko_select_instance", map[string]any{"name": "missing"}, true)
	list := call("neko_list_instances", map[string]any{}, false)
	b, _ := json.Marshal(list.StructuredContent)
	var instances InstanceList
	if err = json.Unmarshal(b, &instances); err != nil || instances.Selected != "Alpha" {
		t.Fatalf("selection changed after rejected request: %s %v", b, err)
	}
	call("neko_select_instance", map[string]any{"name": avd.DefaultName}, false)
	getResources := func() avd.Resources {
		t.Helper()
		result := call("neko_get_resources", map[string]any{}, false)
		b, _ := json.Marshal(result.StructuredContent)
		var values avd.Resources
		if err := json.Unmarshal(b, &values); err != nil {
			t.Fatal(err)
		}
		return values
	}
	if got := getResources(); got != (avd.Resources{Cores: 4, RAMMiB: 2048}) {
		t.Fatalf("default inherited another instance's settings: %+v", got)
	}
	call("neko_select_instance", map[string]any{"name": "alpha"}, false)
	if got := getResources(); got != (avd.Resources{Cores: 2, RAMMiB: 1536}) {
		t.Fatalf("selected instance lost its settings: %+v", got)
	}
}
