package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/runtimeinstall"
)

// runtimeSDKDir is the separate, downloadable SDK root. DiscoverManager
// prefers it before ANDROID_HOME.
func runtimeSDKDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "runtime", "sdk")
}

// downloadRuntime fetches the separately hosted runtime manifest and extracts
// it into runtimeSDKDir. The MSI itself stays small.
func (a *app) downloadRuntime() {
	if a.busy || a.runtimeCancel != nil {
		return
	}
	manifest := a.runtimeManifest
	if manifest == "" {
		a.message = "请填写运行时清单地址（HTTPS 或本地 manifest.json 路径）"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runtimeCancel = cancel
	a.busy = true
	a.message = "正在下载 Android 运行组件…"
	go func() {
		start := time.Now()
		doc, err := runtimeinstall.LoadManifest(ctx, manifest)
		if err == nil {
			err = runtimeinstall.Download(ctx, doc, runtimeSDKDir())
		}
		a.window.Update(func() {
			a.runtimeCancel = nil
			a.busy = false
			if err != nil {
				a.message = "运行组件下载失败：" + err.Error()
				return
			}
			a.message = fmt.Sprintf("运行组件已安装到 %s（耗时 %s）", runtimeSDKDir(), time.Since(start).Round(time.Second))
			a.refresh()
		})
	}()
}
