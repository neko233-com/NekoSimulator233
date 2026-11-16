package vulkan

import "testing"

func TestPreviewVisibilityRejectsQueuedFramesAfterRestore(t *testing.T) {
	var s previewVisibility
	if !s.change(false) || s.canPresent() || !s.change(true) || !s.canPresent() {
		t.Fatal("initial visibility transition failed")
	}
	if !s.presented() || s.presented() {
		t.Fatal("readiness should be announced once")
	}
	if !s.change(false) || s.canPresent() || !s.change(true) || s.canPresent() {
		t.Fatal("queued frame accepted before clear")
	}
	if s.change(true) || s.canPresent() {
		t.Fatal("duplicate visibility bypassed clear")
	}
	s.clear()
	if !s.canPresent() || !s.presented() || s.presented() {
		t.Fatal("fresh frame did not restore readiness")
	}
}
