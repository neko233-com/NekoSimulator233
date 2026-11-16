package avd

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestControlReadinessRevalidatesIdentityAndExpires(t *testing.T) {
	m := Manager{SDK: "sdk", Home: "home", Name: "test"}
	current := Instance{Name: "test", Serial: "emulator-5554", pid: 42, port: 8554, token: "one", sdk: "sdk"}
	clock := time.Unix(100, 0)
	inspections := 0
	discoveries := 0
	discover := func() (Instance, error) { discoveries++; return current, nil }
	inspect := func(_ context.Context, i Instance) (Instance, error) {
		inspections++
		i.VulkanDevice = "verified"
		return i, nil
	}
	var cache ControlReadiness
	call := func(manager Manager) Instance {
		t.Helper()
		i, err := cache.ready(context.Background(), manager, func() time.Time { return clock }, discover, inspect)
		if err != nil {
			t.Fatal(err)
		}
		if i.VulkanDevice != "verified" {
			t.Fatal("returned unverified instance")
		}
		return i
	}
	call(m)
	call(m)
	if inspections != 1 {
		t.Fatalf("repeated inspection: %d", inspections)
	}
	if discoveries != 2 {
		t.Fatalf("process discovery was skipped: %d", discoveries)
	}
	current.token = "two"
	call(m)
	if inspections != 2 {
		t.Fatal("changed endpoint reused previous inspection")
	}
	clock = clock.Add(controlReadinessTTL)
	call(m)
	if inspections != 3 {
		t.Fatal("expired readiness was reused")
	}
	m.Home = "other"
	call(m)
	if inspections != 4 {
		t.Fatal("different manager reused previous inspection")
	}
}

func TestControlReadinessDoesNotCacheErrorsOrCanceledRequests(t *testing.T) {
	m := Manager{Name: "test"}
	current := Instance{Name: "test", pid: 42, port: 8554, token: "one"}
	clock := time.Unix(100, 0)
	now := func() time.Time { return clock }
	discover := func() (Instance, error) { return current, nil }
	inspect := func(_ context.Context, i Instance) (Instance, error) { return i, nil }
	var cache ControlReadiness
	if _, err := cache.ready(context.Background(), m, now, discover, inspect); err != nil {
		t.Fatal(err)
	}
	badDiscovery := func() (Instance, error) { return Instance{}, errors.New("missing process") }
	if _, err := cache.ready(context.Background(), m, now, badDiscovery, inspect); err == nil {
		t.Fatal("missing process accepted")
	}
	inspections := 0
	failing := func(context.Context, Instance) (Instance, error) {
		inspections++
		return Instance{}, errors.New("not ready")
	}
	if _, err := cache.ready(context.Background(), m, now, discover, failing); err == nil || inspections != 1 {
		t.Fatal("discovery failure retained readiness")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.ready(ctx, m, now, discover, inspect); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if _, err := cache.ready(context.Background(), m, now, discover, failing); err == nil || inspections != 2 {
		t.Fatal("failed inspection was cached")
	}
}
