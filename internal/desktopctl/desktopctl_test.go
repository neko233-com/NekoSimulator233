package desktopctl

import (
	"context"
	"errors"
	"testing"
)

func fakeRunner(t *testing.T) runner {
	t.Helper()
	return func(ctx context.Context, path string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "version" {
			return []byte(`{"ok":true,"data":{"version":"1.3.1"}}`), nil
		}
		if len(args) > 0 && args[0] == "list-windows" {
			return []byte(`{"ok":true,"data":{"windows":[{"id":"w-42","title":"demo - NekoSimulator233","app_name":"NekoSimulator233.exe","pid":7,"is_focused":true}]}}`), nil
		}
		return nil, errors.New("unexpected command")
	}
}

func TestStatusAvailable(t *testing.T) {
	s := NewWithRunner("agent-desktop", fakeRunner(t)).Status(context.Background())
	if !s.OK || !s.Available || s.Version != "1.3.1" {
		t.Fatalf("unexpected status: %+v", s)
	}
}

func TestStatusFailure(t *testing.T) {
	s := NewWithRunner("agent-desktop", func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not found")
	}).Status(context.Background())
	if s.OK || s.Available || s.Reason == "" {
		t.Fatalf("expected unavailable status, got %+v", s)
	}
}

func TestListWindows(t *testing.T) {
	wins, err := NewWithRunner("agent-desktop", fakeRunner(t)).ListWindows(context.Background(), "NekoSimulator233.exe")
	if err != nil || len(wins) != 1 || wins[0].ID != "w-42" {
		t.Fatalf("unexpected windows: %+v %v", wins, err)
	}
}

func TestViewerApp(t *testing.T) {
	if got := ViewerApp("demo"); got != "demo"+DefaultViewerSuffix {
		t.Fatalf("viewer app = %q", got)
	}
}
