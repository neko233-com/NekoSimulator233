//go:build windows

package avd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var instanceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// WithName selects an AVD in the project data directory; it never creates it.
func (m Manager) WithName(name string) (Manager, error) {
	if !instanceNamePattern.MatchString(name) || strings.HasSuffix(name, ".") || !filepath.IsLocal(name) {
		return Manager{}, errors.New("实例名称只能使用英文字母、数字、点、下划线和连字符，且不能使用 Windows 保留名称")
	}
	m.Name = name
	if err := m.validate(); err != nil {
		return Manager{}, err
	}
	return m, nil
}

type InstanceSummary struct {
	Name     string `json:"name"`
	State    string `json:"state" jsonschema:"not_created, stopped, running or unknown"`
	API      int    `json:"api,omitempty" jsonschema:"Android system image API level, when recognized"`
	ImageTag string `json:"image_tag,omitempty" jsonschema:"google_apis_playstore or google_apis when recognized"`
	Reason   string `json:"reason,omitempty"`
}

// ListInstances includes incomplete project directories as unknown instead
// of hiding them or interpreting missing metadata as proof of shutdown.
func (m Manager) ListInstances(ctx context.Context) ([]InstanceSummary, error) {
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	entries, err := os.ReadDir(m.Home)
	if err != nil {
		return nil, err
	}
	names := map[string]string{strings.ToLower(defaultName): defaultName}
	for _, entry := range entries {
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".ini" && ext != ".avd" {
			continue
		}
		name = strings.TrimSuffix(name, filepath.Ext(name))
		if _, err := m.WithName(name); err != nil {
			continue
		}
		key := strings.ToLower(name)
		if prev, ok := names[key]; !ok || prev == defaultName {
			names[key] = name
		}
	}
	results := make([]InstanceSummary, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := m.WithName(name)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(m.Home, name+".avd")
		ini := filepath.Join(m.Home, name+".ini")
		dirPresent, dirErr := existsOrLink(dir)
		iniPresent, iniErr := existsOrLink(ini)
		item := InstanceSummary{Name: name}
		switch {
		case dirErr != nil || iniErr != nil:
			item.State, item.Reason = "unknown", "无法检查实例文件"
		case !dirPresent && !iniPresent:
			item.State = "not_created"
		case !dirPresent || !iniPresent:
			item.State, item.Reason = "unknown", "实例元数据不完整，已保留文件"
		default:
			if _, err := ownedAVD(candidate); err != nil || !exists(filepath.Join(dir, "config.ini")) {
				item.State, item.Reason = "unknown", "实例路径或配置无效，已保留文件"
			} else {
				config, configErr := parseINI(filepath.Join(dir, "config.ini"))
				if configErr == nil {
					item.API, item.ImageTag, configErr = systemImageIdentity(config)
				}
				if configErr != nil {
					item.State, item.Reason = "unknown", "实例镜像配置无效，已保留文件"
				} else if _, err := candidate.discover(); err == nil {
					item.State = "running"
				} else if errors.Is(err, errInstanceNotRunning) {
					item.State = "stopped"
				} else {
					item.State, item.Reason = "unknown", err.Error()
				}
			}
		}
		results = append(results, item)
	}
	sort.Slice(results, func(i, j int) bool { return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name) })
	return results, nil
}

// CreateInstance creates only fresh metadata and never overwrites or repairs
// preexisting paths, including incomplete directories and links.
func (m Manager) CreateInstance(ctx context.Context, name string) error {
	return m.CreateInstanceAPI(ctx, name, 0)
}

// CreateInstanceAPI selects an installed image for a fresh instance. API 0
// selects the default; it never changes the system image of an existing AVD.
func (m Manager) CreateInstanceAPI(ctx context.Context, name string, api int) error {
	if api != 0 && api != 30 && api != 34 && api != 35 {
		return errors.New("不支持的 Android 系统版本")
	}
	candidate, err := m.WithName(name)
	if err != nil {
		return err
	}
	unlock, err := candidate.lockLifecycle(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	for _, path := range []string{filepath.Join(m.Home, name+".ini"), filepath.Join(m.Home, name+".avd")} {
		present, err := existsOrLink(path)
		if err != nil {
			return err
		}
		if present {
			return fmt.Errorf("实例 %s 已存在或不完整，不会覆盖", name)
		}
	}
	if _, err := candidate.discover(); err == nil {
		return errors.New("同名实例正在运行，未创建")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	return candidate.ensureWithAPI(api)
}
