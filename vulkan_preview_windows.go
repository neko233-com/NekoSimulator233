package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/frameprobe"
)

type previewOutput struct{ bytes.Buffer }

func (b *previewOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 16384 - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(n, remaining)])
	}
	return n, nil
}

type vulkanPreview struct {
	input  io.WriteCloser
	cancel context.CancelFunc
	done   chan error
	stderr *previewOutput
	once   sync.Once
	buffer [32 * 1024]byte
}

func startVulkanPreview(ctx context.Context) (*vulkanPreview, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, exe, "--vulkan-preview-worker")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output := &previewOutput{}
	cmd.Stderr = output
	if err = cmd.Start(); err != nil {
		cancel()
		_ = input.Close()
		return nil, err
	}
	p := &vulkanPreview{input: input, cancel: cancel, done: make(chan error, 1), stderr: output}
	go func() { p.done <- cmd.Wait(); close(p.done) }()
	return p, nil
}

func (p *vulkanPreview) send(frame *image.RGBA) error {
	r, err := frameprobe.Reader(frame)
	if err != nil {
		return err
	}
	// Closing the child on a stalled pipe bounds shutdown even if a GPU driver hangs.
	timedOut := make(chan struct{})
	timer := time.AfterFunc(5*time.Second, func() { close(timedOut); p.cancel() })
	_, err = io.CopyBuffer(p.input, r, p.buffer[:])
	if !timer.Stop() {
		<-timedOut
		return errors.New("Vulkan 画面进程超时")
	}
	if err != nil {
		select {
		case childErr := <-p.done:
			if p.stderr.Len() != 0 {
				return fmt.Errorf("Vulkan 画面进程结束：%s", p.stderr.String())
			}
			if childErr != nil {
				return fmt.Errorf("Vulkan 画面进程结束：%w", childErr)
			}
		default:
		}
		return fmt.Errorf("Vulkan 画面传输失败：%w", err)
	}
	return nil
}

func (p *vulkanPreview) close() { p.once.Do(func() { p.cancel(); _ = p.input.Close() }) }
