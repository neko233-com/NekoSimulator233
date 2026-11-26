//go:build windows

package avd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// InstanceSettings mirrors MuMu 6.8's complete per-instance customer_config +
// shell_config + vm_config settings, stored atomically as neko-settings.json.
type InstanceSettings struct {
	PerformanceMode     string  `json:"performance_mode,omitempty"`      // low, middle, high, custom
	ResolutionMode      string  `json:"resolution_mode,omitempty"`       // phone, tablet, widescreen, custom
	FPSLimit            int     `json:"fps_limit,omitempty"`             // 15, 30, 60, 120, 240
	VSync               bool    `json:"vsync,omitempty"`
	ShowFPS             bool    `json:"show_fps,omitempty"`
	DynamicFPS          bool    `json:"dynamic_fps,omitempty"`
	RendererMode        string  `json:"renderer_mode,omitempty"`         // vk, dx
	ForceDedicatedGPU   bool    `json:"force_dedicated_gpu,omitempty"`
	VideoMemoryStrategy string  `json:"video_memory_strategy,omitempty"` // auto, perf, balanced
	RootMode            bool    `json:"root_mode,omitempty"`
	ADBDebugMode        string  `json:"adb_debug_mode,omitempty"`        // local_connect, closed
	SystemDiskReadOnly  bool    `json:"system_disk_readonly,omitempty"`
	AutoRotate          bool    `json:"auto_rotate,omitempty"`
	WindowSizeFixed     bool    `json:"window_size_fixed,omitempty"`
	WindowSaveRect      bool    `json:"window_save_rect,omitempty"`
	WindowMinimizedMute bool    `json:"window_minimized_mute,omitempty"`
	QuitConfirm         bool    `json:"quit_confirm,omitempty"`
	RightClickBack      bool    `json:"right_click_back,omitempty"`
	KeepAlive           bool    `json:"keep_alive,omitempty"`
	Brightness          int     `json:"brightness,omitempty"`            // 1..100
	GPSLat              float64 `json:"gps_lat,omitempty"`
	GPSLon              float64 `json:"gps_lon,omitempty"`
	GPSAlt              float64 `json:"gps_alt,omitempty"`
	MACAddress          string  `json:"mac_address,omitempty"`
	AndroidID           string  `json:"android_id,omitempty"`
}

// DefaultInstanceSettings returns MuMu-aligned sensible defaults (ad-free).
func DefaultInstanceSettings() InstanceSettings {
	return InstanceSettings{
		PerformanceMode:     "middle",
		ResolutionMode:      "phone",
		FPSLimit:            60,
		VSync:               false,
		ShowFPS:             false,
		DynamicFPS:          false,
		RendererMode:        "vk",
		ForceDedicatedGPU:   true,
		VideoMemoryStrategy: "auto",
		RootMode:            true,
		ADBDebugMode:        "local_connect",
		SystemDiskReadOnly:  true,
		AutoRotate:          true,
		WindowSizeFixed:     false,
		WindowSaveRect:      true,
		WindowMinimizedMute: false,
		QuitConfirm:         false,
		RightClickBack:      true,
		KeepAlive:           false,
		Brightness:          80,
		GPSLat:              31.2304,
		GPSLon:              121.4737,
		GPSAlt:              10,
	}
}

// PerformancePresetResources maps MuMu 6.8 performance tiers to CPU/RAM values.
func PerformancePresetResources(mode string) (Resources, bool) {
	switch mode {
	case "low":
		return Resources{Cores: 1, RAMMiB: 1024}, true
	case "middle":
		return Resources{Cores: 4, RAMMiB: 6144}, true
	case "high":
		return Resources{Cores: 6, RAMMiB: 12288}, true
	default:
		return Resources{}, false
	}
}

