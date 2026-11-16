package avd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOfficialOptionalAppDownloadUsesSelectedInstance(t *testing.T) {
	root := t.TempDir()
	adb := filepath.Join(root, "platform-tools", "adb.exe")
	if err := os.MkdirAll(filepath.Dir(adb), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(self, adb); err != nil {
		t.Skipf("cannot link isolated fake adb: %v", err)
	}
	record := filepath.Join(root, "args.json")
	t.Setenv("NEKO_TEST_FAKE_ADB_RECORD", record)
	t.Setenv("NEKO_TEST_FAKE_ADB_INTENT", "Status: ok")
	m := Manager{SDK: root}
	instance := Instance{Serial: "emulator-5554"}
	for _, tc := range []struct{ id, url string }{
		{"wechat", "https://weixin.qq.com/"},
		{"douyin", "https://www.douyin.com/downloadpage/app"},
	} {
		if err := m.OpenOfficialAppDownload(context.Background(), instance, tc.id); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		if err = json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		want := []string{"-s", instance.Serial, "shell", "am", "start", "-W", "-a", "android.intent.action.VIEW", "-d", tc.url}
		if !slices.Equal(got, want) {
			t.Fatalf("arguments %q, want %q", got, want)
		}
	}
	if err := m.OpenOfficialAppDownload(context.Background(), instance, "https://example.com"); err == nil {
		t.Fatal("arbitrary URL accepted")
	}
	t.Setenv("NEKO_TEST_FAKE_ADB_INTENT", "Error: Activity not started")
	if err := m.OpenOfficialAppDownload(context.Background(), instance, "wechat"); err == nil {
		t.Fatal("failed Android intent reported success")
	}
}
