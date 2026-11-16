//go:build windows && amd64

// vulkan-host-probe exercises the host Vulkan bootstrap in a separate process.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

func main() {
	h, err := vulkan.OpenHost()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	info := h.Info()
	if err = h.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(info); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
