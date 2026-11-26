package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/neko233-com/NekoSimulator233/internal/update"
	"golang.org/x/sys/windows"
)

const updateCheckInterval = 10 * time.Minute
const updateRequiredIdle = 2 * time.Minute

type lastInputInfo struct{ Size, Tick uint32 }

func sessionIdle() (time.Duration, error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	info := lastInputInfo{Size: uint32(unsafe.Sizeof(lastInputInfo{}))}
	ok, _, err := user32.NewProc("GetLastInputInfo").Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0, fmt.Errorf("读取会话空闲状态失败：%w", err)
	}
	now, _, _ := kernel32.NewProc("GetTickCount").Call()
	return idleDuration(uint32(now), info.Tick)
}

func idleDuration(now, last uint32) (time.Duration, error) {
	delta := now - last
	if delta > 0x7fffffff {
		return 0, errors.New("会话空闲时间不确定")
	}
	return time.Duration(delta) * time.Millisecond, nil
}

func updateKey(m *update.Manifest) string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("%d:%s%d:%s%d:%s", len(m.Version), m.Version, len(m.SHA256), m.SHA256, len(m.MSI), m.MSI)
}

func lookupUpdate(skipOnce bool) (*update.Manifest, string, string) {
	pending, pendingErr := update.PendingReboot()
	if pendingErr != nil {
		return nil, "", "更新状态读取失败: " + pendingErr.Error()
	}
	if pending {
		return nil, "", "更新已安装，请重启 Windows 完成更新"
	}
	if skipOnce && len(os.Args) > 1 && os.Args[1] == "--skip-update-once" {
		b, _ := os.ReadFile(filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "last-update.txt"))
		return nil, "", string(b)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, "", "更新检查失败: " + err.Error()
	}
	installed := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233", "NekoSimulator233.exe")
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(installed)) {
		return nil, "", ""
	}
	manifest := filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "update.json")
	m, err := update.Load(manifest, version)
	if err != nil {
		return nil, "", "更新检查失败: " + err.Error()
	}
	if m == nil {
		return nil, "", ""
	}
	return m, executable, ""
}

func prepareUpdate() (bool, string) {
	m, executable, message := lookupUpdate(true)
	if m == nil {
		return false, message
	}
	if err := update.Start(m, version, executable); err != nil {
		return false, "更新准备失败: " + err.Error()
	}
	return true, ""
}

type periodicUpdateOps struct {
	idle        func() (time.Duration, error)
	lookup      func() (*update.Manifest, string, string)
	checkOthers func(string) error
	start       func(*update.Manifest, string, string, func() error) error
}

var errUpdateDeferred = errors.New("自动更新条件已变化")

func attemptPeriodicUpdate(suppressed string, ops periodicUpdateOps) (bool, string) {
	idle, err := ops.idle()
	if err != nil || idle < updateRequiredIdle {
		return false, ""
	}
	m, exe, message := ops.lookup()
	if m == nil {
		return false, message
	}
	if updateKey(m) == suppressed {
		return false, "上次自动更新未完成；更改本地更新清单后再试"
	}
	if err := ops.checkOthers(exe); err != nil {
		return false, "更新暂缓：" + err.Error()
	}
	idle, err = ops.idle()
	if err != nil || idle < updateRequiredIdle {
		return false, ""
	}
	guard := func() error {
		idle, err := ops.idle()
		if err != nil || idle < updateRequiredIdle {
			return errUpdateDeferred
		}
		if err := ops.checkOthers(exe); err != nil {
			return fmt.Errorf("%w：%v", errUpdateDeferred, err)
		}
		return nil
	}
	if err := ops.start(m, version, exe, guard); err != nil {
		if errors.Is(err, errUpdateDeferred) {
			return false, "更新暂缓，将在空闲后重试"
		}
		return false, "更新准备失败: " + err.Error()
	}
	return true, ""
}
