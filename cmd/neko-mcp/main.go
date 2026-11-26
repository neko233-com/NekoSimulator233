// neko-mcp is started by an MCP client and communicates only over stdio.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/agent"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/desktopctl"
	"github.com/neko233-com/NekoSimulator233/internal/frameprobe"
	"github.com/neko233-com/NekoSimulator233/internal/mcpproxy"
	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

func main() {
	// MuMu parity: new instances use the userdebug image by default so phone
	// identity injection is available; set NEKO_PREFER_USERDEBUG_IMAGE=0 to opt out.
	if os.Getenv("NEKO_PREFER_USERDEBUG_IMAGE") == "" {
		_ = os.Setenv("NEKO_PREFER_USERDEBUG_IMAGE", "1")
	}
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
	// Optional agent-desktop proxy (rea-mcp style): re-declare every tool exposed by
	// `agent-desktop mcp` over stdio and delegate calls to it. Failure is non-fatal;
	// agent-desktop is an optional dependency and the hand-written bridge above still
	// works without it.
	if enabled, ok := os.LookupEnv("NEKO_DESKTOP_PROXY"); ok && enabled != "0" && enabled != "" {
		if count, err := desktopctl.RegisterProxy(context.Background(), server, os.Getenv("NEKO_DESKTOP_BIN"), 5*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, "agent-desktop proxy disabled:", err)
		} else {
			fmt.Fprintf(os.Stderr, "agent-desktop proxy: %d tools registered\n", count)
		}
	}
	if enabled, ok := os.LookupEnv("NEKO_REA_PROXY"); ok && enabled != "0" && enabled != "" {
		if up, found := mcpproxy.DetectREAMCP(); found {
			if count, err := mcpproxy.Register(context.Background(), server, up, 20*time.Second); err != nil {
				fmt.Fprintln(os.Stderr, "rea-mcp proxy disabled:", err)
			} else {
				fmt.Fprintf(os.Stderr, "rea-mcp proxy: %d tools registered\n", count)
			}
		}
	}
	if enabled, ok := os.LookupEnv("NEKO_GHIDRA_PROXY"); ok && enabled != "0" && enabled != "" {
		if up, _, found := mcpproxy.DetectGhidraMCP(); found {
			if count, err := mcpproxy.Register(context.Background(), server, up, 20*time.Second); err != nil {
				fmt.Fprintln(os.Stderr, "ghidra-mcp proxy disabled:", err)
			} else {
				fmt.Fprintf(os.Stderr, "ghidra-mcp proxy: %d tools registered\n", count)
			}
		}
	}
	if enabled, ok := os.LookupEnv("NEKO_CRAFT_PROXY"); ok && enabled != "0" && enabled != "" {
		if up, found := mcpproxy.DetectCraftAnything(); found {
			if count, err := mcpproxy.Register(context.Background(), server, up, 20*time.Second); err != nil {
				fmt.Fprintln(os.Stderr, "craft-anything proxy disabled:", err)
			} else {
				fmt.Fprintf(os.Stderr, "craft-anything proxy: %d tools registered\n", count)
			}
		}
	}
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
