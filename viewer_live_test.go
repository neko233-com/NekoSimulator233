package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/testutil"
)

func TestLiveMappedKeysAndMouse(t *testing.T) {
	home := os.Getenv("NEKO_TEST_AGENT_AVD_HOME")
	if home == "" {
		t.Skip("set NEKO_TEST_AGENT_AVD_HOME to the isolated validation AVD")
	}
	testutil.LockAVD(t, home)
	m, err := avd.DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	m.Home, m.Name = home, "NekoValidation"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instance, err := m.Ready(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Serial != "emulator-5578" {
		t.Fatal("refusing a non-validation instance")
	}
	c, err := instance.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Capture(nil); err != nil {
		t.Fatal(err)
	}
	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()
	cmd := exec.CommandContext(watchCtx, filepath.Join(m.SDK, "platform-tools", "adb.exe"), "-s", instance.Serial, "shell", "getevent", "-lt")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(pipe); done <- data }()
	time.Sleep(300 * time.Millisecond)
	input := newViewerInput(c)
	for _, event := range []pointerEvent{
		{kind: mappedDown, usage: 4, x: 100, y: 400},
		{kind: mappedDown, usage: 5, x: 300, y: 400},
		{kind: pointerDown, x: 500, y: 400},
		{kind: releaseKeys},
		{kind: pointerUp},
	} {
		if err = input.handle(event); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	input.release()
	time.Sleep(300 * time.Millisecond)
	watchCancel()
	data := <-done
	_ = cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	active := map[uint64]bool{}
	var slot uint64
	peak, releases := 0, 0
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, e := strconv.ParseUint(fields[len(fields)-1], 16, 32)
		if e != nil {
			continue
		}
		if strings.Contains(line, "ABS_MT_SLOT") {
			slot = v
		}
		if strings.Contains(line, "ABS_MT_TRACKING_ID") {
			if v == 0xffffffff {
				delete(active, slot)
				releases++
			} else {
				active[slot] = true
				peak = max(peak, len(active))
			}
		}
	}
	if peak < 3 || releases < 3 || len(active) != 0 {
		t.Fatalf("guest input: peak=%d releases=%d remaining=%d", peak, releases, len(active))
	}
	t.Log("isolated guest observed two mapped keys plus mouse simultaneously and all releases")
}

func TestLiveJoystickMovesOneContactAndReleases(t *testing.T) {
	home := os.Getenv("NEKO_TEST_AGENT_AVD_HOME")
	if home == "" {
		t.Skip("set NEKO_TEST_AGENT_AVD_HOME to the isolated validation AVD")
	}
	testutil.LockAVD(t, home)
	m, err := avd.DiscoverManager()
	if err != nil {
		t.Fatal(err)
	}
	m.Home, m.Name = home, "NekoValidation"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instance, err := m.Ready(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Serial != "emulator-5578" {
		t.Fatal("refusing a non-validation instance")
	}
	c, err := instance.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Capture(nil); err != nil {
		t.Fatal(err)
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	cmd := exec.CommandContext(watchCtx, filepath.Join(m.SDK, "platform-tools", "adb.exe"), "-s", instance.Serial, "shell", "getevent", "-lt")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(pipe); done <- b }()
	time.Sleep(300 * time.Millisecond)
	in := newViewerInput(c)
	for _, ev := range []pointerEvent{
		{kind: stickDown, usage: 0x1a, x: 300, y: 500, radius: 100, maxX: 719, maxY: 1279},
		{kind: stickDown, usage: 0x07},
		{kind: stickUp, usage: 0x1a},
		{kind: pointerDown, x: 600, y: 700},
		{kind: releaseKeys},
		{kind: pointerUp},
	} {
		if err = in.handle(ev); err != nil {
			break
		}
		time.Sleep(120 * time.Millisecond)
	}
	in.release()
	time.Sleep(300 * time.Millisecond)
	stopWatch()
	data := <-done
	_ = cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	active := map[uint64]bool{}
	xValues := map[uint64]bool{}
	yValues := map[uint64]bool{}
	var slot uint64
	peak, downs, ups := 0, 0, 0
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, e := strconv.ParseUint(f[len(f)-1], 16, 32)
		if e != nil {
			continue
		}
		if strings.Contains(line, "ABS_MT_SLOT") {
			slot = v
		}
		if strings.Contains(line, "ABS_MT_POSITION_X") {
			xValues[v] = true
		}
		if strings.Contains(line, "ABS_MT_POSITION_Y") {
			yValues[v] = true
		}
		if strings.Contains(line, "ABS_MT_TRACKING_ID") {
			if v == 0xffffffff {
				delete(active, slot)
				ups++
			} else {
				active[slot] = true
				downs++
				peak = max(peak, len(active))
			}
		}
	}
	if downs != 2 || ups != 2 || peak != 2 || len(active) != 0 || len(xValues) < 2 || len(yValues) < 2 {
		t.Fatalf("guest joystick input: downs=%d ups=%d peak=%d remaining=%d x=%d y=%d", downs, ups, peak, len(active), len(xValues), len(yValues))
	}
	t.Log("isolated guest observed one moving joystick contact alongside mouse and both releases")
}
