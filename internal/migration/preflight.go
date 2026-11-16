// Package migration reports observed prerequisites without changing instances.
package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/neko233-com/NekoSimulator233/internal/mumu"
)

type Image struct {
	API                    int  `json:"api"`
	SystemImageFilePresent bool `json:"system_image_file_present"`
}
type Report struct {
	SourceIndex    string `json:"source_index"`
	AndroidVersion string `json:"android_version"`
	// False also covers omitted vendor fields and is not proof of shutdown.
	SourceRunningReported      bool     `json:"source_running_reported"`
	SourceAndroidReadyReported bool     `json:"source_android_ready_reported"`
	MatchingImageAPI           int      `json:"matching_image_api,omitempty"`
	MatchingImageFilePresent   bool     `json:"matching_image_file_present"`
	AvailableImages            []Image  `json:"available_images"`
	FullMigrationSupported     bool     `json:"full_migration_supported"`
	Blockers                   []string `json:"blockers"`
}

func ValidateIndex(index string) error {
	if index == "" || len(index) > 10 || (len(index) > 1 && index[0] == '0') {
		return errors.New("MuMu 实例索引无效")
	}
	for _, c := range index {
		if c < '0' || c > '9' {
			return errors.New("MuMu 实例索引无效")
		}
	}
	if _, err := strconv.ParseUint(index, 10, 32); err != nil {
		return errors.New("MuMu 实例索引无效")
	}
	return nil
}

type ListInstances func(context.Context) ([]mumu.Instance, error)

func Check(ctx context.Context, index, sdk string, list ListInstances) (Report, error) {
	if err := ValidateIndex(index); err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if list == nil {
		return Report{}, errors.New("MuMu 实例查询不可用")
	}
	instances, err := list(ctx)
	if err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	for _, source := range instances {
		if source.Index == index {
			return Assess(ctx, source, sdk)
		}
	}
	return Report{}, errors.New("MuMu 实例不存在或状态已变化，请刷新后重试")
}

func ListLocal(ctx context.Context) ([]mumu.Instance, error) {
	root, err := mumu.Discover()
	if err != nil {
		return nil, err
	}
	client, err := mumu.New(root)
	if err != nil {
		return nil, err
	}
	return client.ListContext(ctx)
}

// Assess inspects only the reported source metadata and the fixed SDK image
// paths. It does not open guest disks, launch devices, or establish compatibility.
func Assess(ctx context.Context, source mumu.Instance, sdk string) (Report, error) {
	if err := ValidateIndex(source.Index); err != nil {
		return Report{}, err
	}
	if sdk == "" || !filepath.IsAbs(sdk) {
		return Report{}, errors.New("Android SDK 路径无效")
	}
	r := Report{SourceIndex: source.Index, AndroidVersion: source.AndroidVersion, SourceRunningReported: source.Started, SourceAndroidReadyReported: source.AndroidStarted}
	version := strings.TrimSpace(source.AndroidVersion)
	switch version {
	case "15", "15.0":
		r.MatchingImageAPI = 35
	case "14", "14.0":
		r.MatchingImageAPI = 34
	case "11", "11.0":
		r.MatchingImageAPI = 30
	}
	for _, api := range []int{30, 34, 35} {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		path := filepath.Join(sdk, "system-images", fmt.Sprintf("android-%d", api), "google_apis", "x86_64", "system.img")
		info, err := os.Stat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Report{}, fmt.Errorf("检查 Android %d 镜像失败：%w", api, err)
		}
		installed := err == nil && info.Mode().IsRegular()
		r.AvailableImages = append(r.AvailableImages, Image{API: api, SystemImageFilePresent: installed})
		if api == r.MatchingImageAPI {
			r.MatchingImageFilePresent = installed
		}
	}
	if r.SourceRunningReported {
		r.Blockers = append(r.Blockers, "源实例正在运行，离线数据一致性未确认")
	} else {
		r.Blockers = append(r.Blockers, "未确认源实例停机及其数据一致性")
	}
	if r.MatchingImageAPI == 0 {
		r.Blockers = append(r.Blockers, "无法从厂商版本字符串确定匹配的受支持系统镜像")
	} else if !r.MatchingImageFilePresent {
		r.Blockers = append(r.Blockers, fmt.Sprintf("缺少 Android API %d 的 google_apis x86_64 system.img 文件", r.MatchingImageAPI))
	}
	r.Blockers = append(r.Blockers, "尚未实现 MuMu 数据盘、应用私有数据和共享数据的完整导入；同版本镜像不证明数据兼容")
	return r, nil
}
