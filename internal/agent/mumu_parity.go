//go:build windows

package agent

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/display"
	"github.com/neko233-com/NekoSimulator233/internal/macro"
	"github.com/neko233-com/NekoSimulator233/internal/mcpproxy"
)

type CloneRequest struct {
	Source      string `json:"source" jsonschema:"Existing stopped instance name"`
	Destination string `json:"destination" jsonschema:"New instance name"`
}

type RenameRequest struct {
	OldName string `json:"old_name" jsonschema:"Existing stopped instance name"`
	NewName string `json:"new_name" jsonschema:"New instance name"`
}

type GPSRequest struct {
	Lat float64 `json:"lat" jsonschema:"Latitude -90..90"`
	Lon float64 `json:"lon" jsonschema:"Longitude -180..180"`
	Alt float64 `json:"alt,omitempty" jsonschema:"Optional altitude in meters"`
}

type RotateRequest struct {
	Landscape bool `json:"landscape" jsonschema:"true for landscape, false for portrait"`
}

type BrightnessRequest struct {
	Percent int `json:"percent" jsonschema:"Screen brightness percentage 0..100"`
}

type MacroNameRequest struct {
	Name string `json:"name"`
}

type MacroMergeRequest struct {
	Name    string   `json:"name"`
	Sources []string `json:"sources"`
	GapMS   int64    `json:"gap_ms,omitempty"`
}

type MacroReplayRequest struct {
	Name  string  `json:"name"`
	Loops int     `json:"loops,omitempty"`
	Speed float64 `json:"speed,omitempty"`
}

type RandomPhoneRequest struct {
	PresetIndex int  `json:"preset_index,omitempty" jsonschema:"Optional 0-based preset index; -1 or omit for random"`
	Apply       bool `json:"apply,omitempty" jsonschema:"Save immediately for the selected stopped instance"`
}

func registerParityTools(server *mcp.Server, s *Service) {
	ro := func(name, desc string) *mcp.Tool {
		return &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	}
	rw := func(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }

	mcp.AddTool(server, ro("neko_mcp_ecosystem", "Inspect local availability of agent-desktop, rea-mcp, ghidra-mcp, craft-anything, Android SDK and WiX."), s.mcpEcosystem)
	mcp.AddTool(server, rw("neko_clone_instance", "Clone a stopped instance into a new instance name (MuMuManager clone parity)."), s.cloneInstance)
	mcp.AddTool(server, rw("neko_rename_instance", "Rename a stopped instance cleanly (MuMuManager rename parity)."), s.renameInstance)
	mcp.AddTool(server, rw("neko_delete_instance", "Delete a stopped instance permanently (MuMuManager delete parity)."), s.deleteInstance)
	mcp.AddTool(server, rw("neko_restart_instance", "Restart the selected instance cleanly."), s.restartInstance)
	mcp.AddTool(server, ro("neko_get_settings", "Read full MuMu-equivalent per-instance settings (performance tier, FPS limit, VSync, root, brightness, GPS, MAC, Android ID)."), s.getSettings)
	mcp.AddTool(server, rw("neko_set_settings", "Update full MuMu-equivalent per-instance settings."), s.setSettings)
	mcp.AddTool(server, ro("neko_phone_presets", "List built-in MuMu-style device identity presets."), s.phonePresets)
	mcp.AddTool(server, rw("neko_randomize_phone_identity", "Generate a randomized phone identity (brand, model, valid IMEI, VDID) and optionally save it."), s.randomizePhone)
	mcp.AddTool(server, ro("neko_disk_usage", "Inspect disk usage of the selected instance directory."), s.diskUsage)
	mcp.AddTool(server, rw("neko_trim_disk", "Trim caches and run fstrim inside the selected running instance."), s.trimDisk)
	mcp.AddTool(server, ro("neko_running_apps", "List active and recent user apps in the selected running instance (Emulator top tab bar source)."), s.runningApps)
	mcp.AddTool(server, rw("neko_close_app", "Force-stop an app by package name in the selected running instance."), s.closeApp)
	mcp.AddTool(server, rw("neko_clear_app_data", "Clear app data by package name in the selected running instance."), s.clearAppData)
	mcp.AddTool(server, rw("neko_uninstall_app", "Uninstall a third-party app by package name in the selected running instance."), s.uninstallApp)
	mcp.AddTool(server, rw("neko_set_gps", "Set virtual GPS coordinates on the selected running instance."), s.setGPS)
	mcp.AddTool(server, rw("neko_shake", "Trigger a simulated accelerometer shake on the selected running instance."), s.shake)
	mcp.AddTool(server, rw("neko_rotate", "Rotate the selected running instance between portrait and landscape."), s.rotate)
	mcp.AddTool(server, rw("neko_set_brightness", "Set guest screen brightness (0..100%) on the selected running instance."), s.setBrightness)
	mcp.AddTool(server, ro("neko_macro_list", "List saved input macro scripts."), s.macroList)
	mcp.AddTool(server, rw("neko_macro_save", "Save or overwrite a named input macro script."), s.macroSave)
	mcp.AddTool(server, rw("neko_macro_delete", "Delete a saved input macro script."), s.macroDelete)
	mcp.AddTool(server, rw("neko_macro_merge", "Merge multiple saved macro scripts sequentially with an optional gap."), s.macroMerge)
	mcp.AddTool(server, rw("neko_macro_replay", "Replay a saved input macro script against the selected running instance."), s.macroReplay)
}

