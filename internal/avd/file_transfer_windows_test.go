package avd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestImportFileKeepsNameAndQuotesAndroidShellArguments(t *testing.T) {
	local := filepath.Join(t.TempDir(), "中文 'test$().txt")
	if err := os.WriteFile(local, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	adb := func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	}
	result, err := importFile(context.Background(), adb, local, make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}
	dir := "/sdcard/Download/NekoSimulator233/00000000000000000000000000000000"
	if result.GuestPath != dir+"/中文 'test$().txt" || len(calls) != 4 {
		t.Fatalf("result %+v, calls %q", result, calls)
	}
	if !reflect.DeepEqual(calls[2], []string{"push", local, dir + "/.neko-transfer.part"}) {
		t.Fatalf("push arguments %q", calls[2])
	}
	if !reflect.DeepEqual(calls[3], []string{"shell", "mv", "'" + dir + "/.neko-transfer.part'", "'" + dir + "/中文 '\\''test$().txt'"}) {
		t.Fatalf("unsafe move arguments %q", calls[3])
	}
	if _, err := importFile(context.Background(), adb, local, []byte{1}); err == nil {
		t.Fatal("accepted invalid directory token")
	}
	if _, err := importFile(context.Background(), adb, filepath.Dir(local), make([]byte, 16)); err == nil {
		t.Fatal("accepted a directory")
	}
}

func TestImportFileReportsFailureWithoutPublishing(t *testing.T) {
	local := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(local, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls int
	_, err := importFile(context.Background(), func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if args[0] == "push" {
			return nil, errors.New("connection lost")
		}
		return nil, nil
	}, local, make([]byte, 16))
	if err == nil || !strings.Contains(err.Error(), "临时文件") || calls != 3 {
		t.Fatalf("failed transfer was published: calls=%d err=%v", calls, err)
	}
}

func TestExportFilePreservesExistingAndConcurrentTargets(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "导出.txt")
	guest := "/sdcard/Download/用户 '文件.txt"
	var calls [][]string
	adb := func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "pull" {
			return nil, os.WriteFile(args[2], []byte("guest content"), 0600)
		}
		return nil, nil
	}
	if err := exportFile(context.Background(), adb, guest, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "guest content" {
		t.Fatalf("export contents %q: %v", data, err)
	}
	if len(calls) != 2 || calls[0][3] != "'/sdcard/Download/用户 '\\''文件.txt'" {
		t.Fatalf("unsafe guest file test: %q", calls)
	}
	if err := exportFile(context.Background(), adb, guest, destination); err == nil || len(calls) != 2 {
		t.Fatal("existing destination was overwritten or accessed guest")
	}
	race := filepath.Join(root, "race.txt")
	raceADB := func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "pull" {
			if err := os.WriteFile(args[2], []byte("guest"), 0600); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(race, []byte("existing"), 0600)
		}
		return nil, nil
	}
	if err := exportFile(context.Background(), raceADB, guest, race); err == nil {
		t.Fatal("concurrent destination accepted")
	}
	data, _ = os.ReadFile(race)
	if string(data) != "existing" {
		t.Fatal("concurrent destination was overwritten")
	}
	matches, err := filepath.Glob(filepath.Join(root, ".neko-transfer-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary export left behind: %v %v", matches, err)
	}
}

func TestExportFileRejectsInvalidPaths(t *testing.T) {
	adb := func(context.Context, ...string) ([]byte, error) { t.Fatal("invalid path reached adb"); return nil, nil }
	for _, guest := range []string{"relative", "/sdcard/../data", "/sdcard/line\nname", "/"} {
		if err := exportFile(context.Background(), adb, guest, filepath.Join(t.TempDir(), "target")); err == nil {
			t.Fatalf("accepted %q", guest)
		}
	}
}

func TestCanceledTransferDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "input.txt")
	if err := os.WriteFile(local, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	_, err := importFile(ctx, func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if args[0] == "push" {
			cancel()
		}
		return nil, nil
	}, local, make([]byte, 16))
	if !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatalf("canceled import published: %d %v", calls, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	destination := filepath.Join(root, "output.txt")
	err = exportFile(ctx, func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "pull" {
			if err := os.WriteFile(args[2], []byte("output"), 0600); err != nil {
				return nil, err
			}
			cancel()
		}
		return nil, nil
	}, "/sdcard/Download/file.txt", destination)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not reported: %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled export published: %v", err)
	}
}
