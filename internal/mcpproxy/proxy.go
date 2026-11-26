// Package mcpproxy implements a generic black-box stdio MCP transparent proxy
// and local reverse-engineering / desktop-automation ecosystem detector for
// rea-mcp, ghidra-mcp, craft-anything, and agent-desktop.
package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolStatus reports availability and detected path of one local ecosystem tool.
type ToolStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

// EcosystemReport summarizes local reverse-engineering, design, desktop, and
// Android SDK toolchains available to NekoSimulator233 and its MCP agents.
type EcosystemReport struct {
	AgentDesktop  ToolStatus `json:"agent_desktop"`
	REAMCP        ToolStatus `json:"rea_mcp"`
	Ghidra        ToolStatus `json:"ghidra"`
	CraftAnything ToolStatus `json:"craft_anything"`
	AndroidSDK    ToolStatus `json:"android_sdk"`
	WiX           ToolStatus `json:"wix"`
}

// Upstream describes an external stdio MCP server to proxy into neko-mcp.
type Upstream struct {
	Name    string
	Prefix  string
	Command string
	Args    []string
	Env     []string
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// DetectREAMCP locates the locally installed REA MCP runner and its bundled Node/Ghidra/JDK env.
func DetectREAMCP() (Upstream, bool) {
	local := os.Getenv("LOCALAPPDATA")
	if local != "" {
		tools := filepath.Join(local, "Neko233", "ReaTools")
		node := filepath.Join(tools, "node-v24.22.2-win-x64", "node.exe")
		script := filepath.Join(tools, "rea-6.8.0", "node_modules", "rea-agents", "scripts", "rea.mjs")
		if fileExists(node) && fileExists(script) {
			var env []string
			ghidra := filepath.Join(tools, "ghidra_12.1.4_PUBLIC")
			if dirExists(ghidra) {
				env = append(env, "GHIDRA_INSTALL_DIR="+ghidra, "GHIDRA_HEADLESS_MAXMEM=8G")
			}
			jdk := filepath.Join(tools, "jdk-21.0.13+9")
			if dirExists(jdk) {
				env = append(env, "JAVA_HOME="+jdk)
			}
			return Upstream{
				Name:    "rea-mcp",
				Prefix:  "rea_",
				Command: node,
				Args:    []string{script, "mcp"},
				Env:     env,
			}, true
		}
	}
	if bin, err := exec.LookPath("rea"); err == nil {
		return Upstream{Name: "rea-mcp", Prefix: "rea_", Command: bin, Args: []string{"mcp"}}, true
	}
	return Upstream{}, false
}

// DetectCraftAnything locates a locally installed craft-anything binary.
func DetectCraftAnything() (Upstream, bool) {
	if custom := strings.TrimSpace(os.Getenv("NEKO_CRAFT_BIN")); custom != "" && fileExists(custom) {
		return Upstream{Name: "craft-anything", Prefix: "craft_", Command: custom, Args: []string{"mcp"}}, true
	}
	for _, cand := range []string{"craft-anything", "craft"} {
		if bin, err := exec.LookPath(cand); err == nil {
			return Upstream{Name: "craft-anything", Prefix: "craft_", Command: bin, Args: []string{"mcp"}}, true
		}
	}
	local := os.Getenv("LOCALAPPDATA")
	if local != "" {
		p := filepath.Join(local, "Programs", "Craft Anything", "craft-anything.exe")
		if fileExists(p) {
			return Upstream{Name: "craft-anything", Prefix: "craft_", Command: p, Args: []string{"mcp"}}, true
		}
	}
	return Upstream{}, false
}

// DetectGhidraMCP locates a locally configured ghidra-mcp command or local Ghidra installation.
func DetectGhidraMCP() (Upstream, string, bool) {
	local := os.Getenv("LOCALAPPDATA")
	ghidraDir := os.Getenv("GHIDRA_INSTALL_DIR")
	if ghidraDir == "" && local != "" {
		cand := filepath.Join(local, "Neko233", "ReaTools", "ghidra_12.1.4_PUBLIC")
		if dirExists(cand) {
			ghidraDir = cand
		}
	}
	if custom := strings.TrimSpace(os.Getenv("NEKO_GHIDRA_MCP_CMD")); custom != "" {
		fields := strings.Fields(custom)
		if len(fields) > 0 {
			var env []string
			if ghidraDir != "" {
				env = append(env, "GHIDRA_INSTALL_DIR="+ghidraDir)
			}
			return Upstream{
				Name:    "ghidra-mcp",
				Prefix:  "ghidra_",
				Command: fields[0],
				Args:    fields[1:],
				Env:     env,
			}, ghidraDir, true
		}
	}
	if bin, err := exec.LookPath("ghidra-mcp"); err == nil {
		return Upstream{Name: "ghidra-mcp", Prefix: "ghidra_", Command: bin}, ghidraDir, true
	}
	return Upstream{}, ghidraDir, false
}

// DetectEcosystem inspects the host machine for all companion MCP/build toolchains.
func DetectEcosystem() EcosystemReport {
	var r EcosystemReport
	r.AgentDesktop = ToolStatus{Name: "agent-desktop"}
	if bin, err := exec.LookPath("agent-desktop"); err == nil {
		r.AgentDesktop.Available = true
		r.AgentDesktop.Path = bin
		r.AgentDesktop.Notes = "启用 NEKO_DESKTOP_PROXY=1 暴露 desktop_* 工具"
	}

	r.REAMCP = ToolStatus{Name: "rea-mcp"}
	if up, ok := DetectREAMCP(); ok {
		r.REAMCP.Available = true
		r.REAMCP.Path = up.Command
		r.REAMCP.Notes = "启用 NEKO_REA_PROXY=1 暴露 rea_* 二进制逆向工具"
	}

	r.Ghidra = ToolStatus{Name: "ghidra-mcp"}
	if up, ghidraDir, ok := DetectGhidraMCP(); ok {
		r.Ghidra.Available = true
		r.Ghidra.Path = up.Command
		r.Ghidra.Notes = "启用 NEKO_GHIDRA_PROXY=1 暴露 ghidra_* 反编译工具"
	} else if ghidraDir != "" {
		r.Ghidra.Available = true
		r.Ghidra.Path = ghidraDir
		r.Ghidra.Notes = "已通过 REA 集成 Ghidra 12.1.4 Headless；可配置 NEKO_GHIDRA_MCP_CMD 接入独立 ghidra-mcp"
	}

	r.CraftAnything = ToolStatus{Name: "craft-anything"}
	if up, ok := DetectCraftAnything(); ok {
		r.CraftAnything.Available = true
		r.CraftAnything.Path = up.Command
		r.CraftAnything.Notes = "启用 NEKO_CRAFT_PROXY=1 暴露 craft_* 工具"
	} else {
		r.CraftAnything.Notes = "可安装 storytold/craft-anything 并设置 NEKO_CRAFT_BIN 启用 craft_* 洁净室检验"
	}

	r.AndroidSDK = ToolStatus{Name: "android-sdk"}
	local := os.Getenv("LOCALAPPDATA")
	for _, cand := range []string{
		filepath.Join(local, "NekoSimulator233-data", "runtime", "sdk"),
		os.Getenv("ANDROID_HOME"),
		os.Getenv("ANDROID_SDK_ROOT"),
		filepath.Join(local, "Android", "Sdk"),
	} {
		if cand != "" && fileExists(filepath.Join(cand, "emulator", "emulator.exe")) {
			r.AndroidSDK.Available = true
			r.AndroidSDK.Path = cand
			r.AndroidSDK.Notes = "Android Emulator + Platform Tools 就绪"
			break
		}
	}
	if !r.AndroidSDK.Available {
		r.AndroidSDK.Notes = "尚未安装 emulator.exe，可在入口管理器「工具」页或 neko_download_runtime 一键下载"
	}

	r.WiX = ToolStatus{Name: "wix"}
	if bin, err := exec.LookPath("wix"); err == nil {
		r.WiX.Available = true
		r.WiX.Path = bin
		r.WiX.Notes = "WiX 5 本机 MSI 打包就绪"
	} else if home := os.Getenv("USERPROFILE"); home != "" {
		p := filepath.Join(home, ".dotnet", "tools", "wix.exe")
		if fileExists(p) {
			r.WiX.Available = true
			r.WiX.Path = p
			r.WiX.Notes = "WiX 5 本机 MSI 打包就绪"
		}
	}
	return r
}

// Register connects to any stdio MCP server and re-declares all of its tools
// under up.Prefix on server. Connect timeout only guards the handshake so the
// child process stays alive for the lifetime of server.
func Register(ctx context.Context, server *mcp.Server, up Upstream, timeout time.Duration) (int, error) {
	if server == nil || up.Command == "" {
		return 0, fmt.Errorf("mcpproxy: invalid upstream %q", up.Name)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	ok := false
	defer func() {
		if !ok {
			cancel()
		}
	}()
	cmd := exec.Command(up.Command, up.Args...)
	cmd.Env = append(os.Environ(), up.Env...)
	cmd.Stderr = io.Discard
	transport := &mcp.CommandTransport{Command: cmd}
	cli := mcp.NewClient(&mcp.Implementation{Name: "neko-proxy-" + up.Name, Version: "1.0.0"}, nil)
	session, err := cli.Connect(connectCtx, transport, nil)
	if err != nil {
		return 0, fmt.Errorf("%s 连接失败: %w", up.Name, err)
	}
	count, err := RegisterSession(ctx, server, session, up.Name, up.Prefix)
	if err != nil {
		_ = session.Close()
		return 0, err
	}
	ok = true
	return count, nil
}

// RegisterSession re-declares every tool from an already-connected MCP client session.
func RegisterSession(ctx context.Context, server *mcp.Server, session *mcp.ClientSession, upstreamName, prefix string) (int, error) {
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("%s 工具列表读取失败: %w", upstreamName, err)
	}
	count := 0
	for _, t := range list.Tools {
		if t == nil || t.Name == "" {
			continue
		}
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			continue
		}
		origName := t.Name
		tool := &mcp.Tool{
			Name:        prefix + origName,
			Description: fmt.Sprintf("[%s] %s", upstreamName, t.Description),
			InputSchema: json.RawMessage(raw),
			Annotations: t.Annotations,
		}
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args any
			if req != nil && req.Params != nil {
				args = req.Params.Arguments
			}
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: origName, Arguments: args})
			if err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%s %s 调用失败: %v", upstreamName, origName, err)}},
					IsError: true,
				}, nil
			}
			if res == nil {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: upstreamName + " 返回空结果"}}, IsError: true}, nil
			}
			return res, nil
		})
		count++
	}
	if count == 0 {
		return 0, fmt.Errorf("%s 未声明任何工具", upstreamName)
	}
	return count, nil
}
