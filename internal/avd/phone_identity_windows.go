//go:build windows

package avd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PhoneIdentity mirrors MuMu's customer_config.setting.phone block. It is
// stored per instance and injected into emulator startup with -prop.
type PhoneIdentity struct {
	Brand        string `json:"brand,omitempty" jsonschema:"Device brand, e.g. OPPO"`
	Manufacturer string `json:"manufacturer,omitempty" jsonschema:"Device manufacturer"`
	Model        string `json:"model,omitempty" jsonschema:"Device model shown in settings"`
	Device       string `json:"device,omitempty" jsonschema:"ro.product.device value"`
	IMEI         string `json:"imei,omitempty" jsonschema:"15-digit IMEI; only used when a compatible radio property is accepted"`
	VDID         string `json:"vdid,omitempty" jsonschema:"Virtual device identifier"`
}

var phoneToken = regexp.MustCompile(`^[A-Za-z0-9_. -]{1,64}$`)

func phoneIdentityPath(m Manager) (string, error) {
	if err := m.validate(); err != nil {
		return "", err
	}
	return filepath.Join(m.Home, m.Name+".avd", "neko-phone.json"), nil
}

func validatePhoneIdentity(p PhoneIdentity) error {
	for _, value := range []string{p.Brand, p.Manufacturer, p.Model, p.Device, p.VDID} {
		if value != "" && !phoneToken.MatchString(value) {
			return errors.New("手机身份字段包含无效字符")
		}
	}
	if p.IMEI != "" && (len(p.IMEI) != 15 || !regexp.MustCompile(`^[0-9]+$`).MatchString(p.IMEI)) {
		return errors.New("IMEI 必须是 15 位数字")
	}
	return nil
}

// PhoneIdentity returns the configured identity; an absent file means no
// overrides, keeping the emulator's defaults.
func (m Manager) PhoneIdentity() (PhoneIdentity, error) {
	path, err := phoneIdentityPath(m)
	if err != nil {
		return PhoneIdentity{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return PhoneIdentity{}, nil
	}
	if err != nil {
		return PhoneIdentity{}, err
	}
	if len(b) > 4096 {
		return PhoneIdentity{}, errors.New("手机身份文件过大")
	}
	var p PhoneIdentity
	if err := json.Unmarshal(b, &p); err != nil {
		return PhoneIdentity{}, err
	}
	if err := validatePhoneIdentity(p); err != nil {
		return PhoneIdentity{}, err
	}
	return p, nil
}

func (m Manager) SetPhoneIdentity(p PhoneIdentity) error {
	if err := validatePhoneIdentity(p); err != nil {
		return err
	}
	path, err := phoneIdentityPath(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".phone-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
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
	return os.Rename(name, path)
}

// Empty reports whether no overrides were configured.
func (p PhoneIdentity) Empty() bool {
	return strings.TrimSpace(p.Brand) == "" && strings.TrimSpace(p.Manufacturer) == "" &&
		strings.TrimSpace(p.Model) == "" && strings.TrimSpace(p.Device) == "" &&
		strings.TrimSpace(p.IMEI) == "" && strings.TrimSpace(p.VDID) == ""
}

// ApplyPhoneIdentity writes the identity into a running instance's
// /system/build.prop and reboots. It mirrors MuMu's identity injection but
// requires a userdebug system image: production Play images refuse adb root.
func (m Manager) ApplyPhoneIdentity(ctx context.Context, instance Instance) error {
	identity, err := m.PhoneIdentity()
	if err != nil {
		return err
	}
	overrides := map[string]string{}
	for prop, value := range map[string]string{
		"ro.product.brand": identity.Brand, "ro.product.manufacturer": identity.Manufacturer,
		"ro.product.model": identity.Model, "ro.product.device": identity.Device,
		"ro.neko.imei": identity.IMEI, "ro.neko.vdid": identity.VDID,
	} {
		if strings.TrimSpace(value) != "" {
			overrides[prop] = value
		}
	}
	if len(overrides) == 0 {
		return errors.New("手机身份为空，无需应用")
	}
	rootOut, err := m.adb(ctx, instance.Serial, "root")
	// adb root signals success through its exit code; some adbd versions
	// print nothing when already running as root.
	if err != nil {
		return fmt.Errorf("无法获取 ADB root（需要 userdebug 镜像）：%s %v", strings.TrimSpace(string(rootOut)), err)
	}
	// adb root restarts adbd; remount can race with the reconnect.
	_, _ = m.adb(ctx, instance.Serial, "wait-for-device")
	var remountErr error
	for attempt := 0; attempt < 3; attempt++ {
		if _, remountErr = m.adb(ctx, instance.Serial, "remount"); remountErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if remountErr != nil {
		return fmt.Errorf("remount /system 失败：%w", remountErr)
	}
	// The first remount only prepares overlayfs; a reboot is required before
	// /system is actually writable. Re-root/remount after the reboot.
	if _, err := m.adb(ctx, instance.Serial, "reboot"); err != nil {
		return fmt.Errorf("重启以启用可写系统失败：%w", err)
	}
	time.Sleep(20 * time.Second)
	_, _ = m.adb(ctx, instance.Serial, "wait-for-device")
	if _, err := m.adb(ctx, instance.Serial, "root"); err != nil {
		return fmt.Errorf("重启后获取 root 失败：%w", err)
	}
	_, _ = m.adb(ctx, instance.Serial, "wait-for-device")
	for attempt := 0; attempt < 3; attempt++ {
		if _, remountErr = m.adb(ctx, instance.Serial, "remount"); remountErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if remountErr != nil {
		return fmt.Errorf("重启后 remount /system 失败：%w", remountErr)
	}
	tmpDir, err := os.MkdirTemp("", "neko-phone-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	local := filepath.Join(tmpDir, "build.prop")
	if _, err := m.adb(ctx, instance.Serial, "pull", "/system/build.prop", local); err != nil {
		return fmt.Errorf("读取 build.prop 失败：%w", err)
	}
	file, err := os.Open(local)
	if err != nil {
		return err
	}
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		key := strings.SplitN(line, "=", 2)[0]
		if _, replaced := overrides[key]; replaced {
			continue
		}
		lines = append(lines, line)
	}
	scanErr := scanner.Err()
	file.Close()
	if scanErr != nil {
		return scanErr
	}
	lines = append(lines, "")
	for key, value := range overrides {
		lines = append(lines, key+"="+value)
	}
	if err := os.WriteFile(local, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	_, _ = m.adb(ctx, instance.Serial, "shell", "chmod", "0666", "/system/build.prop")
	if _, err := m.adb(ctx, instance.Serial, "push", local, "/system/build.prop"); err != nil {
		return fmt.Errorf("写回 build.prop 失败：%w", err)
	}
	if _, err := m.adb(ctx, instance.Serial, "shell", "chmod", "0644", "/system/build.prop"); err != nil {
		return err
	}
	if _, err := m.adb(ctx, instance.Serial, "reboot"); err != nil {
		return err
	}
	return nil
}

// PropArgs converts the identity into emulator -prop arguments, matching
// MuMu's phone block semantics without touching system images.
func (p PhoneIdentity) PropArgs() []string {
	var args []string
	add := func(prop, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, "-prop", prop+"="+value)
		}
	}
	add("ro.product.brand", p.Brand)
	add("ro.product.manufacturer", p.Manufacturer)
	add("ro.product.model", p.Model)
	add("ro.product.device", p.Device)
	add("ro.neko.imei", p.IMEI)
	add("ro.neko.vdid", p.VDID)
	return args
}
