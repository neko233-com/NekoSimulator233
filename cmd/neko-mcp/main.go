// neko-mcp is started by an MCP client and communicates only over stdio.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/agent"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/frameprobe"
	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--vulkan-present-probe" || os.Args[1] == "--vulkan-pixel-probe" || strings.HasPrefix(os.Args[1], "--vulkan-guest-frame-probe")) {
		var info vulkan.DeviceInfo
		var err error
		if strings.HasPrefix(os.Args[1], "--vulkan-guest-frame-probe") {
			width, height, fit, parseErr := parseFrameProbeMode(os.Args[1])
			if parseErr != nil {
				err = parseErr
			} else {
				frame, readErr := frameprobe.Read(os.Stdin)
				if readErr != nil {
					err = readErr
				} else if fit {
					info, err = vulkan.ProbeFittedFrame(frame, width, height)
				} else {
					info, err = vulkan.ProbeFrame(frame)
				}
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
	server := agent.New(func() (avd.Manager, error) {
		executable, err := os.Executable()
		if err != nil {
			return avd.Manager{}, err
		}
		if err := avd.PrepareBundledRuntime(context.Background(), filepath.Join(filepath.Dir(executable), "android-runtime.zip"), os.Getenv("LOCALAPPDATA")); err != nil {
			return avd.Manager{}, err
		}
		return avd.DiscoverManager()
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFrameProbeMode(mode string) (width, height int, fit bool, err error) {
	if mode == "--vulkan-guest-frame-probe" {
		return 0, 0, false, nil
	}
	n, scanErr := fmt.Sscanf(mode, "--vulkan-guest-frame-probe=%dx%d", &width, &height)
	if n != 2 || scanErr != nil || mode != fmt.Sprintf("--vulkan-guest-frame-probe=%dx%d", width, height) || width <= 0 || height <= 0 || width > 8192 || height > 8192 || int64(width)*int64(height) > 32*1024*1024 {
		return 0, 0, false, fmt.Errorf("Vulkan 画面目标尺寸无效")
	}
	return width, height, true, nil
}
