package update

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Start stages a verified local MSI and starts an independent updater. The caller
// must exit after a successful start so Windows Installer can replace its files.
func Start(m *Manifest, currentVersion, executable string) error {
	return StartGuarded(m, currentVersion, executable, nil)
}

// StartGuarded rechecks the caller's safety conditions after staging and
// before launching the updater. A rejected guard removes its staging folder.
func StartGuarded(m *Manifest, currentVersion, executable string, guard func() error) error {
	if m == nil {
		return errors.New("missing update manifest")
	}
	newerVersion, err := newer(m.Version, currentVersion)
	if err != nil || !newerVersion {
		return errors.New("update is not newer than the current version")
	}
	// Keep the MSI beside its configured source: the Windows Installer service
	// must be able to resolve the path from its own execution context.
	dir, err := os.MkdirTemp(filepath.Dir(m.MSI), ".neko-update-")
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			_ = os.RemoveAll(dir)
		}
	}()
	stage := filepath.Join(dir, "update.msi")
	src, err := os.Open(m.MSI)
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = src.Close()
		return err
	}
	_, copyErr := io.Copy(dst, io.LimitReader(src, 2<<30))
	closeErr := dst.Close()
	_ = src.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = checkFile(stage, m.SHA256, m.Version); err != nil {
		return err
	}
	worker := filepath.Join(dir, "updater.exe")
	src, err = os.Open(executable)
	if err != nil {
		return err
	}
	dst, err = os.OpenFile(worker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		_ = src.Close()
		return err
	}
	_, copyErr = io.Copy(dst, src)
	closeErr = dst.Close()
	_ = src.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	cmd := exec.Command(worker, "--update-worker", stage, m.SHA256, m.Version, executable, strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if guard != nil {
		if err = guard(); err != nil {
			return err
		}
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	started = true
	return nil
}

func checkFile(path, hash, expectedVersion string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = VerifyHash(f, hash)
	_ = f.Close()
	if err != nil {
		return err
	}
	return VerifyPackage(path, expectedVersion)
}

// Worker waits for its originating process, installs a verified MSI silently,
// then relaunches the application. A failed update relaunches the old app once
// with update checks disabled to avoid a restart loop.
func Worker(args []string) (result error) {
	if len(args) != 5 {
		return errors.New("invalid update worker arguments")
	}
	stage, hash, targetVersion, executable := args[0], args[1], args[2], args[3]
	pid, err := strconv.ParseUint(args[4], 10, 32)
	if err != nil || pid == 0 {
		return errors.New("invalid parent process")
	}
	local := os.Getenv("LOCALAPPDATA")
	expectedExe := filepath.Join(local, "NekoSimulator233", "NekoSimulator233.exe")
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(expectedExe)) {
		return errors.New("unexpected installed executable")
	}
	relaunch := false
	defer func() {
		message := "更新已安装"
		if result != nil {
			message = "更新未完成: " + result.Error()
		}
		_ = os.WriteFile(filepath.Join(local, "NekoSimulator233-data", "last-update.txt"), []byte(message), 0600)
		if relaunch {
			cmd := exec.Command(executable, "--skip-update-once")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if cmd.Start() == nil {
				_ = cmd.Process.Release()
			}
		}
	}()
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == nil {
		wait, waitErr := windows.WaitForSingleObject(parent, 30000)
		_ = windows.CloseHandle(parent)
		if waitErr != nil || wait != windows.WAIT_OBJECT_0 {
			return errors.New("application did not exit for update")
		}
	} else if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return err
	}
	relaunch = true
	// Windows mutexes are owned by OS threads, not Go goroutines.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, _ := windows.UTF16PtrFromString(`Local\NekoSimulator233-MSI-update`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(mutex)
	wait, err := windows.WaitForSingleObject(mutex, 120000)
	if err != nil || (wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED) {
		return errors.New("another update did not finish")
	}
	defer windows.ReleaseMutex(mutex)
	installedVersion, err := installedProductVersion()
	if err != nil {
		return err
	}
	needsUpdate, err := newer(targetVersion, installedVersion)
	if err != nil {
		return err
	}
	if !needsUpdate {
		return nil
	}
	if err = checkFile(stage, hash, targetVersion); err != nil {
		return err
	}
	if err = ensureNotRunning(executable); err != nil {
		return err
	}
	cmd := exec.Command("msiexec.exe", "/i", stage, "/qn", "/norestart")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	err = cmd.Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3010 {
			relaunch = false
			if markErr := markRebootRequired(); markErr != nil {
				return fmt.Errorf("update installed; Windows restart is required; unable to save reboot state: %w", markErr)
			}
			return errors.New("update installed; Windows restart is required")
		}
		return fmt.Errorf("MSI update failed: %w", err)
	}
	return nil
}

func ensureNotRunning(executable string) error {
	return checkRunning(executable, 0)
}

// EnsureNoOtherProcesses lets the active UI defer an update before it exits.
// The worker repeats the check after the UI exits to handle races.
func EnsureNoOtherProcesses(executable string) error {
	return checkRunning(executable, uint32(os.Getpid()))
}

func checkRunning(executable string, skipPID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafeSizeProcessEntry32())}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == skipPID {
			continue
		}
		name := windows.UTF16ToString(entry.ExeFile[:])
		if !strings.EqualFold(name, "NekoSimulator233.exe") && !strings.EqualFold(name, "neko-mcp.exe") {
			continue
		}
		p, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if e != nil {
			return fmt.Errorf("cannot inspect another %s process: %w", name, e)
		}
		buf := make([]uint16, 32768)
		size := uint32(len(buf))
		e = windows.QueryFullProcessImageName(p, 0, &buf[0], &size)
		_ = windows.CloseHandle(p)
		if e != nil {
			return e
		}
		path := windows.UTF16ToString(buf[:size])
		if strings.EqualFold(filepath.Dir(path), filepath.Dir(executable)) {
			return fmt.Errorf("%s is still running; update deferred", name)
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	return nil
}

// Kept in a function to make the structure-size conversion explicit on Windows.
func unsafeSizeProcessEntry32() uintptr { return unsafe.Sizeof(windows.ProcessEntry32{}) }
