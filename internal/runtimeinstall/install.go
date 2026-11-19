// Package runtimeinstall downloads and unpacks the Android emulator runtime
// separately from the MSI. The MSI stays small; the runtime is fetched from a
// trusted HTTPS manifest with a SHA-256 and size, then extracted atomically.
package runtimeinstall

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manifest is the separately hosted runtime descriptor. Mirrors may contain
// the placeholder {url} and are tried in order after the primary URL.
type Manifest struct {
	URL     string   `json:"url"`
	SHA256  string   `json:"sha256"`
	Bytes   int64    `json:"bytes,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`
}

// LoadManifest reads a local manifest file or an HTTPS URL.
func LoadManifest(ctx context.Context, source string) (Manifest, error) {
	var data []byte
	var err error
	if strings.HasPrefix(strings.ToLower(source), "https://") {
		data, err = fetch(ctx, source, 64<<10)
	} else {
		data, err = os.ReadFile(source)
	}
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("运行时清单无效: %w", err)
	}
	if !strings.HasPrefix(strings.ToLower(manifest.URL), "https://") {
		return Manifest{}, errors.New("运行时下载地址必须是 HTTPS")
	}
	if len(manifest.SHA256) != 64 {
		return Manifest{}, errors.New("运行时清单缺少 SHA-256")
	}
	if manifest.Bytes < 0 || manifest.Bytes > 8<<30 {
		return Manifest{}, errors.New("运行时清单大小无效")
	}
	return manifest, nil
}

func fetch(ctx context.Context, source string, limit int64) ([]byte, error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return nil, errors.New("清单地址不受信任")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("清单下载失败: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Download verifies and extracts the runtime zip into dest. Existing files are
// never overwritten; a partially extracted directory is removed on error.
func Download(ctx context.Context, manifest Manifest, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "neko-runtime-*.zip")
	if err != nil {
		return err
	}
	zipPath := tmp.Name()
	tmp.Close()
	defer os.Remove(zipPath)
	if err := download(ctx, manifest, zipPath); err != nil {
		return err
	}
	return extract(zipPath, dest)
}

func download(ctx context.Context, manifest Manifest, path string) error {
	return downloadWithClient(ctx, manifest, path, &http.Client{Timeout: 30 * time.Minute})
}

// CandidateURLs returns the primary URL plus configured mirrors. GitHub URLs
// also get automatic mirror prefixes unless NEKO_DISABLE_GITHUB_MIRROR=1; the
// manifest SHA-256 still verifies the final bytes.
func CandidateURLs(manifest Manifest) []string {
	candidates := []string{manifest.URL}
	for _, mirror := range manifest.Mirrors {
		if strings.TrimSpace(mirror) != "" {
			candidates = append(candidates, strings.ReplaceAll(mirror, "{url}", manifest.URL))
		}
	}
	if os.Getenv("NEKO_DISABLE_GITHUB_MIRROR") != "1" && isGitHubURL(manifest.URL) {
		for _, prefix := range []string{"https://ghproxy.net/", "https://gh-proxy.com/", "https://github.moeyy.xyz/"} {
			candidates = append(candidates, prefix+manifest.URL)
		}
	}
	seen := map[string]bool{}
	out := candidates[:0]
	for _, candidate := range candidates {
		if !seen[candidate] {
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

func isGitHubURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return host == "github.com" || host == "objects.githubusercontent.com" || host == "raw.githubusercontent.com"
}

func downloadWithClient(ctx context.Context, manifest Manifest, path string, client *http.Client) error {
	if client == nil {
		return errors.New("缺少 HTTP 客户端")
	}
	var lastErr error
	for _, candidate := range CandidateURLs(manifest) {
		if err := downloadOne(ctx, candidate, manifest, path, client); err == nil {
			return nil
		} else {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的运行时下载地址")
	}
	return lastErr
}

func downloadOne(ctx context.Context, rawURL string, manifest Manifest, path string, client *http.Client) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return errors.New("运行时下载地址不受信任")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("运行时下载失败: HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	limit := manifest.Bytes
	if limit <= 0 {
		limit = 8 << 30
	}
	written, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(resp.Body, limit+1))
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if written > limit {
		return errors.New("运行时下载超过清单大小")
	}
	if manifest.Bytes > 0 && written != manifest.Bytes {
		return fmt.Errorf("运行时大小不匹配: got %d want %d", written, manifest.Bytes)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, manifest.SHA256) {
		return errors.New("运行时 SHA-256 校验失败")
	}
	return nil
}

func extract(zipPath, dest string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	base, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for _, entry := range reader.File {
		name := filepath.Clean(entry.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("运行时包含非法路径: %s", entry.Name)
		}
		target := filepath.Join(base, name)
		if !strings.HasPrefix(target, base+string(os.PathSeparator)) && target != base {
			return fmt.Errorf("运行时路径越界: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if entry.UncompressedSize64 > 2<<30 {
			return fmt.Errorf("运行时条目过大: %s", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		src, err := entry.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, io.LimitReader(src, 2<<30))
		src.Close()
		dst.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
