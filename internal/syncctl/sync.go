// Package syncctl implements MuMu-style multi-instance window synchronization
// (matching nemu-inputmanager.dll sync capabilities). Input from the master
// emulator window is broadcast in normalized coordinates to selected slave
// emulator windows in real time.
package syncctl

import (
	"sort"
	"sync"

	"github.com/neko233-com/NekoSimulator233/internal/display"
)

// Target receives synchronized input events from the master window.
type Target interface {
	SyncTouchDown(nx, ny float64)
	SyncTouchMove(nx, ny float64)
	SyncTouchUp()
	SyncKey(usage uint16, down bool)
	SyncNavigate(action display.Action)
}

// Hub coordinates multi-instance synchronization across all open viewer windows.
type Hub struct {
	mu       sync.RWMutex
	enabled  bool
	master   string
	targets  map[string]Target
	selected map[string]bool
}

func NewHub() *Hub {
	return &Hub{
		targets:  make(map[string]Target),
		selected: make(map[string]bool),
	}
}

func (h *Hub) Register(name string, target Target) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.targets[name] = target
	if _, ok := h.selected[name]; !ok {
		h.selected[name] = true
	}
}

func (h *Hub) Unregister(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.targets, name)
	delete(h.selected, name)
	if h.master == name {
		h.master = ""
		h.enabled = false
	}
}

func (h *Hub) Start(master string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.master = master
	h.enabled = true
}

func (h *Hub) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enabled = false
}

func (h *Hub) ActiveFor(name string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.enabled && h.master == name
}

func (h *Hub) Enabled() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.enabled
}

func (h *Hub) Master() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.master
}

func (h *Hub) SetSelected(name string, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.selected[name] = on
}

func (h *Hub) IsSelected(name string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.selected[name]
}

// Peers returns all open target names other than master, sorted.
func (h *Hub) Peers(master string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var peers []string
	for name := range h.targets {
		if name != master {
			peers = append(peers, name)
		}
	}
	sort.Strings(peers)
	return peers
}

func (h *Hub) slavesLocked(master string) []Target {
	if !h.enabled || h.master != master {
		return nil
	}
	var out []Target
	for name, target := range h.targets {
		if name != master && h.selected[name] && target != nil {
			out = append(out, target)
		}
	}
	return out
}

func (h *Hub) BroadcastTouchDown(master string, nx, ny float64) {
	h.mu.RLock()
	slaves := h.slavesLocked(master)
	h.mu.RUnlock()
	for _, s := range slaves {
		s.SyncTouchDown(nx, ny)
	}
}

func (h *Hub) BroadcastTouchMove(master string, nx, ny float64) {
	h.mu.RLock()
	slaves := h.slavesLocked(master)
	h.mu.RUnlock()
	for _, s := range slaves {
		s.SyncTouchMove(nx, ny)
	}
}

func (h *Hub) BroadcastTouchUp(master string) {
	h.mu.RLock()
	slaves := h.slavesLocked(master)
	h.mu.RUnlock()
	for _, s := range slaves {
		s.SyncTouchUp()
	}
}

func (h *Hub) BroadcastKey(master string, usage uint16, down bool) {
	h.mu.RLock()
	slaves := h.slavesLocked(master)
	h.mu.RUnlock()
	for _, s := range slaves {
		s.SyncKey(usage, down)
	}
}

func (h *Hub) BroadcastNavigate(master string, action display.Action) {
	h.mu.RLock()
	slaves := h.slavesLocked(master)
	h.mu.RUnlock()
	for _, s := range slaves {
		s.SyncNavigate(action)
	}
}
