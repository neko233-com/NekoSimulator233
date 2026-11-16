package mumu

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestListContextHonorsCancellationBeforeProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Client{}).ListContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestParseInstances(t *testing.T) {
	instances, err := parseInstances([]byte(`{"10":{"index":"10","name":"second"},"2":{"index":"2","name":"first","android_version":"15.0","is_android_started":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 || instances[0].Index != "2" || instances[1].Index != "10" || !instances[0].AndroidStarted {
		t.Fatalf("unexpected instances: %+v", instances)
	}
}

func TestLiveDiscoveryAndList(t *testing.T) {
	if os.Getenv("NEKO_TEST_LIVE_MUMU") != "1" {
		t.Skip("set NEKO_TEST_LIVE_MUMU=1 for read-only local integration")
	}
	root, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	instances, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MuMu root %s: %d instances", root, len(instances))
}

func TestParseInstancesRejectsMismatchedIndex(t *testing.T) {
	_, err := parseInstances([]byte(`{"1":{"index":"2"}}`))
	if err == nil || !strings.Contains(err.Error(), "索引不一致") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenRejectsUnsafeIndex(t *testing.T) {
	err := (Client{}).Open(Instance{Index: "all"})
	if err == nil {
		t.Fatal("unsafe index was accepted")
	}
}
