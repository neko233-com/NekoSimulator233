//go:build windows

package avd

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

const maxBackupFile = int64(128 << 30)
const maxBackupTotal = int64(256 << 30)

var backupImages = map[string]bool{
	"userdata-qemu.img": true, "userdata-qemu.img.qcow2": true,
	"encryptionkey.img": true, "encryptionkey.img.qcow2": true,
	"sdcard.img": true, "sdcard.img.qcow2": true,
}

type backupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type backupSettings struct {
	API         int    `json:"api"`
	SystemBuild int64  `json:"system_build"`
	Cores       int    `json:"cores"`
	RAM         int    `json:"ram_mib"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Density     int    `json:"density"`
	DataSize    string `json:"data_size"`
}
type backupManifest struct {
	Format   int            `json:"format"`
	Settings backupSettings `json:"settings"`
	Files    []backupFile   `json:"files"`
}

func systemImageAPI(values map[string]string) (int, error) {
	image := strings.ToLower(strings.ReplaceAll(strings.Trim(values["image.sysdir.1"], `\/ `), `\`, "/"))
	switch image {
	case "system-images/android-34/google_apis/x86_64":
		return 34, nil
	case "system-images/android-35/google_apis/x86_64":
		return 35, nil
	default:
		return 0, errors.New("仅支持 Android 34/35 google_apis x86_64 实例")
	}
}

func settingsFromINI(values map[string]string) (backupSettings, error) {
	for _, key := range []string{"disk.dataPartition.path", "disk.cachePartition.path", "hw.sdCard.path"} {
		if strings.TrimSpace(values[key]) != "" {
			return backupSettings{}, fmt.Errorf("不支持迁移自定义磁盘路径 %s", key)
		}
	}
	api, err := systemImageAPI(values)
	if err != nil {
		return backupSettings{}, err
	}
	parse := func(name string, fallback, minValue, maxValue int) (int, error) {
		v := strings.TrimSpace(values[name])
		if v == "" {
			return fallback, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < minValue || n > maxValue {
			return 0, fmt.Errorf("实例配置 %s 超出支持范围", name)
		}
		return n, nil
	}
	cores, err := parse("hw.cpu.ncore", 4, 1, 64)
	if err != nil {
		return backupSettings{}, err
	}
	ram, err := parse("hw.ramSize", 2048, 512, 131072)
	if err != nil {
		return backupSettings{}, err
	}
	w, err := parse("hw.lcd.width", 720, 320, 8192)
	if err != nil {
		return backupSettings{}, err
	}
	h, err := parse("hw.lcd.height", 1280, 320, 8192)
	if err != nil {
		return backupSettings{}, err
	}
	density, err := parse("hw.lcd.density", 240, 72, 1000)
	if err != nil {
		return backupSettings{}, err
	}
	size := strings.TrimSpace(values["disk.dataPartition.size"])
	if size == "" {
		size = "6G"
	}
	if len(size) > 20 || strings.Trim(size, "0123456789KkMmGgTt") != "" {
		return backupSettings{}, errors.New("实例数据盘大小无效")
	}
	settings := backupSettings{API: api, Cores: cores, RAM: ram, Width: w, Height: h, Density: density, DataSize: size}
	if err := validateBackupSettings(settings); err != nil {
		return backupSettings{}, err
	}
	return settings, nil
}

// openOfflineImage denies concurrent write/delete sharing while the backup is
// read. Existing emulator write handles cause a sharing violation, even if its
// discovery metadata is missing or inaccessible.
func openOfflineImage(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, errors.New("实例数据正在使用，请先关闭独立实例")
		}
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("不支持链接或目录形式的实例磁盘")
	}
	return os.NewFile(uintptr(h), path), nil
}

// guardSystemBuild prevents the emulator from silently recreating an existing
// data overlay after an SDK image update or a missing version cache. Locks are
// held through startup so the two version files cannot change after inspection.
func (m Manager) guardSystemBuild() (func(), error) {
	dir := filepath.Join(m.Home, m.Name+".avd")
	present, err := existsOrLink(filepath.Join(dir, "userdata-qemu.img.qcow2"))
	if err != nil {
		return nil, fmt.Errorf("无法检查已有数据盘，已阻止启动：%w", err)
	}
	if !present {
		return func() {}, nil
	}
	values, err := parseINI(filepath.Join(dir, "config.ini"))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(values["disk.dataPartition.path"]) != "" {
		return nil, errors.New("实例使用自定义数据盘路径，无法安全检查系统镜像版本")
	}
	api, err := systemImageAPI(values)
	if err != nil {
		return nil, fmt.Errorf("无法安全检查系统镜像版本：%w", err)
	}
	cache, err := openOfflineImage(filepath.Join(dir, "version_num.cache"))
	if err != nil {
		return nil, fmt.Errorf("数据盘存在但版本缓存不可读，已阻止可能清空数据的启动：%w", err)
	}
	propsPath := filepath.Join(m.SDK, "system-images", fmt.Sprintf("android-%d", api), "google_apis", "x86_64", "build.prop")
	propsLock, err := openOfflineImage(propsPath)
	if err != nil {
		cache.Close()
		return nil, fmt.Errorf("系统镜像版本不可读，已阻止启动：%w", err)
	}
	release := func() { _ = propsLock.Close(); _ = cache.Close() }
	cachedBytes, err := io.ReadAll(io.LimitReader(cache, 129))
	if err != nil || len(cachedBytes) > 128 {
		release()
		return nil, errors.New("实例版本缓存无效，已阻止启动")
	}
	cached, err := strconv.ParseInt(strings.TrimSpace(string(cachedBytes)), 10, 32)
	if err != nil || cached <= 0 {
		release()
		return nil, errors.New("实例版本缓存无效，已阻止启动")
	}
	props, err := parseINI(propsPath)
	if err != nil {
		release()
		return nil, fmt.Errorf("系统镜像版本不可读，已阻止启动：%w", err)
	}
	current, err := strconv.ParseInt(strings.TrimSpace(props["ro.build.version.incremental"]), 10, 32)
	if err != nil || current != cached {
		release()
		return nil, errors.New("系统镜像版本与实例数据不匹配，继续启动可能清空数据；请恢复匹配的系统镜像后备份")
	}
	return release, nil
}

type lockedImage struct {
	file *os.File
	size int64
}

// validateBackings accepts only self-contained, acyclic relative QCOW2 chains.
func validateBackings(images map[string]lockedImage) error {
	edges := map[string]string{}
	for name, item := range images {
		var head [20]byte
		n, err := item.file.ReadAt(head[:], 0)
		if err != nil && err != io.EOF {
			return err
		}
		isQCOW := n >= 4 && string(head[:4]) == "QFI\xfb"
		if !isQCOW {
			if strings.HasSuffix(name, ".qcow2") {
				return fmt.Errorf("磁盘 %s 不是 QCOW2", name)
			}
			continue
		}
		if n < len(head) {
			return errors.New("QCOW2 头部不完整")
		}
		version := binary.BigEndian.Uint32(head[4:8])
		if version != 2 && version != 3 {
			return errors.New("不支持的 QCOW2 版本")
		}
		offset := binary.BigEndian.Uint64(head[8:16])
		length := binary.BigEndian.Uint32(head[16:20])
		if length == 0 {
			if offset != 0 {
				return errors.New("QCOW2 基底字段无效")
			}
			continue
		}
		if length > 1024 || offset > uint64(item.size) || uint64(length) > uint64(item.size)-offset {
			return errors.New("QCOW2 基底路径无效")
		}
		buf := make([]byte, length)
		if _, err = item.file.ReadAt(buf, int64(offset)); err != nil {
			return err
		}
		base := string(buf)
		if !backupImages[base] || base == name || strings.ContainsAny(base, `\/:`) {
			return errors.New("QCOW2 引用了备份之外的基底磁盘")
		}
		if _, ok := images[base]; !ok {
			return errors.New("QCOW2 基底磁盘缺失")
		}
		edges[name] = base
	}
	for name := range edges {
		seen := map[string]bool{}
		for p := name; p != ""; p = edges[p] {
			if seen[p] {
				return errors.New("QCOW2 基底链存在循环")
			}
			seen[p] = true
		}
	}
	return nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 1<<20)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
			if w != n {
				return total, io.ErrShortWrite
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

func inDirectory(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func ownedAVD(m Manager) (string, error) {
	dir, err := filepath.Abs(filepath.Join(m.Home, m.Name+".avd"))
	if err != nil {
		return "", err
	}
	ini, err := parseINI(filepath.Join(m.Home, m.Name+".ini"))
	if err != nil {
		return "", errors.New("未找到完整的项目实例")
	}
	indexed, err := filepath.Abs(ini["path"])
	if err != nil || !strings.EqualFold(filepath.Clean(indexed), filepath.Clean(dir)) {
		return "", errors.New("实例索引指向其他位置")
	}
	return dir, nil
}

// ExportBackup creates a new unencrypted, integrity-checked archive. It never
// modifies the original AVD and requires all source disks to be offline.
func (m Manager) ExportBackup(ctx context.Context, target string) error {
	if err := m.validate(); err != nil {
		return err
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Ext(target), ".nekobak") {
		return errors.New("备份文件必须以 .nekobak 结尾")
	}
	unlock, err := m.lockLifecycle(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	dir, err := ownedAVD(m)
	if err != nil {
		return err
	}
	if inDirectory(target, dir) {
		return errors.New("备份文件不能放在实例目录内")
	}
	if _, err = os.Lstat(target); err == nil {
		return errors.New("备份文件已存在，不会覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err = m.discover(); err == nil {
		return errors.New("实例仍在运行，请先关闭")
	} else if !errors.Is(err, errInstanceNotRunning) {
		return err
	}
	configLock, err := openOfflineImage(filepath.Join(dir, "config.ini"))
	if err != nil {
		return err
	}
	defer configLock.Close()
	config, err := parseINI(filepath.Join(dir, "config.ini"))
	if err != nil {
		return err
	}
	settings, err := settingsFromINI(config)
	if err != nil {
		return err
	}
	resources, err := resourcesOverride(dir)
	if err != nil {
		return err
	}
	if resources != nil {
		settings.Cores, settings.RAM = resources.Cores, resources.RAMMiB
	}
	// The emulator recreates QCOW2 overlays if this cache is absent or differs
	// from the system image build. Keep its exact value to avoid a silent wipe.
	versionFile, err := openOfflineImage(filepath.Join(dir, "version_num.cache"))
	if err != nil {
		return fmt.Errorf("无法读取实例系统镜像版本，不能安全备份：%w", err)
	}
	versionBytes, err := io.ReadAll(io.LimitReader(versionFile, 129))
	_ = versionFile.Close()
	if err != nil || len(versionBytes) > 128 {
		return errors.New("实例系统镜像版本无效")
	}
	settings.SystemBuild, err = strconv.ParseInt(strings.TrimSpace(string(versionBytes)), 10, 32)
	if err != nil || settings.SystemBuild <= 0 {
		return errors.New("实例系统镜像版本无效")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && (strings.HasSuffix(name, ".img") || strings.HasSuffix(name, ".img.qcow2")) && !backupImages[name] && name != "cache.img" && name != "cache.img.qcow2" {
			return fmt.Errorf("发现未支持的实例磁盘 %s", name)
		}
	}
	images := map[string]lockedImage{}
	defer func() {
		for _, v := range images {
			_ = v.file.Close()
		}
	}()
	for name := range backupImages {
		f, e := openOfflineImage(filepath.Join(dir, name))
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return e
		}
		if info.Size() < 0 || info.Size() > maxBackupFile {
			f.Close()
			return errors.New("实例磁盘超过备份大小限制")
		}
		images[name] = lockedImage{file: f, size: info.Size()}
	}
	if _, ok := images["userdata-qemu.img"]; !ok {
		return errors.New("实例用户数据盘缺失")
	}
	if err = validateBackings(images); err != nil {
		return err
	}
	var total int64
	names := make([]string, 0, len(images))
	for name, v := range images {
		if v.size > maxBackupTotal-total {
			return errors.New("备份超过大小限制")
		}
		total += v.size
		names = append(names, name)
	}
	sort.Strings(names)
	tmp, err := os.CreateTemp(filepath.Dir(target), ".nekobak-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)
	manifest := backupManifest{Format: 2, Settings: settings}
	for _, name := range names {
		item := images[name]
		header := &zip.FileHeader{Name: "images/" + name, Method: zip.Store}
		header.SetMode(0600)
		w, e := zw.CreateHeader(header)
		if e != nil {
			zw.Close()
			tmp.Close()
			return e
		}
		hash := sha256.New()
		if _, e = item.file.Seek(0, io.SeekStart); e != nil {
			zw.Close()
			tmp.Close()
			return e
		}
		n, e := copyContext(ctx, io.MultiWriter(w, hash), item.file)
		if e != nil || n != item.size {
			zw.Close()
			tmp.Close()
			if e != nil {
				return e
			}
			return errors.New("备份时磁盘大小变化")
		}
		manifest.Files = append(manifest.Files, backupFile{Name: name, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		zw.Close()
		tmp.Close()
		return err
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Store})
	if err == nil {
		_, err = w.Write(b)
	}
	if err != nil {
		zw.Close()
		tmp.Close()
		return err
	}
	if err = zw.Close(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	from, _ := windows.UTF16PtrFromString(tmp.Name())
	to, _ := windows.UTF16PtrFromString(target)
	if err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("保存备份失败，未覆盖目标：%w", err)
	}
	return nil
}

func validateBackupSettings(s backupSettings) error {
	if s.API != 34 && s.API != 35 {
		return errors.New("备份的 Android 版本不受支持")
	}
	if s.Cores < 1 || s.Cores > 64 || s.RAM < 512 || s.RAM > 131072 || s.Width < 320 || s.Width > 8192 || s.Height < 320 || s.Height > 8192 || s.Density < 72 || s.Density > 1000 {
		return errors.New("备份的硬件配置超出支持范围")
	}
	text := s.DataSize
	if text == "" || len(text) > 20 {
		return errors.New("备份的数据盘大小无效")
	}
	multiplier := uint64(1)
	last := text[len(text)-1]
	switch last {
	case 'k', 'K':
		multiplier = 1 << 10
	case 'm', 'M':
		multiplier = 1 << 20
	case 'g', 'G':
		multiplier = 1 << 30
	case 't', 'T':
		multiplier = 1 << 40
	}
	if multiplier != 1 {
		text = text[:len(text)-1]
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil || n == 0 || n > uint64(1<<40)/multiplier {
		return errors.New("备份的数据盘大小无效")
	}
	return nil
}

func existsOrLink(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// ImportBackup creates an entirely new owned AVD. Existing instances and
// partially existing target paths are never replaced.
func (m Manager) ImportBackup(ctx context.Context, source string) error {
	if err := m.validate(); err != nil {
		return err
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Ext(source), ".nekobak") {
		return errors.New("请选择 .nekobak 备份")
	}
	unlock, err := m.lockLifecycle(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	finalDir, err := filepath.Abs(filepath.Join(m.Home, m.Name+".avd"))
	if err != nil {
		return err
	}
	finalINI, err := filepath.Abs(filepath.Join(m.Home, m.Name+".ini"))
	if err != nil {
		return err
	}
	for _, p := range []string{finalDir, finalINI} {
		present, e := existsOrLink(p)
		if e != nil {
			return e
		}
		if present {
			return errors.New("目标实例已存在或不完整，导入不会覆盖任何文件")
		}
	}
	if inDirectory(source, finalDir) {
		return errors.New("备份不能位于目标实例目录内")
	}
	z, err := zip.OpenReader(source)
	if err != nil {
		return fmt.Errorf("打开备份失败：%w", err)
	}
	defer z.Close()
	if len(z.File) > len(backupImages)+1 {
		return errors.New("备份包含多余文件")
	}
	entries := map[string]*zip.File{}
	var manifestFile *zip.File
	for _, f := range z.File {
		if f.Name == "manifest.json" {
			if manifestFile != nil {
				return errors.New("备份清单重复")
			}
			manifestFile = f
			continue
		}
		name, ok := strings.CutPrefix(f.Name, "images/")
		if !ok || !backupImages[name] || f.Name != "images/"+name {
			return errors.New("备份包含不受支持的路径")
		}
		if entries[name] != nil || !f.Mode().IsRegular() || f.Method != zip.Store {
			return errors.New("备份磁盘条目无效或重复")
		}
		entries[name] = f
	}
	if manifestFile == nil || manifestFile.UncompressedSize64 > 64<<10 || manifestFile.Method != zip.Store {
		return errors.New("备份清单缺失或无效")
	}
	reader, err := manifestFile.Open()
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	closeErr := reader.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(b) > 64<<10 {
		return errors.New("备份清单过大")
	}
	var manifest backupManifest
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("备份清单无效：%w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return errors.New("备份清单包含多余内容")
	}
	if manifest.Format != 2 || len(manifest.Files) != len(entries) || len(manifest.Files) == 0 {
		return errors.New("备份版本或磁盘清单无效")
	}
	if err = validateBackupSettings(manifest.Settings); err != nil {
		return err
	}
	if manifest.Settings.SystemBuild <= 0 {
		return errors.New("备份缺少系统镜像版本，拒绝可能清空数据的恢复")
	}
	imagePath := filepath.Join("system-images", fmt.Sprintf("android-%d", manifest.Settings.API), "google_apis", "x86_64")
	if !exists(filepath.Join(m.SDK, imagePath, "system.img")) {
		return fmt.Errorf("目标缺少 Android %d google_apis x86_64 系统镜像", manifest.Settings.API)
	}
	props, err := parseINI(filepath.Join(m.SDK, imagePath, "build.prop"))
	if err != nil {
		return errors.New("无法读取目标系统镜像版本，拒绝导入")
	}
	targetBuild, err := strconv.ParseInt(strings.TrimSpace(props["ro.build.version.incremental"]), 10, 32)
	if err != nil || targetBuild != manifest.Settings.SystemBuild {
		return errors.New("目标系统镜像版本与备份不同；继续启动可能清空实例数据，已拒绝导入")
	}
	files := map[string]backupFile{}
	var total int64
	for _, f := range manifest.Files {
		entry := entries[f.Name]
		if !backupImages[f.Name] || entry == nil || files[f.Name].Name != "" || f.Size < 0 || f.Size > maxBackupFile || uint64(f.Size) != entry.UncompressedSize64 || f.Size > maxBackupTotal-total {
			return errors.New("备份磁盘清单无效")
		}
		hash, e := hex.DecodeString(f.SHA256)
		if e != nil || len(hash) != sha256.Size {
			return errors.New("备份校验值无效")
		}
		total += f.Size
		files[f.Name] = f
	}
	if _, ok := files["userdata-qemu.img"]; !ok {
		return errors.New("备份缺少用户数据盘")
	}
	root, err := windows.UTF16PtrFromString(m.Home)
	if err != nil {
		return err
	}
	var free uint64
	if err = windows.GetDiskFreeSpaceEx(root, &free, nil, nil); err != nil {
		return fmt.Errorf("无法检查目标磁盘空间：%w", err)
	}
	if uint64(total)+(128<<20) > free {
		return errors.New("目标磁盘空间不足，未开始导入")
	}
	stage, err := os.MkdirTemp(m.Home, ".restore-")
	if err != nil {
		return err
	}
	// Only remove exact files created in our private staging directory.
	defer func() {
		for name := range backupImages {
			_ = os.Remove(filepath.Join(stage, name))
		}
		_ = os.Remove(filepath.Join(stage, "config.ini"))
		_ = os.Remove(filepath.Join(stage, "version_num.cache"))
		_ = os.Remove(stage)
	}()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return err
		}
		f, e := os.OpenFile(filepath.Join(stage, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		r, e := entries[name].Open()
		if e != nil {
			f.Close()
			return e
		}
		hash := sha256.New()
		n, e := copyContext(ctx, io.MultiWriter(f, hash), io.LimitReader(r, files[name].Size+1))
		readClose := r.Close()
		if e == nil {
			e = readClose
		}
		if e == nil && n != files[name].Size {
			e = errors.New("备份磁盘长度不匹配")
		}
		if e == nil && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), files[name].SHA256) {
			e = errors.New("备份磁盘校验失败")
		}
		if e == nil {
			e = f.Sync()
		}
		if closeErr = f.Close(); e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
	}
	images := map[string]lockedImage{}
	for _, name := range names {
		f, e := openOfflineImage(filepath.Join(stage, name))
		if e != nil {
			for _, v := range images {
				v.file.Close()
			}
			return e
		}
		images[name] = lockedImage{file: f, size: files[name].Size}
	}
	err = validateBackings(images)
	for _, v := range images {
		_ = v.file.Close()
	}
	if err != nil {
		return err
	}
	s := manifest.Settings
	config := fmt.Sprintf("AvdId=%s\navd.ini.displayname=%s\nabi.type=x86_64\nimage.sysdir.1=%s%c\ntag.id=google_apis\ntag.display=Google APIs\nhw.cpu.arch=x86_64\nhw.cpu.ncore=%d\nhw.ramSize=%d\nhw.lcd.width=%d\nhw.lcd.height=%d\nhw.lcd.density=%d\nhw.gpu.enabled=yes\nhw.gpu.mode=host\nhw.keyboard=yes\ndisk.dataPartition.size=%s\nfastboot.forceColdBoot=yes\nshowDeviceFrame=no\n", m.Name, m.Name, imagePath, os.PathSeparator, s.Cores, s.RAM, s.Width, s.Height, s.Density, s.DataSize)
	if err = writeNew(filepath.Join(stage, "config.ini"), config); err != nil {
		return err
	}
	if err = writeNew(filepath.Join(stage, "version_num.cache"), fmt.Sprintf("%d\n", s.SystemBuild)); err != nil {
		return err
	}
	ini, err := os.CreateTemp(m.Home, ".restore-index-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(ini.Name())
	if _, err = fmt.Fprintf(ini, "avd.ini.encoding=UTF-8\npath=%s\ntarget=android-%d\n", finalDir, s.API); err != nil {
		ini.Close()
		return err
	}
	if err = ini.Sync(); err != nil {
		ini.Close()
		return err
	}
	if err = ini.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	from, _ := windows.UTF16PtrFromString(stage)
	to, _ := windows.UTF16PtrFromString(finalDir)
	if err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("创建目标实例失败，未覆盖现有文件：%w", err)
	}
	from, _ = windows.UTF16PtrFromString(ini.Name())
	to, _ = windows.UTF16PtrFromString(finalINI)
	if err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("实例数据已保留在 %s，但索引未创建：%w", finalDir, err)
	}
	return nil
}
