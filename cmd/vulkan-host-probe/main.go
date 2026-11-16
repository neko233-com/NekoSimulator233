//go:build windows && amd64

// vulkan-host-probe exercises the host Vulkan bootstrap in a separate process.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"os"

	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

func main() {
	testSurface := flag.Bool("surface", false, "create and destroy an isolated hidden test swapchain")
	testPresent := flag.Bool("present", false, "submit a solid-color frame in this short-lived process")
	testPixels := flag.Bool("pixels", false, "upload and verify a patterned frame in this short-lived process")
	testLetterbox := flag.Bool("letterbox", false, "verify GPU black bars and an unscaled patterned frame in a 64x64 hidden window")
	testGPUScale := flag.Bool("gpu-scale", false, "verify integer GPU scaling of a patterned frame in a 64x64 hidden window")
	flag.Parse()
	var info vulkan.DeviceInfo
	var err error
	if *testGPUScale {
		frame := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				i := y*frame.Stride + x*4
				frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2], frame.Pix[i+3] = byte(x*7), byte(y*7), byte(x+y), 255
			}
		}
		info, err = vulkan.ProbeGPUScaledFrame(frame, 64, 64)
		if err == nil && (info.TestSurface == nil || !info.TestSurface.GPUScaled) {
			err = fmt.Errorf("宿主不支持实验性 GPU 缩放路径")
		}
	} else if *testLetterbox {
		frame := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 64; x++ {
				i := y*frame.Stride + x*4
				frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2], frame.Pix[i+3] = byte(x*3), byte(y*5), byte(x+y), 255
			}
		}
		info, err = vulkan.ProbeFittedFrame(frame, 64, 64)
	} else if *testPixels {
		info, err = vulkan.ProbePixels()
	} else if *testPresent {
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
