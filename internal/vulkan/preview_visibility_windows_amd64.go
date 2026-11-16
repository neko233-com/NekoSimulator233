package vulkan

// previewVisibility prevents frames queued before minimization from becoming
// interactive after restore. Only the preview window thread accesses it.
type previewVisibility struct {
	known, visible, awaitClear, ready bool
}

func (s *previewVisibility) change(visible bool) bool {
	if s.known && s.visible == visible {
		return false
	}
	if s.known && s.visible && !visible {
		s.awaitClear = true
	}
	s.known, s.visible, s.ready = true, visible, false
	return true
}

func (s *previewVisibility) clear()           { s.awaitClear, s.ready = false, false }
func (s *previewVisibility) canPresent() bool { return s.visible && !s.awaitClear }

// presented returns true only for the first frame after a clear or restore.
func (s *previewVisibility) presented() bool {
	if s.ready {
		return false
	}
	s.ready = true
	return true
}
