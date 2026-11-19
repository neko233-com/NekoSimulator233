package audioforward

import (
	"context"
	"testing"
	"time"
)

func TestRelayLatency(t *testing.T) {
	listener, addr, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(ctx, listener) }()
	report, err := Stream(ctx, addr, 40, 640, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if report.Chunks != 40 || report.Bytes != 40*640 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.AvgOneWayMS < 0 || report.P95OneWayMS > report.MaxOneWayMS || report.MaxOneWayMS < report.AvgOneWayMS {
		t.Fatalf("bad latency stats: %+v", report)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("serve failed: %v", err)
	}
}
