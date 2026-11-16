//go:build windows

package avd

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/neko233-com/NekoSimulator233/internal/keymap"
	"golang.org/x/sys/windows"
)

type keymapSource struct{ name, path string }

func validArchiveKeymapName(name string) bool {
	if name == "default.json" {
		return true
	}
	profile, ok := strings.CutPrefix(name, "profiles/")
	if !ok || !strings.HasSuffix(profile, ".json") {
		return false
	}
	return keymap.ValidateProfileName(strings.TrimSuffix(profile, ".json")) == nil
}

func safeKeymapDirectory(path string) (bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return false, errors.New("键位方案目录无效或包含链接")
	}
	return true, nil
}

func keymapSources(m Manager, avdDir string) ([]keymapSource, error) {
	base, err := keymap.Resolve(filepath.Dir(m.Home), avdDir, m.Name, defaultName)
	if err != nil {
		return nil, err
	}
	var sources []keymapSource
	if present, err := existsOrLink(base); err != nil {
		return nil, err
	} else if present {
		sources = append(sources, keymapSource{name: "default.json", path: base})
	}
	profiles := keymap.ProfilesDir(base)
	for _, dir := range []string{filepath.Dir(profiles), profiles} {
		exists, err := safeKeymapDirectory(dir)
		if err != nil {
			return nil, err
		}
		if !exists {
			return sources, nil
		}
	}
	entries, err := os.ReadDir(profiles)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if err := keymap.ValidateProfileName(name); err != nil {
			return nil, fmt.Errorf("键位方案名称无效：%w", err)
		}
		folded := strings.ToLower(name)
		if seen[folded] {
			return nil, errors.New("键位方案名称重复")
		}
		seen[folded] = true
		sources = append(sources, keymapSource{name: "profiles/" + name + ".json", path: filepath.Join(profiles, entry.Name())})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].name < sources[j].name })
	return sources, nil
}

// writeKeymaps snapshots only validated keymaps while the global keymap mutex
// is held; it never includes unrelated files from the user's data directory.
func writeKeymaps(ctx context.Context, zw *zip.Writer, m Manager, avdDir string) ([]backupFile, error) {
	unlock, err := keymap.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	sources, err := keymapSources(m, avdDir)
	if err != nil {
		return nil, err
	}
	var result []backupFile
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := openOfflineImage(source.path)
		if err != nil {
			return nil, fmt.Errorf("无法读取键位方案：%w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(f, keymap.MaxFileSize+1))
		closeErr := f.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if _, err := keymap.Parse(data); err != nil {
			return nil, fmt.Errorf("键位方案 %s 无效：%w", source.name, err)
		}
		header := &zip.FileHeader{Name: "keymaps/" + source.name, Method: zip.Store}
		header.SetMode(0600)
		w, err := zw.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err = w.Write(data); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		result = append(result, backupFile{Name: source.name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	}
	return result, nil
}
