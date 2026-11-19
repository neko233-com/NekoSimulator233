// Package desktopctl bridges Neko's emulator windows to the optional
// agent-desktop CLI (github.com/vercel-labs/agent-desktop). It only uses
// commands that CLI actually provides (version / list-windows); desktop
// observation and input are exposed by the transparent MCP proxy in proxy.go.
package desktopctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultViewerSuffix is appended to an instance name to form the viewer
// window title. It matches mygo.NewWindow in viewer.go.
const DefaultViewerSuffix = " - NekoSimulator233"

// EnvBinary overrides the agent-desktop binary location. EnvNoAutoUpdate
// disables its network update check for spawned CLI processes.
const (
	EnvBinary       = "NEKO_DESKTOP_BIN"
	EnvNoAutoUpdate = "AGENT_DESKTOP_NO_AUTO_UPDATE"
)

type runner func(ctx context.Context, path string, args ...string) ([]byte, error)

// Client locates and invokes the agent-desktop CLI.
type Client struct {
	path   string
	runner runner
}

func New() *Client { return &Client{} }

// NewWithRunner returns a client using the supplied runner and optional fixed
// binary path. Used by tests.
func NewWithRunner(path string, r runner) *Client { return &Client{path: path, runner: r} }

// Resolve finds the binary via an explicit path, NEKO_DESKTOP_BIN, PATH, then
// the conventional per-user install directory.
func (c *Client) Resolve() (string, error) {
	if c.path != "" {
		return c.path, nil
	}
	if p := strings.TrimSpace(os.Getenv(EnvBinary)); p != "" {
		c.path = p
		return c.path, nil
	}
	if p, err := exec.LookPath("agent-desktop"); err == nil {
		c.path = p
		return c.path, nil
	}
	if local, err := os.UserHomeDir(); err == nil {
		name := "agent-desktop"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		p := filepath.Join(local, ".local", "bin", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			c.path = p
			return c.path, nil
		}
	}
	return "", fmt.Errorf("未找到 agent-desktop；请安装 https://github.com/vercel-labs/agent-desktop 或设置 %s 指向可执行文件", EnvBinary)
}

// Status is the structured result of Client.Status.
type Status struct {
	OK        bool   `json:"ok"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Window describes one agent-desktop-visible top-level window.
type Window struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	AppName   string `json:"app_name"`
	PID       int    `json:"pid"`
	Focused   bool   `json:"is_focused"`
	Minimized bool   `json:"minimized"`
	Visible   bool   `json:"visible"`
	Bounds    Bounds `json:"bounds"`
}

type Bounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Status reports availability and version without starting an emulator.
func (c *Client) Status(ctx context.Context) Status {
	path, err := c.Resolve()
	if err != nil {
		return Status{Reason: err.Error()}
	}
	run := c.runner
	if run == nil {
		run = execRunner
	}
	out, err := run(ctx, path, "version")
	if err != nil {
		return Status{Path: path, Reason: "agent-desktop version 调用失败: " + err.Error()}
	}
	var env envelope
	version := strings.TrimSpace(string(out))
	if json.Unmarshal(out, &env) == nil && env.OK {
		if v, ok := env.Data["version"].(string); ok && v != "" {
			version = v
		}
	}
	return Status{OK: true, Available: true, Path: path, Version: version}
}

// ListWindows returns visible windows, optionally filtered by app/process name.
func (c *Client) ListWindows(ctx context.Context, app string) ([]Window, error) {
	path, err := c.Resolve()
	if err != nil {
		return nil, err
	}
	args := []string{"list-windows"}
	if strings.TrimSpace(app) != "" {
		args = append(args, "--app", app)
	}
	run := c.runner
	if run == nil {
		run = execRunner
	}
	out, err := run(ctx, path, args...)
	if err != nil {
		return nil, fmt.Errorf("agent-desktop list-windows 失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	env, err := parseEnvelope(out)
	if err != nil {
		return nil, fmt.Errorf("agent-desktop list-windows 返回无效 JSON: %w", err)
	}
	if !env.OK {
		return nil, errors.New("agent-desktop list-windows 返回错误")
	}
	var shaped struct {
		Data struct {
			Windows []Window `json:"windows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &shaped); err != nil {
		return nil, err
	}
	return shaped.Data.Windows, nil
}

// envelope is the CLI response wrapper. Data stays generic because fields
// differ per command; none of it is treated as trusted instructions.
type envelope struct {
	OK   bool                   `json:"ok"`
	Data map[string]interface{} `json:"data"`
}

func parseEnvelope(out []byte) (envelope, error) {
	var env envelope
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&env); err != nil {
		return envelope{}, err
	}
	return env, nil
}

func execRunner(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), EnvNoAutoUpdate+"=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.Bytes(), err
}

// ViewerApp derives the default target window title for an instance.
func ViewerApp(instance string) string { return instance + DefaultViewerSuffix }
