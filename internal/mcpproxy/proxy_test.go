package mcpproxy

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type emptyArgs struct{}

func TestRegisterSessionProxiesArbitraryUpstream(t *testing.T) {
	ctx := context.Background()
	upstream := mcp.NewServer(&mcp.Implementation{Name: "fake-rea", Version: "1"}, nil)
	mcp.AddTool(upstream, &mcp.Tool{Name: "inspect_pe", Description: "inspect PE binary"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, string, error) {
			return nil, "pe-ok", nil
		})
	us, ut := mcp.NewInMemoryTransports()
	upSess, err := upstream.Connect(ctx, us, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer upSess.Close()
	upClient, err := mcp.NewClient(&mcp.Implementation{Name: "test-proxy", Version: "1"}, nil).Connect(ctx, ut, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer upClient.Close()

	proxy := mcp.NewServer(&mcp.Implementation{Name: "neko-mcp", Version: "1"}, nil)
	count, err := RegisterSession(ctx, proxy, upClient, "rea-mcp", "rea_")
	if err != nil || count != 1 {
		t.Fatalf("RegisterSession: count=%d err=%v", count, err)
	}

	ps, pt := mcp.NewInMemoryTransports()
	pSess, err := proxy.Connect(ctx, ps, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pSess.Close()
	cli, err := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "1"}, nil).Connect(ctx, pt, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	res, err := cli.CallTool(ctx, &mcp.CallToolParams{Name: "rea_inspect_pe", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("proxied call failed: %+v %v", res, err)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "pe-ok") {
		t.Fatalf("unexpected response: %+v", res.Content)
	}
}

func TestDetectEcosystemDoesNotPanic(t *testing.T) {
	report := DetectEcosystem()
	if report.REAMCP.Name != "rea-mcp" || report.CraftAnything.Name != "craft-anything" || report.Ghidra.Name != "ghidra-mcp" {
		t.Fatalf("unexpected report: %+v", report)
	}
}
