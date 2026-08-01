//go:build windows

package resource

import (
	"context"
)

type platformHostLock struct{}

func newPlatformHostLock(_ string) (hostLock, error) {
	// Windows host-agent builds do not currently share the WSL Incus provider.
	// Keep the API valid there; WSL/Linux uses the process-independent flock.
	return &platformHostLock{}, nil
}

func (l *platformHostLock) acquire(ctx context.Context, _ bool) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return func() {}, nil
	}
}
