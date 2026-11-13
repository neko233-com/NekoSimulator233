//go:build windows

package mumu

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hideConsole(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

// Discover first respects the explicit setting, then locates an already running MuMu manager.
func Discover() (string, error) {
	if root := os.Getenv("NEKO_MUMU_ROOT"); root != "" {
		if _, err := New(root); err != nil {
			return "", err
		}
		return root, nil
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "MuMuNxMain.exe") {
			continue
		}
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if err != nil {
			continue
		}
		buffer := make([]uint16, 32768)
		n := uint32(len(buffer))
		err = windows.QueryFullProcessImageName(process, 0, &buffer[0], &n)
		windows.CloseHandle(process)
		if err != nil {
			continue
		}
		root := filepath.Dir(filepath.Dir(windows.UTF16ToString(buffer[:n])))
		if _, err := New(root); err == nil {
			return root, nil
		}
	}
	return "", errors.New("未发现正在运行的 MuMu；请先启动 MuMu，或设置环境变量 NEKO_MUMU_ROOT 为安装目录")
}
