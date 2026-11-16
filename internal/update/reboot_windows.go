package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

type rebootMarker struct {
	BootID *uint64 `json:"boot_id"`
}

func bootID() *uint64 {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()
	n, _, err := key.GetIntegerValue("BootId")
	if err != nil {
		return nil
	}
	return &n
}

func markerPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "NekoSimulator233-data", "update-reboot.json")
}

func markRebootRequired() error {
	b, err := json.Marshal(rebootMarker{BootID: bootID()})
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(), b, 0600)
}

// PendingReboot suppresses another update until Windows records a new boot.
// If Windows does not expose its boot ID, it fails closed with a visible reason.
func PendingReboot() (bool, error) {
	b, err := os.ReadFile(markerPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var marker rebootMarker
	if err = json.Unmarshal(b, &marker); err != nil {
		return true, err
	}
	current := bootID()
	if current == nil || marker.BootID == nil || *current == *marker.BootID {
		return true, nil
	}
	if err = os.Remove(markerPath()); err != nil {
		return true, err
	}
	return false, nil
}