func (s *Service) mcpEcosystem(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, mcpproxy.EcosystemReport, error) {
	return nil, mcpproxy.DetectEcosystem(), nil
}

func (s *Service) cloneInstance(ctx context.Context, _ *mcp.CallToolRequest, req CloneRequest) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := m.CloneInstance(ctx, req.Source, req.Destination); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) renameInstance(ctx context.Context, _ *mcp.CallToolRequest, req RenameRequest) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := m.RenameInstance(ctx, req.OldName, req.NewName); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) deleteInstance(ctx context.Context, _ *mcp.CallToolRequest, req InstanceName) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := m.DeleteInstance(ctx, req.Name); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) restartInstance(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, State, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, State{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	inst, err := m.RestartInstance(ctx)
	if err != nil {
		return nil, State{}, err
	}
	return nil, State{Ready: true, Name: inst.Name, Serial: inst.Serial, VulkanGPU: inst.VulkanDevice, KeyboardEnabled: inst.KeyboardEnabled}, nil
}

func (s *Service) getSettings(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, avd.InstanceSettings, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, avd.InstanceSettings{}, err
	}
	cfg, err := m.InstanceSettings()
	return nil, cfg, err
}

func (s *Service) setSettings(_ context.Context, _ *mcp.CallToolRequest, cfg avd.InstanceSettings) (*mcp.CallToolResult, Result, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.SetInstanceSettings(cfg); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) phonePresets(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
	Presets []avd.PhonePreset `json:"presets"`
}, error) {
	return nil, struct {
		Presets []avd.PhonePreset `json:"presets"`
	}{Presets: avd.PhonePresets()}, nil
}

func (s *Service) randomizePhone(_ context.Context, _ *mcp.CallToolRequest, req RandomPhoneRequest) (*mcp.CallToolResult, avd.PhoneIdentity, error) {
	presets := avd.PhonePresets()
	var chosen *avd.PhonePreset
	if req.PresetIndex >= 0 && req.PresetIndex < len(presets) {
		chosen = &presets[req.PresetIndex]
	}
	id := avd.RandomPhoneIdentity(chosen)
	if req.Apply {
		m, err := s.engineManager()
		if err != nil {
			return nil, avd.PhoneIdentity{}, err
		}
		if err := m.SetPhoneIdentity(id); err != nil {
			return nil, avd.PhoneIdentity{}, err
		}
	}
	return nil, id, nil
}

func (s *Service) diskUsage(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, avd.DiskUsageInfo, error) {
	m, err := s.engineManager()
	if err != nil {
		return nil, avd.DiskUsageInfo{}, err
	}
	info, err := m.DiskUsage()
	return nil, info, err
}

func (s *Service) trimDisk(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.TrimDisk(ctx, inst); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) runningApps(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
	Apps []avd.RunningApp `json:"apps"`
}, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, struct {
			Apps []avd.RunningApp `json:"apps"`
		}{}, err
	}
	apps, err := m.ListRunningApps(ctx, inst)
	return nil, struct {
		Apps []avd.RunningApp `json:"apps"`
	}{Apps: apps}, err
}

