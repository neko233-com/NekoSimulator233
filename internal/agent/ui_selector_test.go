package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

const selectorTree = `<?xml version="1.0"?><hierarchy rotation="0"><node text="开始 &amp; 继续" resource-id="game:id/start" content-desc="play" class="android.widget.TextView" package="game.app" enabled="true" clickable="false" bounds="[10,20][110,80]"/><node text="开始 &amp; 继续" resource-id="game:id/disabled" enabled="false" bounds="[120,20][220,80]"/></hierarchy>`

func TestSelectUIAndTapPoint(t *testing.T) {
	nodes, err := selectUI(selectorTree, UISelector{Text: "开始 & 继续", ResourceID: "game:id/start", Package: "game.app"})
	if err != nil || len(nodes) != 1 || nodes[0].Description != "play" || nodes[0].Clickable {
		t.Fatalf("selection: %+v, %v", nodes, err)
	}
	x, y, err := uiTapPoint(nodes, 80, 100)
	if err != nil || x != 44 || y != 49 {
		t.Fatalf("clipped point = %d,%d: %v", x, y, err)
	}
	if _, _, err := uiTapPoint(nodes, 10, 100); err == nil {
		t.Fatal("accepted a node outside the screen")
	}
	multiple, err := selectUI(selectorTree, UISelector{Text: "开始 & 继续"})
	if err != nil || len(multiple) != 2 {
		t.Fatalf("ambiguous selection: %+v %v", multiple, err)
	}
	if _, _, err := uiTapPoint(multiple, 300, 200); err == nil {
		t.Fatal("accepted ambiguous selection")
	}
	if _, _, err := uiTapPoint(multiple[1:], 300, 200); err == nil {
		t.Fatal("accepted disabled node")
	}
	missing, err := selectUI(selectorTree, UISelector{Description: "PLAY"})
	if err != nil || missing == nil || len(missing) != 0 {
		t.Fatalf("exact match failure: %+v %v", missing, err)
	}
}

func TestSelectUIRejectsMalformedAndUnboundedInput(t *testing.T) {
	selector := UISelector{Text: "A"}
	for _, tree := range []string{
		`<hierarchy><node text="A" enabled="true" bounds="[1,2][3,4]"/>`,
		`<other><node text="A" bounds="[1,2][3,4]"/></other>`,
		`<hierarchy/><hierarchy/>`,
		`<hierarchy><node text="A" text="B" bounds="[1,2][3,4]"/></hierarchy>`,
		`<hierarchy><node text="A" bounds="[1,2][0,4]"/></hierarchy>`,
		`<hierarchy><node text="A" bounds="[1,2][999999999999,4]"/></hierarchy>`,
		strings.Repeat("x", (4<<20)+1),
	} {
		if _, err := selectUI(tree, selector); err == nil {
			t.Fatal("accepted invalid hierarchy")
		}
	}
	for _, invalid := range []UISelector{{}, {Class: "android.view.View"}, {Text: strings.Repeat("x", 1025)}} {
		if _, err := selectUI(selectorTree, invalid); err == nil {
			t.Fatal("accepted invalid selector")
		}
	}
}

func TestUISelectorMCPRegistrationAndValidation(t *testing.T) {
	ctx := context.Background()
	server := New(func() (avd.Manager, error) {
		t.Fatal("invalid selector accessed an emulator")
		return avd.Manager{}, nil
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"neko_find_ui": false, "neko_tap_ui": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing %s", name)
		}
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil || !result.IsError {
			t.Fatalf("%s accepted empty selector: %+v %v", name, result, err)
		}
	}
}
