// Package provision acquires prerequisites for first-run app provisioning.
package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tapTapURL = "https://d.taptap.cn/latest"
const maxTapTapBytes = 512 << 20

type DownloadResult struct {
	Bytes  int64
	SHA256 string
}

// DownloadTapTap streams the Android APK from TapTap's published download
// endpoint. The caller owns the destination and must discard it on error.
// The digest is useful for identifying the download; it is not a signature.
func DownloadTapTap(ctx context.Context, destination io.Writer) (DownloadResult, error) {
	client := &http.Client{Timeout: 3 * time.Minute}
	return downloadTapTap(ctx, destination, client, tapTapURL, map[string]bool{
		"d.taptap.cn": true, "rls.tapurl.com": true,
	}, maxTapTapBytes)
}

func trustedDownloadURL(u *url.URL, hosts map[string]bool) bool {
	return u != nil && u.Scheme == "https" && u.User == nil && hosts[strings.ToLower(u.Host)]
}

func downloadTapTap(ctx context.Context, destination io.Writer, client *http.Client, source string, hosts map[string]bool, maxBytes int64) (DownloadResult, error) {
	if destination == nil || client == nil || maxBytes < 4 {
		return DownloadResult{}, errors.New("TapTap 下载配置无效")
	}
	u, err := url.Parse(source)
	if err != nil || !trustedDownloadURL(u, hosts) {
		return DownloadResult{}, errors.New("TapTap 下载地址不受信任")
	}
	// Copy the client so redirect policy cannot mutate a shared caller's client.
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !trustedDownloadURL(req.URL, hosts) {
			return errors.New("TapTap 下载重定向不受信任")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return DownloadResult{}, errors.New("无法创建 TapTap 下载请求")
	}
	resp, err := c.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err // Do not expose signed redirect URLs in logs.
		}
		return DownloadResult{}, fmt.Errorf("TapTap 下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DownloadResult{}, fmt.Errorf("TapTap 下载失败：HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return DownloadResult{}, errors.New("TapTap 安装包超过大小限制")
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	var header [4]byte
	if _, err := io.ReadFull(limited, header[:]); err != nil || string(header[:]) != "PK\x03\x04" {
		return DownloadResult{}, errors.New("TapTap 下载结果不是 APK 文件")
	}
	h := sha256.New()
	w := io.MultiWriter(destination, h)
	if _, err := w.Write(header[:]); err != nil {
		return DownloadResult{}, fmt.Errorf("保存 TapTap 安装包失败：%w", err)
	}
	n, err := io.Copy(w, limited)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("保存 TapTap 安装包失败：%w", err)
	}
	n += int64(len(header))
	if n > maxBytes {
		return DownloadResult{}, errors.New("TapTap 安装包超过大小限制")
	}
	return DownloadResult{Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}
