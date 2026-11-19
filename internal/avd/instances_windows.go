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
	"strconv"
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
	Name        string `json:"name"`
	State       string `json:"state" jsonschema:"not_created, stopped, running or unknown"`
	API         int    `json:"api,omitempty" jsonschema:"Android system image API level, when recognized"`
	ImageTag    string `json:"image_tag,omitempty" jsonschema:"google_apis_playstore or google_apis when recognized"`
	TapTapSetup string `json:"taptap_setup,omitempty" jsonschema:"First-run TapTap task: pending, retry_pending, complete, or unknown"`
	Cores       int    `json:"cores,omitempty" jsonschema:"Configured virtual CPU cores"`
	RAMMiB      int    `json:"ram_mib,omitempty" jsonschema:"Configured guest RAM in MiB"`
	Width       int    `json:"width,omitempty" jsonschema:"Configured guest display width"`
	Height      int    `json:"height,omitempty" jsonschema:"Configured guest display height"`
	Density     int    `json:"density,omitempty" jsonschema:"Configured guest display DPI"`
	Reason      string `json:"reason,omitempty"`
}

// configInt reads a bounded integer from an AVD config, leaving the zero value
// when the key is absent or invalid so the UI can omit unknown details.
func configInt(config map[string]string, key string, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(config[key]))
	if err != nil || n < min || n > max {
		return 0
	}
	return n
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
					item.Cores = configInt(config, "hw.cpu.ncore", 1, 64)
					item.RAMMiB = configInt(config, "hw.ramSize", 512, 131072)
					item.Width = configInt(config, "hw.lcd.width", 320, 8192)
					item.Height = configInt(config, "hw.lcd.height", 320, 8192)
					item.Density = configInt(config, "hw.lcd.density", 72, 1000)
				}
				if configErr != nil {
					item.State, item.Reason = "unknown", "实例镜像配置无效，已保留文件"
				} else {
					setup, setupErr := candidate.TapTapAutoStatus()
					if setupErr != nil {
						item.TapTapSetup = "unknown"
					} else if setup != "not_scheduled" {
						item.TapTapSetup = setup
					}
					if _, err := candidate.discover(); err == nil {
						item.State = "running"
					} else if errors.Is(err, errInstanceNotRunning) {
						item.State = "stopped"
					} else {
						item.State, item.Reason = "unknown", err.Error()
					}
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
	return m.createInstanceWithPolicy(ctx, name, api, false)
}

func (m Manager) createInstanceWithPolicy(ctx context.Context, name string, api int, requirePlay bool) error {
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
	if err := candidate.checkFreshInstance(name); err != nil {
		return err
	}
	return candidate.ensureWithImagePolicy(api, requirePlay)
}

// Caller holds the lifecycle lock. It does not change any existing instance.
func (m Manager) checkFreshInstance(name string) error {
	for _, path := range []string{filepath.Join(m.Home, name+".ini"), filepath.Join(m.Home, name+".avd")} {
		present, err := existsOrLink(path)
		if err != nil {
			return err
		}
		if present {
			return fmt.Errorf("实例 %s 已存在或不完整，不会覆盖", name)
		}
	}
	if _, err := m.discover(); err == nil {
		return errors.New("同名实例正在运行，未创建")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	return nil
}

// CreatePlayInstance ensures a Play image is present before creating new
// metadata. Image downloads occur outside the instance lifecycle lock.
func (m Manager) CreatePlayInstance(ctx context.Context, name string, api int, notify func(int)) error {
	return m.createPlayInstance(ctx, name, api, notify, func(ctx context.Context, api int, notify func(int)) error {
		_, err := m.InstallPlayImage(ctx, api, notify)
		return err
	})
}

func (m Manager) createPlayInstance(ctx context.Context, name string, api int, notify func(int), install func(context.Context, int, func(int)) error) error {
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
	err = candidate.checkFreshInstance(name)
	unlock()
	if err != nil {
		return err
	}
	chosen := api
	if chosen == 0 {
		chosen = 34
		for _, level := range []int{35, 34} {
			if completePlayImage(filepath.Join(m.SDK, "system-images", fmt.Sprintf("android-%d", level), "google_apis_playstore", "x86_64"), level) {
				chosen = level
				break
			}
		}
	}
	dir := filepath.Join(m.SDK, "system-images", fmt.Sprintf("android-%d", chosen), "google_apis_playstore", "x86_64")
	if !completePlayImage(dir, chosen) {
		if err := install(ctx, chosen, notify); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.createInstanceWithPolicy(ctx, name, chosen, true)
}

// prepareStart validates existing metadata or creates a Play-backed instance
// without holding the lifecycle lock across an SDK image download.
func (m Manager) prepareStart(ctx context.Context, notify func(int), install func(context.Context, int, func(int)) error) error {
	check := func() (bool, error) {
		unlock, err := m.lockLifecycle(ctx, true)
		if err != nil {
			return false, err
		}
		defer unlock()
		dir, err := existsOrLink(filepath.Join(m.Home, m.Name+".avd"))
		if err != nil {
			return false, err
		}
		ini, err := existsOrLink(filepath.Join(m.Home, m.Name+".ini"))
		if err != nil {
			return false, err
		}
		if !dir && !ini {
			return false, nil
		}
		if !dir || !ini {
			return false, errors.New("实例配置不完整，已保留现有数据；请检查 AVD 目录")
		}
		if err := m.ensureWithImagePolicy(0, true); err != nil {
			return false, err
		}
		return true, nil
	}
	exists, err := check()
	if err != nil || exists {
		return err
	}
	err = m.createPlayInstance(ctx, m.Name, 0, notify, install)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Another Neko process may have created this instance during the download.
	if exists, checkErr := check(); checkErr == nil && exists {
		return nil
	}
	return err
}
