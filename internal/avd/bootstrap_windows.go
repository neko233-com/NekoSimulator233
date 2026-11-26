//go:build windows

package avd

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultCommandLineToolsURL = "https://dl.google.com/android/repository/commandlinetools-win-11076708_latest.zip"
	DefaultPlatformToolsURL    = "https://dl.google.com/android/repository/platform-tools-latest-windows.zip"
)

// DefaultBootstrapSDKRoot returns the managed NekoSimulator233 Android SDK root
// under %LOCALAPPDATA%\NekoSimulator233-data\runtime\sdk.
func DefaultBootstrapSDKRoot() string {
	local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if local == "" {
		local = filepath.Join(os.TempDir(), "NekoSimulator233-data")
	}
	return filepath.Join(local, "NekoSimulator233-data", "runtime", "sdk")
}

// IsMissingSDKError reports whether err indicates the local Android SDK or
// Command-line Tools are not yet installed.
func IsMissingSDKError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Android SDK") ||
		strings.Contains(msg, "Command-line Tools") ||
		strings.Contains(msg, "ANDROID_HOME") ||
		strings.Contains(msg, "sdkmanager")
}

// BootstrapRuntime downloads and installs the minimal Android SDK runtime
// (cmdline-tools, platform-tools, emulator, and Android 15/14 system image)
// into sdkRoot when no local SDK is present.
func BootstrapRuntime(ctx context.Context, sdkRoot string, notify func(stage string, percent int)) error {
	if strings.TrimSpace(sdkRoot) == "" {
		sdkRoot = DefaultBootstrapSDKRoot()
	}
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		return err
	}
	if _, err := sdkManagerJava(); err != nil {
		return fmt.Errorf("未检测到 Java 17 运行时，请先运行 configure-jdk.ps1 或安装 JDK 17：%w", err)
	}
	report := func(stage string, pct int) {
		if notify != nil {
			notify(stage, pct)
		}
	}

	if _, _, err := sdkManagerTools(sdkRoot); err != nil {
		report("正在下载 Android Command-line Tools…", 5)
		latestDir := filepath.Join(sdkRoot, "cmdline-tools", "latest")
		if err := downloadAndExtractZip(ctx, DefaultCommandLineToolsURL, latestDir, "cmdline-tools/", func(p int) {
			report("正在下载 Android Command-line Tools…", 5+p*20/100)
		}); err != nil {
			return fmt.Errorf("下载 Command-line Tools 失败：%w", err)
		}
	}

	if !fileExists(filepath.Join(sdkRoot, "platform-tools", "adb.exe")) {
		report("正在下载 Android Platform-Tools (ADB)…", 25)
		if err := downloadAndExtractZip(ctx, DefaultPlatformToolsURL, sdkRoot, "", func(p int) {
			report("正在下载 Android Platform-Tools (ADB)…", 25+p*20/100)
		}); err != nil {
			return fmt.Errorf("下载 Platform-Tools 失败：%w", err)
		}
	}

	if err := writeStandardSDKAcceptLicenses(sdkRoot); err != nil {
		return fmt.Errorf("写入 SDK 协议许可失败：%w", err)
	}

	if !fileExists(filepath.Join(sdkRoot, "emulator", "emulator.exe")) {
		report("正在安装 Android Emulator 引擎…", 50)
		if err := runSDKPackageInstall(ctx, sdkRoot, "emulator", func(p int) {
			report("正在安装 Android Emulator 引擎…", 50+p*20/100)
		}); err != nil {
			return fmt.Errorf("安装 Android Emulator 失败：%w", err)
		}
	}

	if !hasAnySystemImage(sdkRoot) {
		report("正在下载 Android 15 系统镜像…", 70)
		err := runSDKPackageInstall(ctx, sdkRoot, "system-images;android-35;google_apis;x86_64", func(p int) {
			report("正在下载 Android 15 系统镜像…", 70+p*30/100)
		})
		if err != nil {
			report("正在下载 Android 14 系统镜像…", 70)
			err = runSDKPackageInstall(ctx, sdkRoot, "system-images;android-34;google_apis;x86_64", func(p int) {
				report("正在下载 Android 14 系统镜像…", 70+p*30/100)
			})
		}
		if err != nil {
			return fmt.Errorf("安装 Android 系统镜像失败：%w", err)
		}
	}

	report("Android 运行时部署完成", 100)
	return nil
}

func hasAnySystemImage(sdkRoot string) bool {
	matches, _ := filepath.Glob(filepath.Join(sdkRoot, "system-images", "*", "*", "*", "system.img"))
	return len(matches) > 0
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func writeStandardSDKAcceptLicenses(sdkRoot string) error {
	licDir := filepath.Join(sdkRoot, "licenses")
	if err := os.MkdirAll(licDir, 0o755); err != nil {
		return err
	}
	licenses := map[string]string{
		"android-sdk-license":          "\n24333f8a63b6825ea9c5514f83c2829b004d1fee\n8933bad161af4178b1185d1a37fbf41ea5269c55\nd56f5187479451eabf01fb78af6dfcb131a6481e\n",
		"android-sdk-preview-license":  "\n84831b9409646a918e30573bab4c9c91346d8abd\n",
		"android-googletv-license":     "\n601085b94cd77f0b54ff86406957099ebe79c4d6\n",
		"google-gdk-license":           "\n33b6a2b64607f11b759f320ef9dff4ae5c47d97a\n",
	}
	for name, body := range licenses {
		if err := os.WriteFile(filepath.Join(licDir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func downloadAndExtractZip(ctx context.Context, url, destDir, stripPrefix string, progress func(percent int)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	cli := &http.Client{Timeout: 20 * time.Minute}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	tmp, err := os.CreateTemp("", "neko-sdk-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	var downloaded int64
	buf := make([]byte, 64*1024)
	lastPct := -1
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				tmp.Close()
				return werr
			}
			downloaded += int64(n)
			if resp.ContentLength > 0 && progress != nil {
				pct := int(downloaded * 100 / resp.ContentLength)
				if pct > 100 {
					pct = 100
				}
				if pct != lastPct {
					lastPct = pct
					progress(pct)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			return readErr
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return extractZip(tmpPath, destDir, stripPrefix)
}

func extractZip(zipPath, destDir, stripPrefix string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	cleanDest := filepath.Clean(destDir)
	if err := os.MkdirAll(cleanDest, 0o755); err != nil {
		return err
	}

	for _, f := range r.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if stripPrefix != "" {
			if !strings.HasPrefix(name, stripPrefix) {
				continue
			}
			name = strings.TrimPrefix(name, stripPrefix)
		}
		name = strings.TrimLeft(name, "/")
		if name == "" {
			continue
		}
		target := filepath.Join(cleanDest, filepath.FromSlash(name))
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanDest+string(os.PathSeparator)) && filepath.Clean(target) != cleanDest {
			return fmt.Errorf("illegal zip path: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeOutErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	return nil
}
