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

func TestResourceSettingsMCP(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	dir := filepath.Join(sdk, "system-images", "android-34", "google_apis_playstore", "x86_64")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"system.img": "test image", "package.xml": "<fake-package/>", "build.prop": "ro.build.version.incremental=12077443\n",
		"source.properties": "Pkg.Revision=1\nAndroidVersion.ApiLevel=34\nSystemImage.Abi=x86_64\nSystemImage.TagId=google_apis_playstore\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager := avd.Manager{SDK: sdk, Home: filepath.Join(root, "avd"), Name: "NekoMCPResources"}
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
	set, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_set_resources", Arguments: map[string]any{"cores": 2, "ram_mib": 1536}})
	if err != nil || set.IsError {
		t.Fatalf("set resources failed: %+v %v", set, err)
	}
	got, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_get_resources", Arguments: map[string]any{}})
	if err != nil || got.IsError {
		t.Fatalf("get resources failed: %+v %v", got, err)
	}
	data, err := json.Marshal(got.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var resources avd.Resources
	if err = json.Unmarshal(data, &resources); err != nil || resources != (avd.Resources{Cores: 2, RAMMiB: 1536}) {
		t.Fatalf("resource result = %s, %v", data, err)
	}
	invalid, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "neko_set_resources", Arguments: map[string]any{"cores": 0, "ram_mib": 1536}})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid resources accepted: %+v %v", invalid, err)
	}
}
