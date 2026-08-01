//go:build !windows

package resource

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type platformHostLock struct {
	path string
}

func newPlatformHostLock(path string) (hostLock, error) {
	return &platformHostLock{path: path}, nil
}

func (l *platformHostLock) acquire(ctx context.Context, exclusive bool) (func(), error) {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	flags := unix.LOCK_SH
	if exclusive {
		flags = unix.LOCK_EX
	}
	for {
		err = unix.Flock(int(file.Fd()), flags|unix.LOCK_NB)
		if err == nil {
			return func() {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
