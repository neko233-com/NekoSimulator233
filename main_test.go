package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/mumu"
)

func TestInstanceView(t *testing.T) {
	a := &app{message: "已连接", instances: []mumu.Instance{{Index: "0", Name: "测试实例", AndroidVersion: "15.0", AndroidStarted: true}}}
	tester := ui.NewTester(a.view, 760, 500)
	for _, want := range []string{"NekoSimulator233", "已连接", "资源设置…", "安装 APK…", "更新 APK…", "测试实例  ·  Android 15.0  ·  运行中", "打开"} {
		if !tester.HasText(want) {
			t.Errorf("missing %q in %q", want, tester.Texts())
		}
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
