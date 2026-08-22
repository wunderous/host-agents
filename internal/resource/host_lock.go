package resource

import (
	"context"
	"os"
	"path/filepath"
)

type hostLock interface {
	acquire(context.Context, bool) (func(), error)
}

func newHostLock(dir string) (hostLock, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "opute-host-agent-resource-coordinator")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return newPlatformHostLock(filepath.Join(dir, "admission.lock"))
}
