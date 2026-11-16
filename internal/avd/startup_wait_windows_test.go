package avd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitInstanceReadyReportsLauncherFailureWithoutWaitingForBootTimeout(t *testing.T) {
	failure := errors.New("launcher exited with status 1")
	exit := make(chan error, 1)
	exit <- failure
	result, err := waitInstanceReady(context.Background(), "emulator-5554", func() (Instance, error) {
		return Instance{}, errInstanceNotRunning
	}, func(context.Context, Instance) (Instance, error) {
		t.Fatal("an unregistered instance was probed")
		return Instance{}, nil
	}, exit, time.Millisecond)
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "异常退出") || result.Serial != "" {
		t.Fatalf("launcher failure was hidden: %+v %v", result, err)
	}
}

func TestWaitInstanceReadyAcceptsLauncherHandoffAndRegisteredInstance(t *testing.T) {
	for _, launchErr := range []error{nil, errors.New("launcher failed after handoff")} {
		exit := make(chan error, 1)
		exit <- launchErr
		calls := 0
		want := Instance{Serial: "emulator-5554", VulkanDevice: "test"}
		got, err := waitInstanceReady(context.Background(), want.Serial, func() (Instance, error) {
			calls++
			if calls == 1 {
				return Instance{}, errInstanceNotRunning
			}
			return want, nil
		}, func(_ context.Context, instance Instance) (Instance, error) {
			return instance, nil
		}, exit, time.Second)
		if err != nil || got.Serial != want.Serial || got.VulkanDevice != want.VulkanDevice {
			t.Fatalf("handoff %v failed: %+v %v", launchErr, got, err)
		}
	}
}

func TestWaitInstanceReadyRespectsCancellationBeforeAndAfterProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitInstanceReady(ctx, "emulator-5554", func() (Instance, error) {
		t.Fatal("discovery ran after cancellation")
		return Instance{}, nil
	}, nil, nil, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	_, err = waitInstanceReady(ctx, "emulator-5554", func() (Instance, error) {
		return Instance{Serial: "emulator-5554"}, nil
	}, func(_ context.Context, instance Instance) (Instance, error) {
		cancel()
		return instance, nil
	}, nil, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("successful stale probe ignored cancellation: %v", err)
	}
}
