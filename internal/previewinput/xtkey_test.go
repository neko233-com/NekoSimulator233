package previewinput

import "testing"

func TestXTUsagePhysicalKeys(t *testing.T) {
	for _, c := range []struct {
		scan     uint8
		extended bool
		want     uint16
	}{
		{0x11, false, 0x1a}, {0x1e, false, 0x04}, {0x02, false, 0x1e}, {0x1d, false, 0xe0}, {0x1d, true, 0xe4},
		{0x48, false, 0x60}, {0x48, true, 0x52}, {0x45, false, 0x48}, {0x45, true, 0x53}, {0x64, false, 0x68},
		{0x36, true, 0xe5}, {0xff, false, 0}, {0x11, true, 0},
	} {
		if got := XTUsage(c.scan, c.extended); got != c.want {
			t.Fatalf("scan %x extended %v: got %x want %x", c.scan, c.extended, got, c.want)
		}
	}
}

func TestKeyMessageRespectsSystemKeys(t *testing.T) {
	if e, ok := KeyMessage(0x0100, 'W', 0x11<<16); !ok || e.Kind != "key_down" || e.Usage != 0x1a || !e.Valid() {
		t.Fatalf("W: %+v %v", e, ok)
	}
	if e, ok := KeyMessage(0x0101, 'W', 0x11<<16); !ok || e.Kind != "key_up" {
		t.Fatalf("W release: %+v %v", e, ok)
	}
	for _, c := range []struct {
		msg    uint32
		vk, lp uintptr
	}{{0x0104, 0x73, 0x3e<<16 | 1<<29}, {0x0100, 0xe5, 0x11 << 16}, {0x0100, 0xe7, 0x11 << 16}, {0x0100, 'W', 0}} {
		if _, ok := KeyMessage(c.msg, c.vk, c.lp); ok {
			t.Fatalf("accepted system or unknown key %+v", c)
		}
	}
}
