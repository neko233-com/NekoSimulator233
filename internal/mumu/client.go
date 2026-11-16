package mumu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Instance struct {
	Index          string `json:"index"`
	Name           string `json:"name"`
	AndroidVersion string `json:"android_version"`
	PlayerState    string `json:"player_state"`
	Started        bool   `json:"is_process_started"`
	AndroidStarted bool   `json:"is_android_started"`
}

type Client struct{ CLI string }

func New(root string) (Client, error) {
	cli := filepath.Join(root, "nx_main", "mumu-cli.exe")
	if info, err := os.Stat(cli); err != nil || info.IsDir() {
		return Client{}, fmt.Errorf("未找到 MuMu CLI：%s", cli)
	}
	return Client{CLI: cli}, nil
}

func (c Client) run(args ...string) ([]byte, error) {
	return c.runContext(context.Background(), args...)
}

func (c Client) runContext(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.CLI, args...)
	hideConsole(cmd)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("MuMu 命令已取消或超时：%w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("MuMu 命令失败：%w", err)
	}
	var response struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return nil, fmt.Errorf("MuMu 响应无效：%w", err)
	}
	if response.ErrCode != 0 {
		return nil, fmt.Errorf("MuMu 错误 %d：%s", response.ErrCode, response.ErrMsg)
	}
	return out, nil
}

func (c Client) List() ([]Instance, error) {
	return c.ListContext(context.Background())
}

// ListContext reads current instance metadata without starting or changing it.
func (c Client) ListContext(ctx context.Context) ([]Instance, error) {
	out, err := c.runContext(ctx, "info", "--vmindex", "all")
	if err != nil {
		return nil, err
	}
	return parseInstances(out)
}

func parseInstances(out []byte) ([]Instance, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	var result []Instance
	for key, value := range raw {
		if _, err := strconv.Atoi(key); err != nil {
			continue
		}
		var instance Instance
		if err := json.Unmarshal(value, &instance); err != nil {
			return nil, fmt.Errorf("实例 %s 的数据无效：%w", key, err)
		}
		if instance.Index != key {
			return nil, fmt.Errorf("实例索引不一致：%s / %s", key, instance.Index)
		}
		result = append(result, instance)
	}
	sort.Slice(result, func(i, j int) bool {
		a, _ := strconv.Atoi(result[i].Index)
		b, _ := strconv.Atoi(result[j].Index)
		return a < b
	})
	return result, nil
}

func (c Client) Open(instance Instance) error {
	if _, err := strconv.ParseUint(instance.Index, 10, 32); err != nil || strings.TrimSpace(instance.Index) != instance.Index {
		return errors.New("实例索引无效")
	}
	action := "launch"
	if instance.Started {
		action = "show_window"
	}
	_, err := c.run("control", "--vmindex", instance.Index, "--version", "auto", action)
	return err
}