func (s *Service) closeApp(ctx context.Context, _ *mcp.CallToolRequest, req Package) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.CloseApp(ctx, inst, req.Package); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) clearAppData(ctx context.Context, _ *mcp.CallToolRequest, req Package) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.ClearAppData(ctx, inst, req.Package); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) uninstallApp(ctx context.Context, _ *mcp.CallToolRequest, req Package) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.UninstallApp(ctx, inst, req.Package); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) setGPS(ctx context.Context, _ *mcp.CallToolRequest, req GPSRequest) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.SetGPSLocation(ctx, inst, req.Lat, req.Lon, req.Alt); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) shake(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.Shake(ctx, inst); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) rotate(ctx context.Context, _ *mcp.CallToolRequest, req RotateRequest) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.RotateDisplay(ctx, inst, req.Landscape); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) setBrightness(ctx context.Context, _ *mcp.CallToolRequest, req BrightnessRequest) (*mcp.CallToolResult, Result, error) {
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	if err := m.SetBrightness(ctx, inst, req.Percent); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) macroList(_ context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
	Scripts []macro.Script `json:"scripts"`
}, error) {
	list, err := macro.NewStore("").List()
	return nil, struct {
		Scripts []macro.Script `json:"scripts"`
	}{Scripts: list}, err
}

func (s *Service) macroSave(_ context.Context, _ *mcp.CallToolRequest, req macro.Script) (*mcp.CallToolResult, Result, error) {
	if err := macro.NewStore("").Save(req); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) macroDelete(_ context.Context, _ *mcp.CallToolRequest, req MacroNameRequest) (*mcp.CallToolResult, Result, error) {
	if err := macro.NewStore("").Delete(req.Name); err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

func (s *Service) macroMerge(_ context.Context, _ *mcp.CallToolRequest, req MacroMergeRequest) (*mcp.CallToolResult, macro.Script, error) {
	store := macro.NewStore("")
	var parts []macro.Script
	for _, src := range req.Sources {
		sc, err := store.Load(src)
		if err != nil {
			return nil, macro.Script{}, err
		}
		parts = append(parts, sc)
	}
	merged, err := macro.Merge(req.Name, parts, req.GapMS)
	if err != nil {
		return nil, macro.Script{}, err
	}
	if err := store.Save(merged); err != nil {
		return nil, macro.Script{}, err
	}
	return nil, merged, nil
}

func (s *Service) macroReplay(ctx context.Context, _ *mcp.CallToolRequest, req MacroReplayRequest) (*mcp.CallToolResult, Result, error) {
	sc, err := macro.NewStore("").Load(req.Name)
	if err != nil {
		return nil, Result{}, err
	}
	m, inst, err := s.audioTarget(ctx)
	if err != nil {
		return nil, Result{}, err
	}
	ctrl, err := inst.Open()
	if err != nil {
		return nil, Result{}, err
	}
	defer ctrl.Close()
	frame, err := ctrl.Capture(nil)
	if err != nil {
		return nil, Result{}, err
	}
	w, h := frame.Bounds().Dx(), frame.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return nil, Result{}, errors.New("画面尺寸无效")
	}
	loops := req.Loops
	if loops <= 0 {
		loops = 1
	}
	err = macro.Replay(ctx, sc, macro.ReplayOptions{Loops: loops, Speed: req.Speed}, func(ev macro.Event) error {
		switch ev.Kind {
		case macro.EventTouchDown, macro.EventTouchMove:
			x := max(0, min(w-1, int(ev.NX*float64(w-1)+0.5)))
			y := max(0, min(h-1, int(ev.NY*float64(h-1)+0.5)))
			return ctrl.TouchDown(x, y)
		case macro.EventTouchUp:
			return ctrl.TouchUp()
		case macro.EventKeyDown:
			return ctrl.KeyDown(ev.Usage)
		case macro.EventKeyUp:
			return ctrl.KeyUp(ev.Usage)
		case macro.EventNavigate:
			return ctrl.Navigate(ev.Action)
		}
		return nil
	})
	_ = m
	if err != nil {
		return nil, Result{}, err
	}
	return nil, Result{OK: true}, nil
}

// Keep display referenced for display.Action in macro replay.
var _ = display.Back
