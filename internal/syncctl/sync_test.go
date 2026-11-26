package syncctl

import (
	"testing"

	"github.com/neko233-com/NekoSimulator233/internal/display"
)

type fakeTarget struct {
	downs []float64
	ups   int
	keys  []uint16
	navs  []display.Action
}

func (f *fakeTarget) SyncTouchDown(nx, ny float64)   { f.downs = append(f.downs, nx+ny) }
func (f *fakeTarget) SyncTouchMove(nx, ny float64)   {}
func (f *fakeTarget) SyncTouchUp()                   { f.ups++ }
func (f *fakeTarget) SyncKey(usage uint16, _ bool)   { f.keys = append(f.keys, usage) }
func (f *fakeTarget) SyncNavigate(a display.Action)  { f.navs = append(f.navs, a) }

func TestHubBroadcastsOnlyToSelectedSlaves(t *testing.T) {
	hub := NewHub()
	master := &fakeTarget{}
	slaveA := &fakeTarget{}
	slaveB := &fakeTarget{}
	hub.Register("dev0", master)
	hub.Register("dev1", slaveA)
	hub.Register("dev2", slaveB)
	hub.SetSelected("dev2", false)
	hub.Start("dev0")

	hub.BroadcastTouchDown("dev0", 0.25, 0.5)
	hub.BroadcastTouchUp("dev0")
	hub.BroadcastKey("dev0", 0x1a, true)
	hub.BroadcastNavigate("dev0", display.Home)

	if len(master.downs) != 0 || len(slaveB.downs) != 0 {
		t.Fatalf("unexpected broadcast to master/unselected: master=%+v slaveB=%+v", master, slaveB)
	}
	if len(slaveA.downs) != 1 || slaveA.ups != 1 || len(slaveA.keys) != 1 || len(slaveA.navs) != 1 {
		t.Fatalf("slaveA missed events: %+v", slaveA)
	}
	hub.Unregister("dev0")
	if hub.Enabled() {
		t.Fatal("hub remained enabled after master unregistered")
	}
}
