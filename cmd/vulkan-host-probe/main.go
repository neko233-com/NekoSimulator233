//go:build windows && amd64

// vulkan-host-probe exercises the host Vulkan bootstrap in a separate process.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

func main() {
	testSurface := flag.Bool("surface", false, "create and destroy an isolated hidden test swapchain")
	testPresent := flag.Bool("present", false, "submit a solid-color frame in this short-lived process")
	flag.Parse()
	var info vulkan.DeviceInfo
	var err error
	if *testPresent {
		info, err = vulkan.ProbePresent()
	} else {
		info, err = vulkan.InspectHost(*testSurface)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(info); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
