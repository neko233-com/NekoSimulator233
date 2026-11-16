//go:build windows

package avd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DeviceInfo reports guest properties, not proof that a particular app runs.
type DeviceInfo struct {
	API          int      `json:"api"`
	ABIs         []string `json:"abis"`
	NativeBridge string   `json:"native_bridge,omitempty"`
}

func parseABIList(value string) ([]string, error) {
	var out []string
	for _, token := range strings.Split(strings.TrimSpace(value), ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		for _, r := range token {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return nil, errors.New("Android ABI 列表无效")
			}
		}
		out = append(out, token)
	}
	if len(out) == 0 {
		return nil, errors.New("Android 未报告支持的 ABI")
	}
	return out, nil
}

// DeviceInfo reads only three non-sensitive system properties from an already
// verified instance. It does not start an emulator or modify guest state.
func (m Manager) DeviceInfo(ctx context.Context, instance Instance) (DeviceInfo, error) {
	read := func(key string) (string, error) {
		data, err := m.adb(ctx, instance.Serial, "shell", "getprop", key)
		if err != nil {
			return "", fmt.Errorf("读取 Android 属性 %s 失败：%w", key, err)
		}
		if len(data) > 4096 {
			return "", errors.New("Android 属性响应过大")
		}
		return strings.TrimSpace(string(data)), nil
	}
	sdk, err := read("ro.build.version.sdk")
	if err != nil {
		return DeviceInfo{}, err
	}
	api, err := strconv.Atoi(sdk)
	if err != nil || api <= 0 {
		return DeviceInfo{}, errors.New("Android API 版本无效")
	}
	rawABIs, err := read("ro.product.cpu.abilist")
	if err != nil {
		return DeviceInfo{}, err
	}
	abis, err := parseABIList(rawABIs)
	if err != nil {
		return DeviceInfo{}, err
	}
	bridge, err := read("ro.dalvik.vm.native.bridge")
	if err != nil {
		return DeviceInfo{}, err
	}
	return DeviceInfo{API: api, ABIs: abis, NativeBridge: bridge}, nil
}
