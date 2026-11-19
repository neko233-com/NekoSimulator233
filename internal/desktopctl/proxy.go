// Package agent-desktop proxy mirrors the rea-mcp pattern: instead of reimplementing
// an upstream toolset, it connects to desktop's own MCP server as a black-box
// child process, re-declares every tool it advertises, and delegates each call.
// This keeps the full desktop command surface available even when the
// hand-written bridge only covers a few tools, and stays current across
// desktop upgrades without code changes here.
package desktopctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProxyPrefix namespaces proxied tools so they never collide with neko's own
// neko_* tools or with the hand-written neko_desktop_* bridge.
const ProxyPrefix = "desktop_"

// RegisterProxy connects to `agent-desktop mcp` over stdio and re-declares every
// tool it advertises on the given server. It returns the number of tools
// registered. Failures are returned to the caller so desktop can remain a
// strictly optional dependency: the caller should log and continue.
//
// The upstream client session is captured by the registered handlers and lives
// as long as the server does.
func RegisterProxy(ctx context.Context, server *mcp.Server, binary string, timeout time.Duration) (int, error) {
	if server == nil {
		return 0, fmt.Errorf("agent-desktop proxy: nil server")
	}
	if binary == "" {
		var err error
		binary, err = (&Client{}).Resolve()
		if err != nil {
			return 0, err
		}
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// The connect timeout must NOT own the child process: this function returns
	// long before the proxy is used, so a context-bound command would be killed
	// when the timeout fires. Use a plain command and let the timeout context
	// expire naturally after a successful handshake.
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	ok := false
	defer func() {
		if !ok {
			cancel()
		}
	}()

	cmd := exec.Command(binary, "mcp")
	cmd.Env = append(os.Environ(), EnvNoAutoUpdate+"=1")
	// stdout carries the MCP stream; never let child stderr contaminate it.
	cmd.Stderr = io.Discard
	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "neko-desktop-proxy", Version: "1.0.0"}, nil)
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return 0, fmt.Errorf("agent-desktop mcp 连接失败: %w", err)
	}

	registered, err := RegisterProxySession(ctx, server, session)
	if err != nil {
		_ = session.Close()
		return 0, err
	}
	ok = true
	return registered, nil
}

// RegisterProxySession re-declares every tool advertised by an already-connected
// upstream MCP session. Keeping this separate from process spawning makes the
// proxy logic testable with in-memory transports.
func RegisterProxySession(ctx context.Context, server *mcp.Server, session *mcp.ClientSession) (int, error) {
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("agent-desktop mcp 工具列表读取失败: %w", err)
	}

	registered := 0
	for _, upstream := range result.Tools {
		if upstream == nil || upstream.Name == "" {
			continue
		}
		schema := upstream.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			continue
		}
		name := upstream.Name
		description := upstream.Description
		tool := &mcp.Tool{
			Name:        ProxyPrefix + name,
			Description: "[agent-desktop] " + description,
			InputSchema: json.RawMessage(raw),
			Annotations: upstream.Annotations,
		}
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args any
			if req != nil && req.Params != nil {
				args = req.Params.Arguments
			}
			upstreamResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("agent-desktop %s 调用失败: %v", name, err)}},
					IsError: true,
				}, nil
			}
			if upstreamResult == nil {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "agent-desktop 返回空结果"}}, IsError: true}, nil
			}
			return upstreamResult, nil
		})
		registered++
	}
	if registered == 0 {
		return 0, fmt.Errorf("agent-desktop mcp 未声明任何工具")
	}
	return registered, nil
}
