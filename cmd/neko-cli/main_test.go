//go:build windows

package main

import "testing"

func TestRunHelpAndVersion(t *testing.T) {
	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if err := run([]string{"version"}); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if err := run([]string{"unknown-subcommand"}); err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}
