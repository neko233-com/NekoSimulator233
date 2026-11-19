// mcpctl is a small diagnostic client that spawns an MCP server over stdio,
// lists its tools, or calls one tool with JSON arguments. It is useful for
// smoke-testing the neko-mcp binary and the optional agent-desktop proxy without
// needing a full MCP host.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mcpctl <server-exe> [server-arg...] -- <list|call> [tool-name] [json-args]")
		os.Exit(2)
	}
	sep := -1
	for i, a := range os.Args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(os.Args) {
		fmt.Fprintln(os.Stderr, "missing -- separator or command")
		os.Exit(2)
	}
	serverArgs := os.Args[1:sep]
	cmdArgs := os.Args[sep+1:]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, serverArgs[0], serverArgs[1:]...)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpctl", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer session.Close()

	switch cmdArgs[0] {
	case "batch":
		if len(cmdArgs) < 2 {
			fmt.Fprintln(os.Stderr, "batch needs a JSON file path")
			os.Exit(2)
		}
		raw, err := os.ReadFile(cmdArgs[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		var calls []struct {
			Name string `json:"name"`
			Args any    `json:"args"`
		}
		if err := json.Unmarshal(raw, &calls); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		for _, call := range calls {
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: call.Name, Arguments: call.Args})
			if err != nil || res.IsError {
				out, _ := json.Marshal(res)
				fmt.Printf("FAILED %s: %s %v\n", call.Name, string(out), err)
				os.Exit(1)
			}
			b, _ := json.Marshal(res.StructuredContent)
			fmt.Printf("OK %s %s\n", call.Name, string(b))
		}
	case "bench-screenshot":
		count := 20
		if len(cmdArgs) >= 2 {
			if n, err := strconv.Atoi(cmdArgs[1]); err == nil && n > 0 && n <= 500 {
				count = n
			}
		}
		var total, minMS, maxMS time.Duration
		bytesTotal := 0
		for i := 0; i < count; i++ {
			start := time.Now()
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "neko_screenshot", Arguments: map[string]any{}})
			elapsed := time.Since(start)
			if err != nil || res.IsError {
				fmt.Fprintln(os.Stderr, "screenshot failed:", err)
				os.Exit(1)
			}
			total += elapsed
			if i == 0 || elapsed < minMS {
				minMS = elapsed
			}
			if elapsed > maxMS {
				maxMS = elapsed
			}
			for _, c := range res.Content {
				if img, ok := c.(*mcp.ImageContent); ok {
					bytesTotal += len(img.Data)
				}
			}
		}
		avg := total / time.Duration(count)
		fmt.Printf("{\"frames\":%d,\"avg_ms\":%.2f,\"min_ms\":%.2f,\"max_ms\":%.2f,\"avg_png_bytes\":%d,\"effective_fps\":%.2f}\n",
			count, float64(avg.Microseconds())/1000, float64(minMS.Microseconds())/1000,
			float64(maxMS.Microseconds())/1000, bytesTotal/count, 1000.0/(float64(avg.Microseconds())/1000))
	case "list":
		res, err := session.ListTools(ctx, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "list:", err)
			os.Exit(1)
		}
		for _, t := range res.Tools {
			fmt.Println(t.Name)
		}
	case "list-json":
		res, err := session.ListTools(ctx, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "list:", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(res.Tools, "", "  ")
		fmt.Println(string(b))
	case "call":
		if len(cmdArgs) < 2 {
			fmt.Fprintln(os.Stderr, "call needs a tool name")
			os.Exit(2)
		}
		var args any
		if len(cmdArgs) >= 3 {
			raw := []byte(cmdArgs[2])
			if strings.HasPrefix(cmdArgs[2], "@") {
				var err error
				raw, err = os.ReadFile(strings.TrimPrefix(cmdArgs[2], "@"))
				if err != nil {
					fmt.Fprintln(os.Stderr, "read args file:", err)
					os.Exit(2)
				}
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				fmt.Fprintln(os.Stderr, "bad json args:", err)
				os.Exit(2)
			}
		}
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: cmdArgs[1], Arguments: args})
		if err != nil {
			fmt.Fprintln(os.Stderr, "call:", err)
			os.Exit(1)
		}
		out, err := json.Marshal(struct {
			IsError bool          `json:"is_error"`
			Content []mcp.Content `json:"content"`
		}{IsError: res.IsError, Content: res.Content})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(out))
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmdArgs[0])
		os.Exit(2)
	}
}
