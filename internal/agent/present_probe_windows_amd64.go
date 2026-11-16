//go:build windows && amd64

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/vulkan"
)

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		return 0, errors.New("子进程输出过大")
	}
	return b.Buffer.Write(data)
}

// A dedicated subprocess contains any stalled GPU work and owns its hidden
// window. It is terminated on timeout, without affecting running emulators.
func runPresentProbe(ctx context.Context, pixels bool) (vulkan.DeviceInfo, error) {
	exe, err := os.Executable()
	if err != nil {
		return vulkan.DeviceInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	mode := "--vulkan-present-probe"
	if pixels {
		mode = "--vulkan-pixel-probe"
	}
	cmd := exec.CommandContext(ctx, exe, mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var stdout, stderr boundedOutput
	stdout.limit, stderr.limit = 65536, 16384
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return vulkan.DeviceInfo{}, fmt.Errorf("Vulkan 呈现测试超时或取消：%w", ctx.Err())
	}
	if err != nil {
		if stderr.Len() > 0 {
			return vulkan.DeviceInfo{}, fmt.Errorf("Vulkan 呈现测试失败：%s", stderr.String())
		}
		return vulkan.DeviceInfo{}, fmt.Errorf("Vulkan 呈现测试失败：%w", err)
	}
	var info vulkan.DeviceInfo
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&info); err != nil {
		return vulkan.DeviceInfo{}, fmt.Errorf("Vulkan 呈现测试响应无效：%w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return vulkan.DeviceInfo{}, errors.New("Vulkan 呈现测试响应包含额外内容")
	}
	if info.TestSurface == nil || !info.TestSurface.ClearSubmitted {
		return vulkan.DeviceInfo{}, errors.New("Vulkan 呈现测试没有完成提交")
	}
	if pixels && !info.TestSurface.PixelsVerified {
		return vulkan.DeviceInfo{}, errors.New("Vulkan 像素回读测试没有完成")
	}
	return info, nil
}
