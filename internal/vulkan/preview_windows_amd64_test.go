//go:build windows && amd64

package vulkan

import (
	"testing"
	"unsafe"
)

func TestPreviewWin64ABI(t *testing.T) {
	if got := unsafe.Sizeof(previewClass{}); got != 80 {
		t.Fatalf("WNDCLASSEXW size %d", got)
	}
	if got := unsafe.Sizeof(previewMessage{}); got != 48 {
		t.Fatalf("MSG size %d", got)
	}
}
