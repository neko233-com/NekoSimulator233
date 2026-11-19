//go:build windows

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/keymap"
)

// Keymap tools expose the project-owned keymap files to agents, so a shooter
// binding can be created and verified without opening the GUI editor.

type KeymapRequest struct {
	Profile string `json:"profile,omitempty" jsonschema:"Optional keymap profile name; omit for the default profile"`
}

type KeymapPoint struct {
	X float64 `json:"x" jsonschema:"Normalized horizontal position 0..1 of the guest screen"`
	Y float64 `json:"y" jsonschema:"Normalized vertical position 0..1 of the guest screen"`
}

// KeymapStick uses tagged fields so the MCP SDK generates lowercase JSON keys;
// keymap.Point itself has no tags and would be inferred as X/Y.
type KeymapStick struct {
	Center KeymapPoint `json:"center"`
	Radius float64     `json:"radius"`
}

func (k KeymapStick) internal() *keymap.Joystick {
	return &keymap.Joystick{
		Center: keymap.Point{X: k.Center.X, Y: k.Center.Y},
		Radius: k.Radius,
	}
}

func fromInternal(j *keymap.Joystick) *KeymapStick {
	if j == nil {
		return nil
	}
	return &KeymapStick{
		Center: KeymapPoint{X: j.Center.X, Y: j.Center.Y},
		Radius: j.Radius,
	}
}

type KeymapSetRequest struct {
	Profile  string                 `json:"profile,omitempty" jsonschema:"Optional keymap profile name; omit for the default profile"`
	Keys     map[string]KeymapPoint `json:"keys" jsonschema:"Map from USB HID keyboard usage (decimal string, e.g. 26 for W) to normalized screen point"`
	Joystick *KeymapStick           `json:"joystick,omitempty" jsonschema:"Optional WASD joystick center and radius in normalized coordinates"`
}

type KeymapInfo struct {
	Name     string                 `json:"name"`
	Profile  string                 `json:"profile"`
	Path     string                 `json:"path"`
	Keys     map[string]KeymapPoint `json:"keys"`
	Joystick *KeymapStick           `json:"joystick,omitempty"`
}

type KeymapProfiles struct {
	Profiles []string `json:"profiles"`
}

type KeymapShooterRequest struct {
	Profile  string      `json:"profile,omitempty" jsonschema:"Optional target keymap profile name"`
	Joystick KeymapPoint `json:"joystick" jsonschema:"Joystick center in normalized guest coordinates"`
	Radius   float64     `json:"radius" jsonschema:"Joystick radius in normalized coordinates, 0.04 to 0.25"`
	Fire     KeymapPoint `json:"fire" jsonschema:"Normalized point of the fire button"`
	Scope    KeymapPoint `json:"scope" jsonschema:"Normalized point of the scope button"`
	FireKey  uint16      `json:"fire_key,omitempty" jsonschema:"USB HID usage for fire; defaults to left mouse button usage 1"`
	ScopeKey uint16      `json:"scope_key,omitempty" jsonschema:"USB HID usage for scope; defaults to right mouse button usage 2"`
}

func (s *Service) keymapBase(name string) (string, error) {
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data")
	return keymap.Resolve(root, filepath.Join(root, "avd", name+".avd"), name, avd.DefaultName)
}

func keymapPath(base, profile string) (string, error) {
	if profile == "" || profile == keymap.DefaultProfile {
		return base, nil
	}
	if err := keymap.ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(keymap.ProfilesDir(base), profile+".json"), nil
}

func parseUsage(raw string) (uint16, error) {
	n, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || !display.ValidKeyboardUsage(uint16(n)) {
		return 0, fmt.Errorf("无效的 USB 键盘按键 %q", raw)
	}
	return uint16(n), nil
}

func readKeymapFile(path, name, profile string) (KeymapInfo, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return KeymapInfo{Name: name, Profile: profileOrDefault(profile), Path: path, Keys: map[string]KeymapPoint{}}, nil
	}
	if err != nil {
		return KeymapInfo{}, err
	}
	file, err := keymap.Parse(b)
	if err != nil {
		return KeymapInfo{}, err
	}
	keys := make(map[string]KeymapPoint, len(file.Keys))
	for usage, p := range file.Keys {
		keys[strconv.FormatUint(uint64(usage), 10)] = KeymapPoint{X: p.X, Y: p.Y}
	}
	return KeymapInfo{Name: name, Profile: profileOrDefault(profile), Path: path, Keys: keys, Joystick: fromInternal(file.Joystick)}, nil
}

