package main

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/neko233-com/NekoSimulator233/internal/update"
)

func TestIdleDurationWrapAndAmbiguity(t *testing.T) {
	if unsafe.Sizeof(lastInputInfo{}) != 8 {
		t.Fatal("LASTINPUTINFO layout")
	}
	if d, err := idleDuration(1000, 500); err != nil || d != 500*time.Millisecond {
		t.Fatalf("idle %v %v", d, err)
	}
	if d, err := idleDuration(100, ^uint32(0)-99); err != nil || d != 200*time.Millisecond {
		t.Fatalf("wrapped idle %v %v", d, err)
	}
	if _, err := idleDuration(100, 200); err == nil {
		t.Fatal("future tick treated as idle")
	}
}

func TestPeriodicUpdateGuardsAndAutomaticStart(t *testing.T) {
	m := &update.Manifest{Version: "0.2.0", MSI: `C:\updates\next.msi`, SHA256: "digest"}
	idle := 3 * time.Minute
	checks, starts := 0, 0
	var guardErr error
	ops := periodicUpdateOps{
		idle:        func() (time.Duration, error) { return idle, nil },
		lookup:      func() (*update.Manifest, string, string) { return m, `C:\app\NekoSimulator233.exe`, "" },
		checkOthers: func(string) error { checks++; return guardErr },
		start: func(got *update.Manifest, current, exe string, guard func() error) error {
			starts++
			if got != m || current != version || exe == "" {
				t.Fatal("wrong update arguments")
			}
			return guard()
		},
	}
	if started, _ := attemptPeriodicUpdate(updateKey(m), ops); started || checks != 0 || starts != 0 {
		t.Fatal("failed update was retried")
	}
	idle = time.Second
	if started, _ := attemptPeriodicUpdate("", ops); started || checks != 0 {
		t.Fatal("active user was interrupted")
	}
	idle = 3 * time.Minute
	guardErr = errors.New("another instance running")
	if started, message := attemptPeriodicUpdate("", ops); started || starts != 0 || message == "" {
		t.Fatal("active instance was not protected")
	}
	guardErr = nil
	idleCalls := 0
	ops.idle = func() (time.Duration, error) {
		idleCalls++
		if idleCalls == 2 {
			return time.Second, nil
		}
		return 3 * time.Minute, nil
	}
	if started, _ := attemptPeriodicUpdate("", ops); started || starts != 0 {
		t.Fatal("user activity during check was ignored")
	}
	idleCalls = 0
	ops.idle = func() (time.Duration, error) {
		idleCalls++
		if idleCalls == 3 {
			return time.Second, nil
		}
		return 3 * time.Minute, nil
	}
	if started, message := attemptPeriodicUpdate("", ops); started || starts != 1 || !strings.Contains(message, "暂缓") {
		t.Fatal("user activity during staging was ignored")
	}
	checks = 0
	ops.idle = func() (time.Duration, error) { return 3 * time.Minute, nil }
	ops.checkOthers = func(string) error {
		checks++
		if checks == 2 {
			return errors.New("another instance started during staging")
		}
		return nil
	}
	if started, message := attemptPeriodicUpdate("", ops); started || starts != 2 || !strings.Contains(message, "暂缓") {
		t.Fatal("instance started during staging was ignored")
	}
	ops.checkOthers = func(string) error { return nil }
	ops.idle = func() (time.Duration, error) { return 3 * time.Minute, nil }
	if started, message := attemptPeriodicUpdate("", ops); !started || starts != 3 || message != "" {
		t.Fatalf("eligible update: %v %q", started, message)
	}
}

func TestPeriodicUpdateDoesNotInterruptWork(t *testing.T) {
	a := &app{}
	if !a.periodicUpdateEligible() {
		t.Fatal("idle app rejected")
	}
	for _, current := range []*app{
		{busy: true}, {updateInProgress: true}, {backupCancel: func() {}},
		{viewers: map[string]*viewer{"active": {}}}, {resources: map[string]*resourceSettings{"editing": {}}}, {newInstance: "pending"},
	} {
		if current.periodicUpdateEligible() {
			t.Fatal("active work accepted")
		}
	}
}
