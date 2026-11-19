package main

import (
	"path/filepath"
	"testing"
)

func TestViewerGeometryRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	want := viewerGeometry{Left: 941, Top: 294, Width: 1707, Height: 1000, Monitor: 0, Maximized: false}
	if err := saveViewerGeometry("demo", want); err != nil {
		t.Fatal(err)
	}
	got, ok := loadViewerGeometry("demo")
	if !ok || got != want {
		t.Fatalf("geometry mismatch: %+v ok=%v", got, ok)
	}
	p, err := viewerGeometryPath("demo")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(p)) != "demo.avd" || filepath.Base(p) != "neko-viewer.json" {
		t.Fatalf("unexpected geometry path: %s", p)
	}
}

func TestViewerGeometryRejectsInvalid(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := saveViewerGeometry("../escape", viewerGeometry{Width: 800, Height: 600}); err == nil {
		t.Fatal("path traversal must be rejected")
	}
	if err := saveViewerGeometry("demo", viewerGeometry{Width: 10, Height: 10}); err == nil {
		t.Fatal("tiny window must be rejected")
	}
	if _, ok := loadViewerGeometry("missing"); ok {
		t.Fatal("missing geometry should not load")
	}
}