func profileOrDefault(p string) string {
	if p == "" {
		return keymap.DefaultProfile
	}
	return p
}

func (s *Service) selectedName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected == "" {
		return avd.DefaultName
	}
	return s.selected
}

func (s *Service) keymapGet(ctx context.Context, _ *mcp.CallToolRequest, req KeymapRequest) (*mcp.CallToolResult, KeymapInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, KeymapInfo{}, err
	}
	name := s.selectedName()
	base, err := s.keymapBase(name)
	if err != nil {
		return nil, KeymapInfo{}, err
	}
	path, err := keymapPath(base, req.Profile)
	if err != nil {
		return nil, KeymapInfo{}, err
	}
	info, err := readKeymapFile(path, name, req.Profile)
	return nil, info, err
}

func (s *Service) keymapProfiles(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, KeymapProfiles, error) {
	if err := ctx.Err(); err != nil {
		return nil, KeymapProfiles{}, err
	}
	base, err := s.keymapBase(s.selectedName())
	if err != nil {
		return nil, KeymapProfiles{}, err
	}
	dir := keymap.ProfilesDir(base)
	profiles := []string{keymap.DefaultProfile}
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			name := entry.Name()[:len(entry.Name())-len(".json")]
			if keymap.ValidateProfileName(name) == nil {
				profiles = append(profiles, name)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, KeymapProfiles{}, err
	}
	sort.Slice(profiles[1:], func(i, j int) bool { return profiles[i+1] < profiles[j+1] })
	return nil, KeymapProfiles{Profiles: profiles}, nil
}

func writeKeymap(path string, file keymap.File) error {
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	// Reuse the project's parser so every write is validated identically.
	if _, err := keymap.Parse(data); err != nil {
		return err
	}
	unlock, err := keymap.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".keymap-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *Service) keymapSet(ctx context.Context, _ *mcp.CallToolRequest, req KeymapSetRequest) (*mcp.CallToolResult, Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, Result{}, err
	}
	name := s.selectedName()
	base, err := s.keymapBase(name)
	if err != nil {
		return nil, Result{}, err
	}
	path, err := keymapPath(base, req.Profile)
	if err != nil {
		return nil, Result{}, err
	}
	keys := make(map[uint16]keymap.Point, len(req.Keys))
	for raw, p := range req.Keys {
		usage, err := parseUsage(raw)
		if err != nil {
			return nil, Result{}, err
		}
		keys[usage] = keymap.Point{X: p.X, Y: p.Y}
	}
	var stick *keymap.Joystick
	if req.Joystick != nil {
		stick = req.Joystick.internal()
	}
	file := keymap.File{Version: 2, Keys: keys, Joystick: stick}
	if err := writeKeymap(path, file); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) keymapShooter(ctx context.Context, _ *mcp.CallToolRequest, req KeymapShooterRequest) (*mcp.CallToolResult, Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, Result{}, err
	}
	if req.Radius < 0.04 || req.Radius > 0.25 {
		return nil, Result{}, errors.New("摇杆半径必须在 0.04 到 0.25 之间")
	}
	fireKey := req.FireKey
	if fireKey == 0 {
		fireKey = 40 // valid keyboard usage, not part of WASD
	}
	scopeKey := req.ScopeKey
	if scopeKey == 0 {
		scopeKey = 44
	}
	if keymap.JoystickKey(fireKey) || keymap.JoystickKey(scopeKey) {
		return nil, Result{}, errors.New("开火或瞄准键不能占用 WASD 摇杆")
	}
	_, info, err := s.keymapGet(ctx, nil, KeymapRequest{Profile: req.Profile})
	if err != nil {
		return nil, Result{}, err
	}
	keys := map[string]KeymapPoint{}
	// Keep existing non-conflicting single bindings when applying a template.
	for raw, p := range info.Keys {
		usage, err := parseUsage(raw)
		if err != nil || keymap.JoystickKey(usage) {
			continue
		}
		keys[raw] = p
	}
	keys[strconv.FormatUint(uint64(fireKey), 10)] = req.Fire
	keys[strconv.FormatUint(uint64(scopeKey), 10)] = req.Scope
	setReq := KeymapSetRequest{
		Profile: req.Profile,
		Keys:    keys,
		Joystick: &KeymapStick{
			Center: KeymapPoint{X: req.Joystick.X, Y: req.Joystick.Y},
			Radius: req.Radius,
		},
	}
	return s.keymapSet(ctx, nil, setReq)
}
