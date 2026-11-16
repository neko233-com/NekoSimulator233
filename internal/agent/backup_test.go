package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func TestMCPBackupRestoreToNewInstance(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	image := filepath.Join(sdk, "system-images", "android-34", "google_apis", "x86_64", "system.img")
	if err := os.MkdirAll(filepath.Dir(image), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("test system"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(image), "build.prop"), []byte("ro.build.version.incremental=12077443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := avd.Manager{SDK: sdk, Home: filepath.Join(root, "avd"), Name: avd.DefaultName}
	if err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.Home, m.Name+".avd")
	if err := os.WriteFile(filepath.Join(dir, "version_num.cache"), []byte("12077443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "userdata-qemu.img"), []byte("synthetic guest data"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	server := New(func() (avd.Manager, error) { return m, nil })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	st, ct := mcp.NewInMemoryTransports()
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
	call := func(name string, args any, wantError bool) {
		t.Helper()
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError != wantError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
	}
	path := filepath.Join(root, "data.nekobak")
	call("neko_export_backup", map[string]any{"path": "relative.nekobak"}, true)
	call("neko_export_backup", map[string]any{"path": path}, false)
	call("neko_export_backup", map[string]any{"path": path}, true)
	call("neko_import_backup", map[string]any{"path": path, "name": "Restored"}, false)
	call("neko_import_backup", map[string]any{"path": path, "name": "Restored"}, true)
	call("neko_import_backup", map[string]any{"path": path, "name": "../bad"}, true)
	got, err := os.ReadFile(filepath.Join(m.Home, "Restored.avd", "userdata-qemu.img"))
	if err != nil || string(got) != "synthetic guest data" {
		t.Fatalf("restored data = %q, %v", got, err)
	}
}
