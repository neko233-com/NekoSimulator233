package avd

import (
	"context"
	"errors"
	"strings"
	"time"
)

func sameInstance(a, b Instance) bool {
	return a.pid != 0 && a.pid == b.pid && a.port != 0 && a.port == b.port && a.token != "" && a.token == b.token && a.Serial != "" && a.Serial == b.Serial && a.Name != "" && a.Name == b.Name
}

// Revalidate process identity and the ADB console while coordinating with
// Neko's start/stop operations; the callback must remain bounded.
func (m Manager) withCurrentInstance(ctx context.Context, original Instance, fn func(context.Context, Instance) error) error {
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	return m.withCurrentInstanceGuard(ctx, original, fn)
}

func (m Manager) withCurrentInstanceGuard(ctx context.Context, original Instance, fn func(context.Context, Instance) error) error {
	unlock, err := m.lockLifecycle(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := m.discover()
	if err != nil || !sameInstance(original, current) {
		return errors.New("实例已停止或发生变化，未操作其他实例")
	}
	name, err := m.adb(ctx, current.Serial, "emu", "avd", "name")
	if err != nil || strings.TrimSpace(strings.SplitN(string(name), "\n", 2)[0]) != current.Name {
		return errors.New("无法确认实例控制端口，未执行操作")
	}
	return fn(ctx, current)
}
