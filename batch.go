package main

import (
	"context"
	"time"

	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

// startInstanceForBatch starts one project-owned instance for batch boot.
func startInstanceForBatch(name string) (avd.Instance, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	manager, err := selectedManager(name)
	if err != nil {
		return avd.Instance{}, "", err
	}
	instance, err := manager.StartWithProgress(ctx, nil)
	if err != nil {
		return avd.Instance{}, "", err
	}
	bindings, err := resolveKeymapPathForInstance(name)
	return instance, bindings, err
}
