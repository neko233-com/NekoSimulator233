//go:build windows

package avd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPhoneIdentityPropArgs(t *testing.T) {
	p := PhoneIdentity{Brand: "OPPO", Manufacturer: "OPPO", Model: "A93s", Device: "odin-a", IMEI: "863323045635028", VDID: "abc-123"}
	args := p.PropArgs()
	joined := strings.Join(args, " ")
	for _, want := range []string{"ro.product.brand=OPPO", "ro.product.model=A93s", "ro.product.device=odin-a", "ro.neko.vdid=abc-123"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
	if len(args) != 12 {
		t.Fatalf("expected 6 prop pairs, got %d args: %v", len(args), args)
	}
}

func TestPhoneIdentityValidationAndStorage(t *testing.T) {
	root := t.TempDir()
	m := Manager{SDK: `D:\Android\Sdk`, Home: filepath.Join(root, "avd"), Name: "demo"}
	if err := m.SetPhoneIdentity(PhoneIdentity{Model: "bad model\n"}); err == nil {
		t.Fatal("control characters must be rejected")
	}
	if err := m.SetPhoneIdentity(PhoneIdentity{IMEI: "123"}); err == nil {
		t.Fatal("short IMEI must be rejected")
	}
	want := PhoneIdentity{Brand: "Samsung", Model: "K50", IMEI: "860130046437277"}
	if err := m.SetPhoneIdentity(want); err != nil {
		t.Fatal(err)
	}
	got, err := m.PhoneIdentity()
	if err != nil || got != want {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}