// DisplayPresetSettings maps MuMu display presets to width/height/DPI.
func DisplayPresetSettings(mode string) (DisplaySettings, bool) {
	switch mode {
	case "phone":
		return DisplaySettings{Width: 1080, Height: 1920, Density: 480}, true
	case "tablet":
		return DisplaySettings{Width: 1920, Height: 1080, Density: 280}, true
	case "widescreen":
		return DisplaySettings{Width: 2560, Height: 1440, Density: 360}, true
	default:
		return DisplaySettings{}, false
	}
}

// PhonePreset defines a common device identity template (MuMu phone.mode.preset parity).
type PhonePreset struct {
	Label        string `json:"label"`
	Brand        string `json:"brand"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Device       string `json:"device"`
}

func PhonePresets() []PhonePreset {
	return []PhonePreset{
		{Label: "HONOR Magic6 Pro", Brand: "HONOR", Manufacturer: "HONOR", Model: "Magic6 Pro", Device: "BVL-AN16"},
		{Label: "Xiaomi 15 Pro", Brand: "Xiaomi", Manufacturer: "Xiaomi", Model: "24101PNB7C", Device: "haotian"},
		{Label: "Samsung Galaxy S25 Ultra", Brand: "samsung", Manufacturer: "samsung", Model: "SM-S9380", Device: "pa3q"},
		{Label: "OnePlus 13", Brand: "OnePlus", Manufacturer: "OnePlus", Model: "PJZ110", Device: "dodge"},
		{Label: "ASUS ROG Phone 9", Brand: "asus", Manufacturer: "asus", Model: "ASUS_AI2501", Device: "AI2501"},
		{Label: "Google Pixel 9 Pro", Brand: "google", Manufacturer: "Google", Model: "Pixel 9 Pro", Device: "caiman"},
	}
}

// RandomPhoneIdentity generates a realistic device identity with valid Luhn IMEI and UUID v4 VDID.
func RandomPhoneIdentity(preset *PhonePreset) PhoneIdentity {
	presets := PhonePresets()
	chosen := presets[0]
	if preset != nil && preset.Model != "" {
		chosen = *preset
	} else {
		var b [1]byte
		_, _ = rand.Read(b[:])
		chosen = presets[int(b[0])%len(presets)]
	}
	return PhoneIdentity{
		Brand:        chosen.Brand,
		Manufacturer: chosen.Manufacturer,
		Model:        chosen.Model,
		Device:       chosen.Device,
		IMEI:         randomIMEI(),
		VDID:         randomUUID(),
	}
}

func randomIMEI() string {
	var raw [14]byte
	_, _ = rand.Read(raw[:])
	digits := make([]int, 14)
	digits[0], digits[1] = 8, 6
	for i := 2; i < 14; i++ {
		digits[i] = int(raw[i]) % 10
	}
	sum := 0
	for i := 0; i < 14; i++ {
		d := digits[i]
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	check := (10 - (sum % 10)) % 10
	var sb strings.Builder
	for _, d := range digits {
		sb.WriteByte(byte('0' + d))
	}
	sb.WriteByte(byte('0' + check))
	return sb.String()
}

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RandomMACAddress generates a locally administered unicast MAC address.
func RandomMACAddress() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	b[0] = (b[0] | 0x02) & 0xfe
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[0], b[1], b[2], b[3], b[4], b[5])
}

// RandomAndroidID generates a 16-char lowercase hex Android ID.
func RandomAndroidID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

var (
	macPattern       = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)
	androidIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{16}$`)
)

