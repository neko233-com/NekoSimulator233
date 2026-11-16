package main

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
	"github.com/neko233-com/NekoSimulator233/internal/mumu"
)

func TestInstanceView(t *testing.T) {
	a := &app{message: "已连接", instances: []mumu.Instance{{Index: "0", Name: "测试实例", AndroidVersion: "15.0", AndroidStarted: true}}}
	tester := ui.NewTester(a.view, 760, 500)
	for _, want := range []string{"NekoSimulator233", "已连接", "安装 Google Play 镜像…", "资源设置…", "系统兼容信息", "安装 APK…", "更新 APK…", "安装 TapTap…", "微信官方下载…", "抖音官方下载…", "备份选中实例…", "恢复备份到新实例…", "测试实例  ·  Android 15.0  ·  运行中", "打开"} {
		if !tester.HasText(want) {
			t.Errorf("missing %q in %q", want, tester.Texts())
		}
	}
}

func TestCloseCancelsSDKInstall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &app{sdkCancel: cancel}
	if !a.cancelSDKForClose() || !a.closeAfterSDK || ctx.Err() != context.Canceled {
		t.Fatal("close did not cancel SDK installation")
	}
	a.sdkCancel = nil
	if a.cancelSDKForClose() {
		t.Fatal("close blocked without SDK installation")
	}
}

func TestIndependentInstanceChoicesAndKeymaps(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if keymapPathForInstance(avd.DefaultName) != keymapPath() {
		t.Fatal("default instance no longer uses the existing keymap")
	}
	if keymapPathForInstance("Alpha") == keymapPath() || keymapPathForInstance("Alpha") == keymapPathForInstance("Beta") || keymapPathForInstance("Alpha") != keymapPathForInstance("alpha") {
		t.Fatal("instance keymap paths are not isolated")
	}
	a := &app{selected: "Alpha"}
	a.setAvailable([]avd.InstanceSummary{{Name: avd.DefaultName, State: "not_created"}, {Name: "Alpha", State: "stopped"}, {Name: "Incomplete", State: "unknown"}})
	if a.selectedName() != "Alpha" || len(a.instanceChoices()) != 2 {
		t.Fatalf("incorrect choices: %+v", a.instanceChoices())
	}
	a.setAvailable([]avd.InstanceSummary{{Name: avd.DefaultName, State: "not_created"}})
	if a.selectedName() != avd.DefaultName {
		t.Fatal("missing selection was not reset")
	}
}

func TestIndependentInstanceSelectionInView(t *testing.T) {
	a := &app{selected: avd.DefaultName, available: []avd.InstanceSummary{{Name: avd.DefaultName, State: "stopped"}, {Name: "Alpha", State: "stopped"}}}
	tester := ui.NewTester(a.view, 760, 600)
	if err := tester.Click("独立实例"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	if a.selectedName() != "Alpha" {
		t.Fatalf("selection = %q", a.selectedName())
	}
}

func TestNewInstanceSystemChoice(t *testing.T) {
	if api, err := newSystemAPI(systemAndroid11); err != nil || api != 30 {
		t.Fatalf("Android 11 choice = %d, %v", api, err)
	}
	if api, err := newSystemAPI(systemAuto); err != nil || api != 0 {
		t.Fatalf("default choice = %d, %v", api, err)
	}
	if _, err := newSystemAPI("unknown"); err == nil {
		t.Fatal("unsupported system choice accepted")
	}
}

func TestNewInstanceSystemChoiceInView(t *testing.T) {
	a := &app{}
	tester := ui.NewTester(a.view, 760, 650)
	if err := tester.Click("新实例系统"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click(systemAndroid11); err != nil {
		t.Fatal(err)
	}
	if a.newSystem != systemAndroid11 {
		t.Fatalf("system selection = %q", a.newSystem)
	}
}

func TestResourceSettingsViewAndValidation(t *testing.T) {
	s := &resourceSettings{cores: 4, ram: 2048}
	tester := ui.NewTester(s.view, 500, 300)
	for _, want := range []string{"独立实例资源设置", "CPU 核数", "内存 MiB", "保存设置"} {
		if !tester.HasText(want) {
			t.Errorf("missing %q in %q", want, tester.Texts())
		}
	}
	s.ram = 511
	s.save()
	if s.message == "" || s.busy {
		t.Fatal("invalid resources were not rejected")
	}
}

func TestRestoreRequiresNewNameBeforeOpeningDialog(t *testing.T) {
	a := &app{newInstance: "   "}
	a.transferBackup(true)
	if a.busy || a.backupCancel != nil || a.message == "" {
		t.Fatalf("empty restore name was not rejected: %+v", a)
	}
}

func TestCloseCancelsBackupBeforeWindowCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &app{backupCancel: cancel}
	if !a.cancelBackupForClose() || !a.closeAfterBackup || ctx.Err() != context.Canceled {
		t.Fatal("window close did not request backup cancellation")
	}
	a.backupCancel = nil
	if a.cancelBackupForClose() {
		t.Fatal("window close was blocked with no backup")
	}
}
