// neko-mcp is started by an MCP client and communicates only over stdio.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/agent"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func main() {
	server := agent.New(avd.DiscoverManager)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