func validateInstanceSettings(s InstanceSettings) error {
	if s.PerformanceMode != "" && s.PerformanceMode != "low" && s.PerformanceMode != "middle" && s.PerformanceMode != "high" && s.PerformanceMode != "custom" {
		return errors.New("性能档位无效")
	}
	if s.ResolutionMode != "" && s.ResolutionMode != "phone" && s.ResolutionMode != "tablet" && s.ResolutionMode != "widescreen" && s.ResolutionMode != "custom" {
		return errors.New("分辨率预设模式无效")
	}
	if s.FPSLimit != 0 && s.FPSLimit != 15 && s.FPSLimit != 30 && s.FPSLimit != 60 && s.FPSLimit != 120 && s.FPSLimit != 240 {
		return errors.New("帧率上限必须为 15/30/60/120/240")
	}
	if s.RendererMode != "" && s.RendererMode != "vk" && s.RendererMode != "dx" {
		return errors.New("渲染模式必须为 vk 或 dx")
	}
	if s.Brightness < 0 || s.Brightness > 100 {
		return errors.New("屏幕亮度必须在 0 到 100 之间")
	}
	if s.GPSLat < -90 || s.GPSLat > 90 || s.GPSLon < -180 || s.GPSLon > 180 || math.IsNaN(s.GPSLat) || math.IsNaN(s.GPSLon) {
		return errors.New("GPS 经纬度超出有效范围")
	}
	if s.MACAddress != "" && !macPattern.MatchString(s.MACAddress) {
		return errors.New("MAC 地址格式无效（例如 02:11:22:33:44:55）")
	}
	if s.AndroidID != "" && !androidIDPattern.MatchString(s.AndroidID) {
		return errors.New("Android ID 必须为 16 位十六进制字符")
	}
	return nil
}

func settingsPath(m Manager) (string, error) {
	if err := m.validate(); err != nil {
		return "", err
	}
	return filepath.Join(m.Home, m.Name+".avd", "neko-settings.json"), nil
}

