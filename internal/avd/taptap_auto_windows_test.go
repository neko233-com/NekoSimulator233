package avd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFirstRunTapTapRetriesAndDoesNotReinstall(t *testing.T) {
	m := backupFixture(t)
	if status, err := m.TapTapAutoStatus(); err != nil || status != "pending" {
		t.Fatalf("new instance status: %s %v", status, err)
	}
	count := 0
	install := func(context.Context, Instance) error {
		count++
		if count == 1 {
			return errors.New("offline")
		}
		return nil
	}
	if err := m.runPendingTapTap(context.Background(), Instance{}, install); err == nil {
		t.Fatal("failure hidden")
	}
	if status, err := m.TapTapAutoStatus(); err != nil || status != "retry_pending" {
		t.Fatalf("retry status: %s %v", status, err)
	}
	if err := m.runPendingTapTap(context.Background(), Instance{}, install); err != nil {
		t.Fatal(err)
	}
	if status, err := m.TapTapAutoStatus(); err != nil || status != "complete" {
		t.Fatalf("complete status: %s %v", status, err)
	}
	if err := m.runPendingTapTap(context.Background(), Instance{}, install); err != nil || count != 2 {
		t.Fatal("completed task ran again")
	}
}

func TestFirstRunTapTapLockAndLegacyInstance(t *testing.T) {
	m := backupFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- m.runPendingTapTap(context.Background(), Instance{}, func(context.Context, Instance) error {
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first setup did not start")
	}
	called := false
	if err := m.runPendingTapTap(context.Background(), Instance{}, func(context.Context, Instance) error { called = true; return nil }); err != nil || called {
		close(release)
		t.Fatalf("parallel setup was not deferred: %v", err)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first setup did not finish")
	}
	legacy := backupFixture(t)
	if err := os.Remove(filepath.Join(legacy.Home, legacy.Name+".avd", tapTapPendingFile)); err != nil {
		t.Fatal(err)
	}
	if status, err := legacy.TapTapAutoStatus(); err != nil || status != "not_scheduled" {
		t.Fatalf("legacy status: %s %v", status, err)
	}
	if err := legacy.runPendingTapTap(context.Background(), Instance{}, func(context.Context, Instance) error { t.Fatal("legacy instance changed"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestFirstRunTapTapRejectsInvalidMarkers(t *testing.T) {
	m := backupFixture(t)
	dir := filepath.Join(m.Home, m.Name+".avd")
	if err := os.WriteFile(filepath.Join(dir, tapTapPendingFile), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TapTapAutoStatus(); err == nil {
		t.Fatal("invalid marker accepted")
	}
}
