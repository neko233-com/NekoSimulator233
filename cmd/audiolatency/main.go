// audiolatency measures the timestamped PCM relay transport. With -wav it
// streams PCM captured from a real guest audio backend (QEMU WAV capture)
// instead of a synthetic source.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/audioforward"
)

func wavPCM(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("not a RIFF/WAVE file")
	}
	for i := 12; i+8 <= len(data); {
		id := string(data[i : i+4])
		size := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		if id == "data" {
			// QEMU's WAV backend streams with a zero data size; use the rest
			// of the file in that case.
			if size == 0 {
				return data[i+8:], nil
			}
			end := i + 8 + size
			if end > len(data) {
				end = len(data)
			}
			return data[i+8 : end], nil
		}
		i += 8 + size + (size & 1)
	}
	return nil, errors.New("WAVE data chunk not found")
}

func main() {
	wavPath := flag.String("wav", "", "path to a PCM WAV captured from the guest audio backend")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	listener, addr, err := audioforward.Listen()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	go func() { _ = audioforward.Serve(ctx, listener) }()
	var report audioforward.LatencyReport
	if *wavPath != "" {
		pcm, err := wavPCM(*wavPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		report, err = audioforward.StreamPCM(ctx, addr, pcm, 1280, 5*time.Millisecond)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		report, err = audioforward.Stream(ctx, addr, 200, 1280, 5*time.Millisecond)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(b))
}
