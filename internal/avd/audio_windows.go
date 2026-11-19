//go:build windows

package avd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// AudioStatus is the measurable part of the guest audio state. STREAM_MUSIC
// (3) is the stream games and media use.
type AudioStatus struct {
	Serial       string `json:"serial"`
	MusicMuted   bool   `json:"music_muted"`
	MusicIndex   int    `json:"music_index"`
	MusicMax     int    `json:"music_max"`
	RingerMode   string `json:"ringer_mode,omitempty"`
	OutputDevice string `json:"output_device,omitempty"`
	ActiveTracks int    `json:"active_tracks"`
	RawLength    int    `json:"raw_length"`
}

var (
	mutedLine       = regexp.MustCompile(`(?m)^\s*Muted:\s*(true|false)`)
	indexLine       = regexp.MustCompile(`(?m)^\s*streamVolume:\s*(\d+)`)
	maxLine         = regexp.MustCompile(`(?m)^\s*Max:\s*(\d+)`)
	ringerLine      = regexp.MustCompile(`(?m)Ringer mode:\s*(\w+)`)
	deviceLine      = regexp.MustCompile(`(?m)^\s*Devices:\s*(.*)$`)
	activeTrackLine = regexp.MustCompile(`(?m)^\s{4}\+\+\+ pid:\s*\d+`)
)

// AudioStatus reads the guest's audio state via dumpsys. Values are treated as
// untrusted diagnostic data, never as instructions.
func (m Manager) AudioStatus(ctx context.Context, serial string) (AudioStatus, error) {
	out, err := m.adb(ctx, serial, "shell", "dumpsys", "audio")
	if err != nil {
		return AudioStatus{}, err
	}
	text := string(out)
	status := AudioStatus{Serial: serial, RawLength: len(out), ActiveTracks: len(activeTrackLine.FindAllString(text, -1))}
	// dumpsys separates stream sections with "- STREAM_<NAME>:".
	block := ""
	for _, part := range strings.Split(text, "- STREAM_") {
		if strings.HasPrefix(part, "MUSIC:") {
			block = part
			break
		}
	}
	if block != "" {
		status.MusicMuted = strings.Contains(strings.ToLower(mutedLine.FindString(block)), "true")
		if v := indexLine.FindStringSubmatch(block); len(v) == 2 {
			status.MusicIndex, _ = strconv.Atoi(v[1])
		}
		if v := maxLine.FindStringSubmatch(block); len(v) == 2 {
			status.MusicMax, _ = strconv.Atoi(v[1])
		}
	}
	if v := ringerLine.FindStringSubmatch(text); len(v) == 2 {
		status.RingerMode = v[1]
	}
	if v := deviceLine.FindStringSubmatch(text); len(v) == 2 {
		status.OutputDevice = strings.TrimSpace(v[1])
	}
	if status.RawLength == 0 {
		return AudioStatus{}, errors.New("dumpsys audio 返回空内容")
	}
	return status, nil
}

// AudioLatency reports the guest AudioFlinger output threads and its own
// measured getLatency() binder-call statistics. This is a diagnostic of the
// audio path inside Android, not an end-to-end speaker-to-microphone latency.
type AudioLatency struct {
	Serial        string   `json:"serial"`
	OutputThreads []string `json:"output_threads"`
	GetLatency    string   `json:"get_latency,omitempty"`
	FastMixer     bool     `json:"fast_mixer"`
	RawLength     int      `json:"raw_length"`
}

func (m Manager) AudioLatency(ctx context.Context, serial string) (AudioLatency, error) {
	out, err := m.adb(ctx, serial, "shell", "dumpsys", "media.audio_flinger")
	if err != nil {
		return AudioLatency{}, err
	}
	text := string(out)
	result := AudioLatency{Serial: serial, RawLength: len(out)}
	threadRe := regexp.MustCompile(`(?m)Output thread .*?, name ([^,]+), tid \d+, type \d+ \((\w+)\)`)
	for _, match := range threadRe.FindAllStringSubmatch(text, -1) {
		result.OutputThreads = append(result.OutputThreads, strings.TrimSpace(match[1])+"/"+match[2])
	}
	if v := regexp.MustCompile(`(?m)getLatency (n=\d+ ave=[\d.]+ std=[\d.]+ min=[\d.]+ max=[\d.]+)`).FindStringSubmatch(text); len(v) == 2 {
		result.GetLatency = v[1]
	}
	result.FastMixer = !strings.Contains(text, "No FastMixer")
	if result.RawLength == 0 {
		return AudioLatency{}, errors.New("dumpsys media.audio_flinger 返回空内容")
	}
	return result, nil
}

// SetMusicMute mutes or unmutes STREAM_MUSIC through the guest AudioService.
func (m Manager) SetMusicMute(ctx context.Context, serial string, mute bool) error {
	action := "adj-unmute"
	if mute {
		action = "adj-mute"
	}
	if _, err := m.adb(ctx, serial, "shell", "cmd", "audio", action, "3"); err != nil {
		return fmt.Errorf("设置静音失败：%w", err)
	}
	return nil
}

// SetMusicVolume sets STREAM_MUSIC volume index (0..15 on the emulated device).
func (m Manager) SetMusicVolume(ctx context.Context, serial string, index int) error {
	if index < 0 || index > 15 {
		return errors.New("音量索引必须在 0 到 15 之间")
	}
	if _, err := m.adb(ctx, serial, "shell", "cmd", "audio", "set-volume", "3", strconv.Itoa(index)); err != nil {
		return fmt.Errorf("设置音量失败：%w", err)
	}
	return nil
}
