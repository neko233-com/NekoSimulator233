package avd

import (
	"context"
	"time"
)

// ControlReadiness is owned by a single, serialized control client. It keeps a
// recently inspected instance only while discovery still reports the exact
// same process, port and per-process token. It is not a cache for status or
// ADB operations, which continue to perform full readiness checks.
type ControlReadiness struct {
	manager  Manager
	instance Instance
	verified time.Time
}

const controlReadinessTTL = 2 * time.Second

func sameControlEndpoint(a, b Instance) bool {
	return a.Name == b.Name && a.Serial == b.Serial && a.pid == b.pid && a.port == b.port && a.token == b.token && a.sdk == b.sdk
}

func (c *ControlReadiness) Ready(ctx context.Context, m Manager) (Instance, error) {
	return c.ready(ctx, m, time.Now, m.discover, m.inspect)
}

func (c *ControlReadiness) ready(ctx context.Context, m Manager, now func() time.Time, discover func() (Instance, error), inspect func(context.Context, Instance) (Instance, error)) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	current, err := discover()
	if err != nil {
		*c = ControlReadiness{}
		return Instance{}, err
	}
	age := now().Sub(c.verified)
	if c.manager == m && !c.verified.IsZero() && age >= 0 && age < controlReadinessTTL && sameControlEndpoint(c.instance, current) {
		if err := ctx.Err(); err != nil {
			return Instance{}, err
		}
		return c.instance, nil
	}
	*c = ControlReadiness{}
	ready, err := inspect(ctx, current)
	if err != nil {
		return Instance{}, err
	}
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	c.manager, c.instance, c.verified = m, ready, now()
	return ready, nil
}
