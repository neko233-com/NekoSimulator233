package desktopctl

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type proxyEmpty struct{}

// TestProxyReDeclaresAndDelegates verifies the rea-mcp pattern: tools from an
// upstream MCP server are re-declared with a prefix and calls are delegated.
// TestLiveProxyAgainstDesktop runs only when a real desktop CLI is present.
// It proves the black-box stdio handshake works with the actual binary.
func TestLiveProxyAgainstDesktop(t *testing.T) {
	ctx := context.Background()
	binary, err := (&Client{}).Resolve()
	if err != nil {
		t.Skip("desktop not installed: " + err.Error())
	}
	proxyServer := mcp.NewServer(&mcp.Implementation{Name: "neko-live-proxy-test", Version: "1"}, nil)
	count, err := RegisterProxy(context.Background(), proxyServer, binary, 8*time.Second)
	if err != nil {
		t.Fatalf("live proxy registration failed: %v", err)
	}
	if count < 10 {
		t.Fatalf("expected desktop to advertise many tools, got %d", count)
	}
	t.Logf("live agent-desktop proxy registered %d tools", count)

	// A real delegated call: catches context/process lifetime bugs that a pure
	// list-tools test would miss.
	ps, pt := mcp.NewInMemoryTransports()
	proxySession, err := proxyServer.Connect(ctx, ps, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxySession.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "live-client", Version: "1"}, nil).Connect(ctx, pt, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: ProxyPrefix + "desktop_status", Arguments: map[string]any{}})
	if err == nil && res != nil && res.IsError {
		if sc, ok := res.StructuredContent.(map[string]any); ok {
			if errObj, ok := sc["error"].(map[string]any); ok && errObj["code"] == "PERM_DENIED" {
				t.Skip("agent-desktop host state dir DACL rejected by CLI: " + ProxyPrefix + "desktop_status")
			}
		}
	}
	if err != nil || res == nil || res.IsError {
		t.Fatalf("live delegated call failed: res=%+v err=%v", res, err)
	}
}

func TestProxyReDeclaresAndDelegates(t *testing.T) {
	ctx := context.Background()

	// Fake upstream: plays the role of `agent-desktop mcp`.
	upstream := mcp.NewServer(&mcp.Implementation{Name: "fake-desktop", Version: "1"}, nil)
	mcp.AddTool(upstream,
		&mcp.Tool{Name: "status", Description: "fake status tool"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ proxyEmpty) (*mcp.CallToolResult, string, error) {
			return nil, "upstream-ok", nil
		},
	)
	us, ut := mcp.NewInMemoryTransports()
	serverSession, err := upstream.Connect(ctx, us, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	upstreamClient, err := mcp.NewClient(&mcp.Implementation{Name: "proxy", Version: "1"}, nil).Connect(ctx, ut, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamClient.Close()

	// Our proxy server re-declares the upstream tools.
	proxyServer := mcp.NewServer(&mcp.Implementation{Name: "neko-proxy", Version: "1"}, nil)
	count, err := RegisterProxySession(ctx, proxyServer, upstreamClient)
	if err != nil || count != 1 {
		t.Fatalf("RegisterProxySession: count=%d err=%v", count, err)
	}

	ps, pt := mcp.NewInMemoryTransports()
	proxySession, err := proxyServer.Connect(ctx, ps, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxySession.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "1"}, nil).Connect(ctx, pt, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == ProxyPrefix+"status" {
			found = true
			if !strings.Contains(tool.Description, "fake status tool") {
				t.Fatalf("description not proxied: %q", tool.Description)
			}
		}
	}
	if !found {
		t.Fatalf("proxied tool not listed: %+v", tools.Tools)
	}

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: ProxyPrefix + "status", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("proxied call failed: %+v %v", res, err)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "upstream-ok") {
		t.Fatalf("delegation produced unexpected content: %q", text)
	}
}