// InstanceSettings reads per-instance MuMu-parity settings, returning defaults if unset.
func (m Manager) InstanceSettings() (InstanceSettings, error) {
	defaults := DefaultInstanceSettings()
	path, err := settingsPath(m)
	if err != nil {
		return defaults, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	if len(b) > 64<<10 {
		return defaults, errors.New("实例设置文件过大")
	}
	cfg := defaults
	if err := json.Unmarshal(b, &cfg); err != nil {
		return defaults, err
	}
	if err := validateInstanceSettings(cfg); err != nil {
		return defaults, err
	}
	return cfg, nil
}

// SetInstanceSettings atomically persists per-instance MuMu-parity settings.
func (m Manager) SetInstanceSettings(s InstanceSettings) error {
	if err := validateInstanceSettings(s); err != nil {
		return err
	}
	path, err := settingsPath(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// LocalAVDHome returns the project-owned AVD root regardless of whether an
// Android SDK is installed yet, allowing offline instance inspection.
func LocalAVDHome() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("未找到 LOCALAPPDATA")
	}
	return filepath.Join(local, "NekoSimulator233-data", "avd"), nil
}

// DiscoverManagerOrOffline discovers the Android SDK manager, or returns a
// degraded local Manager (SDK == "") so the Entry Manager can still inspect
// and manage local instances before SDK installation.
func DiscoverManagerOrOffline() (Manager, error) {
	m, err := DiscoverManager()
	if err == nil {
		return m, nil
	}
	home, homeErr := LocalAVDHome()
	if homeErr != nil {
		return Manager{}, err
	}
	return Manager{SDK: "", Home: home, Name: defaultName}, err
}

// ListLocalInstances lists instances from Home even when SDK is empty.
func ListLocalInstances(ctx context.Context, home string) ([]InstanceSummary, error) {
	if home == "" {
		var err error
		home, err = LocalAVDHome()
		if err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(home); errors.Is(err, os.ErrNotExist) {
		return []InstanceSummary{{Name: DefaultName, State: "not_created", API: 35, Cores: 2, RAMMiB: 4096, Width: 1080, Height: 1920, Density: 480}}, nil
	}
	m := Manager{SDK: "offline", Home: home, Name: DefaultName}
	return m.ListInstances(ctx)
}

// CloneInstance duplicates a stopped instance into a new fresh instance name
// (MuMuManager clone parity). Running or existing destinations are rejected.
func (m Manager) CloneInstance(ctx context.Context, srcName, dstName string) error {
	src, err := m.WithName(srcName)
	if err != nil {
		return err
	}
	dst, err := m.WithName(dstName)
	if err != nil {
		return err
	}
	if strings.EqualFold(src.Name, dst.Name) {
		return errors.New("目标实例名称不能与源实例相同")
	}
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := src.discover(); err == nil {
		return errors.New("源实例正在运行，请先关闭后再克隆")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	if err := dst.checkFreshInstance(dst.Name); err != nil {
		return err
	}
	srcDir, err := ownedAVD(src)
	if err != nil {
		return err
	}
	dstDir := filepath.Join(m.Home, dst.Name+".avd")
	dstINI := filepath.Join(m.Home, dst.Name+".ini")
	if err := copyDirClean(ctx, srcDir, dstDir); err != nil {
		_ = os.RemoveAll(dstDir)
		return fmt.Errorf("克隆实例文件失败：%w", err)
	}
	if err := rewriteAVDIdentity(dstDir, dstINI, dst.Name); err != nil {
		_ = os.RemoveAll(dstDir)
		_ = os.Remove(dstINI)
		return fmt.Errorf("更新克隆实例配置失败：%w", err)
	}
	return nil
}

// RenameInstance renames a stopped instance atomically (MuMuManager rename parity).
func (m Manager) RenameInstance(ctx context.Context, oldName, newName string) error {
	src, err := m.WithName(oldName)
	if err != nil {
		return err
	}
	dst, err := m.WithName(newName)
	if err != nil {
		return err
	}
	if strings.EqualFold(src.Name, dst.Name) {
		return errors.New("新旧实例名称相同")
	}
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := src.discover(); err == nil {
		return errors.New("实例正在运行，请先关闭后再重命名")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	if err := dst.checkFreshInstance(dst.Name); err != nil {
		return err
	}
	srcDir, err := ownedAVD(src)
	if err != nil {
		return err
	}
	srcINI := filepath.Join(m.Home, src.Name+".ini")
	dstDir := filepath.Join(m.Home, dst.Name+".avd")
	dstINI := filepath.Join(m.Home, dst.Name+".ini")
	if err := os.Rename(srcDir, dstDir); err != nil {
		return fmt.Errorf("重命名实例目录失败：%w", err)
	}
	if err := rewriteAVDIdentity(dstDir, dstINI, dst.Name); err != nil {
		_ = os.Rename(dstDir, srcDir)
		return fmt.Errorf("更新实例配置失败：%w", err)
	}
	_ = os.Remove(srcINI)
	return nil
}

// DeleteInstance permanently removes a stopped instance (MuMuManager delete parity).
func (m Manager) DeleteInstance(ctx context.Context, name string) error {
	target, err := m.WithName(name)
	if err != nil {
		return err
	}
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := target.discover(); err == nil {
		return errors.New("实例正在运行，请先关闭后再删除")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	dir := filepath.Join(m.Home, target.Name+".avd")
	ini := filepath.Join(m.Home, target.Name+".ini")
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除实例目录失败：%w", err)
	}
	if err := os.Remove(ini); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除实例索引失败：%w", err)
	}
	return nil
}

// RestartInstance stops and restarts the selected instance cleanly.
func (m Manager) RestartInstance(ctx context.Context) (Instance, error) {
	stopCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	_ = m.Stop(stopCtx)
	cancel()
	return m.Start(ctx)
}

func copyDirClean(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("拒绝复制符号链接")
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		// Skip transient lock files.
		if strings.HasSuffix(strings.ToLower(d.Name()), ".lock") {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func rewriteAVDIdentity(avdDir, iniPath, name string) error {
	configPath := filepath.Join(avdDir, "config.ini")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	api := 35
	if parsed, err := parseINI(configPath); err == nil {
		if detected, _, err := systemImageIdentity(parsed); err == nil && detected > 0 {
			api = detected
		}
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "AvdId":
			lines[i] = "AvdId=" + name
		case "avd.ini.displayname":
			lines[i] = "avd.ini.displayname=" + name
		}
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	absDir, err := filepath.Abs(avdDir)
	if err != nil {
		return err
	}
	iniContent := fmt.Sprintf("avd.ini.encoding=UTF-8\npath=%s\ntarget=android-%d\n", absDir, api)
	return os.WriteFile(iniPath, []byte(iniContent), 0o600)
}

// DiskUsageInfo reports disk space used by an instance directory.
type DiskUsageInfo struct {
	Name      string `json:"name"`
	Bytes     int64  `json:"bytes"`
	Files     int    `json:"files"`
	Formatted string `json:"formatted"`
}

func (m Manager) DiskUsage() (DiskUsageInfo, error) {
	if err := m.validate(); err != nil {
		return DiskUsageInfo{}, err
	}
	dir := filepath.Join(m.Home, m.Name+".avd")
	info := DiskUsageInfo{Name: m.Name}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		info.Formatted = "0 B"
		return info, nil
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, statErr := d.Info(); statErr == nil {
			info.Bytes += fi.Size()
			info.Files++
		}
		return nil
	})
	info.Formatted = FormatBytes(info.Bytes)
	return info, err
}

func FormatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// TrimDisk frees unused guest caches and runs fstrim inside a running instance.
func (m Manager) TrimDisk(ctx context.Context, instance Instance) error {
	_, _ = m.adb(ctx, instance.Serial, "shell", "pm", "trim-caches", "1G")
	_, err := m.adb(ctx, instance.Serial, "shell", "sm", "fstrim")
	return err
}

// RunningApp represents an active or recent Android app shown in the Figure 2
// Chrome-style top tab strip (MuMu Vcontrol::getCurrentAppSync parity).
type RunningApp struct {
	Package    string `json:"package"`
	Label      string `json:"label"`
	Foreground bool   `json:"foreground"`
}

var (
	reFocusedApp   = regexp.MustCompile(`mCurrentFocus=Window\{[^\}]*\s+([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)+)/`)
	reResumedApp   = regexp.MustCompile(`mResumedActivity:.*?\s+([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)+)/`)
	reRecentTask   = regexp.MustCompile(`A=\d+:([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)+)`)
	reRecentComp   = regexp.MustCompile(`realActivity=([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)+)/`)
)

func isLauncherOrSystemPkg(pkg string) bool {
	switch pkg {
	case "com.neko233.launcher",
		"com.android.systemui",
		"com.android.launcher3",
		"com.google.android.apps.nexuslauncher",
		"com.android.permissioncontroller",
		"android":
		return true
	}
	return strings.HasPrefix(pkg, "com.android.internal.")
}

func shortAppLabel(pkg string) string {
	known := map[string]string{
		"com.android.settings":           "设置",
		"com.android.chrome":             "Chrome",
		"com.android.vending":            "Google Play",
		"com.taptap":                     "TapTap",
		"com.tencent.mm":                 "微信",
		"com.ss.android.ugc.aweme":       "抖音",
		"com.android.documentsui":        "文件管理",
		"com.google.android.apps.photos": "相册",
		"com.android.gallery3d":          "图库",
	}
	if label, ok := known[pkg]; ok {
		return label
	}
	parts := strings.Split(pkg, ".")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if len(last) > 0 {
			return strings.ToUpper(last[:1]) + last[1:]
		}
	}
	return pkg
}

// ParseRunningApps extracts the foreground & recent non-launcher user apps from
// dumpsys activity/window output so the Emulator top tab bar mirrors MuMu.
func ParseRunningApps(windowDump, recentsDump string) []RunningApp {
	fg := ""
	if m := reFocusedApp.FindStringSubmatch(windowDump); len(m) == 2 {
		fg = m[1]
	} else if m := reResumedApp.FindStringSubmatch(recentsDump); len(m) == 2 {
		fg = m[1]
	}
	seen := map[string]bool{}
	var apps []RunningApp
	add := func(pkg string, isFg bool) {
		if pkg == "" || isLauncherOrSystemPkg(pkg) || strings.Contains(strings.ToLower(pkg), "fallbackhome") || seen[pkg] {
			return
		}
		seen[pkg] = true
		apps = append(apps, RunningApp{Package: pkg, Label: shortAppLabel(pkg), Foreground: isFg})
	}
	if fg != "" && !isLauncherOrSystemPkg(fg) {
		add(fg, true)
	}
	for _, re := range []*regexp.Regexp{reRecentTask, reRecentComp} {
		for _, match := range re.FindAllStringSubmatch(recentsDump, -1) {
			if len(match) == 2 {
				add(match[1], match[1] == fg)
			}
			if len(apps) >= 8 {
				break
			}
		}
	}
	return apps
}

// ListRunningApps queries the guest for open/recent apps shown as Emulator tabs.
func (m Manager) ListRunningApps(ctx context.Context, instance Instance) ([]RunningApp, error) {
	winOut, _ := m.adb(ctx, instance.Serial, "shell", "dumpsys", "window", "displays")
	recOut, err := m.adb(ctx, instance.Serial, "shell", "dumpsys", "activity", "recents")
	if err != nil && len(winOut) == 0 {
		return nil, err
	}
	return ParseRunningApps(string(winOut), string(recOut)), nil
}

// CloseApp stops a running Android application (MuMu tab close button / Vcontrol::closeTaskAsync).
func (m Manager) CloseApp(ctx context.Context, instance Instance, pkg string) error {
	if !packageName.MatchString(pkg) {
		return errors.New("Android 包名无效")
	}
	_, err := m.adb(ctx, instance.Serial, "shell", "am", "force-stop", pkg)
	return err
}

// ClearAppData wipes a package's guest data (Vcontrol::clearAppData).
func (m Manager) ClearAppData(ctx context.Context, instance Instance, pkg string) error {
	if !packageName.MatchString(pkg) {
		return errors.New("Android 包名无效")
	}
	_, err := m.adb(ctx, instance.Serial, "shell", "pm", "clear", pkg)
	return err
}

// UninstallApp uninstalls a user package from the guest.
func (m Manager) UninstallApp(ctx context.Context, instance Instance, pkg string) error {
	if !packageName.MatchString(pkg) {
		return errors.New("Android 包名无效")
	}
	_, err := m.adb(ctx, instance.Serial, "uninstall", pkg)
	return err
}

// SetGPSLocation sets the simulated GPS coordinates (MuMu tool location parity).
func (m Manager) SetGPSLocation(ctx context.Context, instance Instance, lat, lon, alt float64) error {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 || math.IsNaN(lat) || math.IsNaN(lon) {
		return errors.New("GPS 经纬度超出有效范围")
	}
	// Android emulator console expects longitude first, then latitude, then optional altitude.
	_, err := m.adb(ctx, instance.Serial, "emu", "geo", "fix",
		strconv.FormatFloat(lon, 'f', 6, 64),
		strconv.FormatFloat(lat, 'f', 6, 64),
		strconv.FormatFloat(alt, 'f', 1, 64))
	return err
}

// SetAccelerometer injects gravity/accelerometer vector (MuMu tool gyro parity).
func (m Manager) SetAccelerometer(ctx context.Context, instance Instance, x, y, z float64) error {
	val := fmt.Sprintf("%.2f:%.2f:%.2f", x, y, z)
	_, err := m.adb(ctx, instance.Serial, "emu", "sensor", "set", "acceleration", val)
	return err
}

// Shake triggers a realistic device shake sequence on the accelerometer (MuMu tool shake parity).
func (m Manager) Shake(ctx context.Context, instance Instance) error {
	steps := [][3]float64{
		{18.0, 9.81, 2.0},
		{-18.0, 9.81, -2.0},
		{16.0, 9.81, 1.5},
		{-16.0, 9.81, -1.5},
		{0.0, 9.81, 0.0},
	}
	for _, s := range steps {
		if err := m.SetAccelerometer(ctx, instance, s[0], s[1], s[2]); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(90 * time.Millisecond):
		}
	}
	return nil
}

// RotateDisplay rotates the guest display between portrait (0) and landscape (1)
// (MuMu tool rotate parity).
func (m Manager) RotateDisplay(ctx context.Context, instance Instance, landscape bool) error {
	_, _ = m.adb(ctx, instance.Serial, "shell", "settings", "put", "system", "accelerometer_rotation", "0")
	rot := "0"
	if landscape {
		rot = "1"
	}
	_, err := m.adb(ctx, instance.Serial, "shell", "settings", "put", "system", "user_rotation", rot)
	return err
}

// SetBrightness updates the guest screen brightness from 0..100% (MuMu screen_brightness parity).
func (m Manager) SetBrightness(ctx context.Context, instance Instance, percent int) error {
	if percent < 0 || percent > 100 {
		return errors.New("亮度百分比须为 0..100")
	}
	val := (percent * 255) / 100
	_, _ = m.adb(ctx, instance.Serial, "shell", "settings", "put", "system", "screen_brightness_mode", "0")
	_, err := m.adb(ctx, instance.Serial, "shell", "settings", "put", "system", "screen_brightness", strconv.Itoa(val))
	return err
}

// InstallAdFreeLauncher installs the pure ad-free com.neko233.launcher APK and sets it as default Home.
func (m Manager) InstallAdFreeLauncher(ctx context.Context, instance Instance, apkPath string) error {
	if err := m.InstallAPK(ctx, instance, apkPath, true); err != nil {
		return err
	}
	_, _ = m.adb(ctx, instance.Serial, "shell", "cmd", "package", "set-home-activity", "com.neko233.launcher/.LauncherActivity")
	_, _ = m.adb(ctx, instance.Serial, "shell", "am", "start", "-a", "android.intent.action.MAIN", "-c", "android.intent.category.HOME", "-n", "com.neko233.launcher/.LauncherActivity")
	return nil
}

// CreateDesktopShortcut creates a Windows .lnk shortcut on the user's Desktop
// to launch a specific emulator instance directly (MuMuManager shortcut create parity).
func CreateDesktopShortcut(instanceName, exePath string) (string, error) {
	if !instanceNamePattern.MatchString(instanceName) {
		return "", errors.New("实例名称无效")
	}
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	lnkPath := filepath.Join(desktop, fmt.Sprintf("NekoSimulator233 - %s.lnk", instanceName))
	script := fmt.Sprintf(`$s = (New-Object -ComObject WScript.Shell).CreateShortcut('%s'); $s.TargetPath = '%s'; $s.Arguments = '--launch %s'; $s.WorkingDirectory = '%s'; $s.Description = 'NekoSimulator233 - %s'; $s.Save()`,
		strings.ReplaceAll(lnkPath, "'", "''"),
		strings.ReplaceAll(exePath, "'", "''"),
		strings.ReplaceAll(instanceName, "'", "''"),
		strings.ReplaceAll(filepath.Dir(exePath), "'", "''"),
		strings.ReplaceAll(instanceName, "'", "''"),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("创建桌面快捷方式失败：%s %w", strings.TrimSpace(string(out)), err)
	}
	return lnkPath, nil
}

// GPSPreset provides quick city coordinates for virtual location testing.
type GPSPreset struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

func GPSPresets() []GPSPreset {
	return []GPSPreset{
		{Name: "上海", Lat: 31.2304, Lon: 121.4737},
		{Name: "北京", Lat: 39.9042, Lon: 116.4074},
		{Name: "深圳", Lat: 22.5431, Lon: 114.0579},
		{Name: "广州", Lat: 23.1291, Lon: 113.2644},
		{Name: "东京", Lat: 35.6762, Lon: 139.6503},
		{Name: "旧金山", Lat: 37.7749, Lon: -122.4194},
	}
}

// SortInstalledApps sorts third-party packages alphabetically.
func SortInstalledApps(pkgs []string) []string {
	out := append([]string(nil), pkgs...)
	sort.Strings(out)
	return out
}
