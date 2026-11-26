// Package macro implements MuMu-compatible operation recording, playback,
// speed scaling, looping, and merging (matching nemu-inputmanager.dll).
package macro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/display"
)

const (
	EventTouchDown = "touch_down"
	EventTouchMove = "touch_move"
	EventTouchUp   = "touch_up"
	EventKeyDown   = "key_down"
	EventKeyUp     = "key_up"
	EventNavigate  = "navigate"
)

// Event is a single normalized timestamped input action in a macro script.
type Event struct {
	OffsetMS int64          `json:"offset_ms"`
	Kind     string         `json:"kind"`
	NX       float64        `json:"nx,omitempty"`     // Normalized [0..1] X
	NY       float64        `json:"ny,omitempty"`     // Normalized [0..1] Y
	Usage    uint16         `json:"usage,omitempty"`  // USB HID key usage
	Action   display.Action `json:"action,omitempty"` // Android navigation action
}

// Script is a persisted macro recording.
type Script struct {
	Name       string  `json:"name"`
	CreatedAt  string  `json:"created_at"`
	DurationMS int64   `json:"duration_ms"`
	Events     []Event `json:"events"`
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_\-\p{Han}]{1,64}$`)

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return errors.New("宏名称仅允许中英文、数字、下划线和连字符（1-64 字符）")
	}
	return nil
}

// DefaultDir returns the per-user macro scripts directory.
func DefaultDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "macros")
}

// Store manages reading, writing, listing, merging, and deleting macros.
type Store struct {
	Dir string
}

func NewStore(dir string) *Store {
	if dir == "" {
		dir = DefaultDir()
	}
	return &Store{Dir: dir}
}

func (s *Store) pathFor(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, name+".json"), nil
}

func (s *Store) List() ([]Script, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Script
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if err != nil || len(b) > 4<<20 {
			continue
		}
		var sc Script
		if json.Unmarshal(b, &sc) == nil && sc.Name != "" {
			out = append(out, sc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func (s *Store) Load(name string) (Script, error) {
	p, err := s.pathFor(name)
	if err != nil {
		return Script{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Script{}, err
	}
	var sc Script
	if err := json.Unmarshal(b, &sc); err != nil {
		return Script{}, err
	}
	return sc, nil
}

func (s *Store) Save(sc Script) error {
	if err := ValidateName(sc.Name); err != nil {
		return err
	}
	if len(sc.Events) > 100000 {
		return errors.New("宏事件数量超出上限")
	}
	if sc.CreatedAt == "" {
		sc.CreatedAt = time.Now().Format(time.RFC3339)
	}
	if sc.DurationMS == 0 && len(sc.Events) > 0 {
		sc.DurationMS = sc.Events[len(sc.Events)-1].OffsetMS
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	p, err := s.pathFor(sc.Name)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".macro-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

func (s *Store) Delete(name string) error {
	p, err := s.pathFor(name)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Merge concatenates multiple scripts with an optional gap between them (MuMu MergeRecord parity).
func Merge(name string, scripts []Script, gapMS int64) (Script, error) {
	if err := ValidateName(name); err != nil {
		return Script{}, err
	}
	if len(scripts) == 0 {
		return Script{}, errors.New("至少需要一个宏脚本用于合并")
	}
	if gapMS < 0 {
		gapMS = 0
	}
	merged := Script{Name: name, CreatedAt: time.Now().Format(time.RFC3339)}
	var baseOffset int64
	for idx, sc := range scripts {
		if idx > 0 {
			baseOffset += gapMS
		}
		var maxOff int64
		for _, ev := range sc.Events {
			cp := ev
			cp.OffsetMS += baseOffset
			if ev.OffsetMS > maxOff {
				maxOff = ev.OffsetMS
			}
			merged.Events = append(merged.Events, cp)
		}
		if sc.DurationMS > maxOff {
			maxOff = sc.DurationMS
		}
		baseOffset += maxOff
	}
	merged.DurationMS = baseOffset
	return merged, nil
}

// Recorder captures live user events with pause/resume support.
type Recorder struct {
	mu          sync.Mutex
	active      bool
	paused      bool
	startedAt   time.Time
	pausedAt    time.Time
	pausedTotal time.Duration
	events      []Event
}

func (r *Recorder) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = true
	r.paused = false
	r.startedAt = time.Now()
	r.pausedTotal = 0
	r.events = nil
}

func (r *Recorder) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *Recorder) Paused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

func (r *Recorder) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active && !r.paused {
		r.paused = true
		r.pausedAt = time.Now()
	}
}

func (r *Recorder) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active && r.paused {
		r.pausedTotal += time.Since(r.pausedAt)
		r.paused = false
	}
}

func (r *Recorder) Record(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.paused {
		return
	}
	offset := time.Since(r.startedAt) - r.pausedTotal
	if offset < 0 {
		offset = 0
	}
	ev.OffsetMS = offset.Milliseconds()
	r.events = append(r.events, ev)
}

func (r *Recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *Recorder) Stop(name string) Script {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return Script{Name: name}
	}
	if r.paused {
		r.pausedTotal += time.Since(r.pausedAt)
	}
	dur := (time.Since(r.startedAt) - r.pausedTotal).Milliseconds()
	if dur < 0 {
		dur = 0
	}
	sc := Script{
		Name:       name,
		CreatedAt:  time.Now().Format(time.RFC3339),
		DurationMS: dur,
		Events:     append([]Event(nil), r.events...),
	}
	r.active = false
	r.paused = false
	r.events = nil
	return sc
}

// ReplayOptions controls playback speed and loop count.
type ReplayOptions struct {
	Loops int     // >=1; 0 means loop until ctx canceled
	Speed float64 // 0.25..8.0, default 1.0
}

// Replay plays a recorded Script through the emit callback, respecting speed,
// loop count, and context cancellation.
func Replay(ctx context.Context, sc Script, opts ReplayOptions, emit func(Event) error) error {
	if emit == nil {
		return errors.New("缺少回放执行回调")
	}
	speed := opts.Speed
	if speed <= 0 {
		speed = 1.0
	}
	if speed < 0.25 || speed > 8.0 {
		return fmt.Errorf("回放倍速须在 0.25 到 8.0 之间")
	}
	loops := opts.Loops
	if loops < 0 {
		loops = 1
	}
	for iter := 0; loops == 0 || iter < loops; iter++ {
		var prev int64
		for _, ev := range sc.Events {
			delta := ev.OffsetMS - prev
			if delta > 0 {
				wait := time.Duration(float64(delta)/speed) * time.Millisecond
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
			prev = ev.OffsetMS
			if err := emit(ev); err != nil {
				return err
			}
		}
		if loops == 0 && len(sc.Events) == 0 {
			return nil
		}
	}
	return nil
}
