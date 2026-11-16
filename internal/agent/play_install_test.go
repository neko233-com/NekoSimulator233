package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func TestPlayImageMCPProgressDoesNotBlockOtherTools(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := &Service{
		Manager: func() (avd.Manager, error) { return avd.Manager{}, nil },
		playImageInstall: func(ctx context.Context, _ avd.Manager, api int, notify func(int)) (avd.PlayImageInstallResult, error) {
			if api != 34 || notify == nil {
				return avd.PlayImageInstallResult{}, errors.New("missing API or progress callback")
			}
			notify(10)
			close(started)
			select {
			case <-release:
				notify(100)
				return avd.PlayImageInstallResult{API: api}, nil
			case <-ctx.Done():
				return avd.PlayImageInstallResult{}, ctx.Err()
			}
		},
	}
	progress := make(chan float64, 2)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) { progress <- req.Params.Progress },
	})
	server := newServer(s)
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	params := &mcp.CallToolParams{Name: "neko_install_play_image", Arguments: map[string]any{}}
	params.SetProgressToken("play-test")
	done := make(chan error, 1)
	go func() {
		result, err := cs.CallTool(ctx, params)
		if err == nil && result.IsError {
			err = context.Canceled
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not start")
	}
	otherCtx, otherCancel := context.WithTimeout(ctx, time.Second)
	other, err := cs.CallTool(otherCtx, &mcp.CallToolParams{Name: "neko_status", Arguments: map[string]any{}})
	otherCancel()
	if err != nil || other.IsError {
		close(release)
		t.Fatalf("status blocked during install: %+v %v", other, err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("install did not finish")
	}
	for _, want := range []float64{10, 100} {
		select {
		case got := <-progress:
			if got != want {
				t.Fatalf("progress %v, want %v", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing progress")
		}
	}
}
