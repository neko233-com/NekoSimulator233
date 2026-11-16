// neko-mcp is started by an MCP client and communicates only over stdio.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/agent"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/frameprobe"
	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--vulkan-present-probe" || os.Args[1] == "--vulkan-pixel-probe" || os.Args[1] == "--vulkan-guest-frame-probe") {
		var info vulkan.DeviceInfo
		var err error
		if os.Args[1] == "--vulkan-guest-frame-probe" {
			frame, readErr := frameprobe.Read(os.Stdin)
			if readErr != nil {
				err = readErr
			} else {
				info, err = vulkan.ProbeFrame(frame)
			}
		} else if os.Args[1] == "--vulkan-pixel-probe" {
			info, err = vulkan.ProbePixels()
		} else {
			info, err = vulkan.ProbePresent()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err = json.NewEncoder(os.Stdout).Encode(info); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	server := agent.New(avd.DiscoverManager)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
