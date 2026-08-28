//go:build !windows

package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type hostRequestLock struct{ path string }

func newHostRequestLock(lockDir string) *hostRequestLock {
	if lockDir == "" {
		return nil
	}
	return &hostRequestLock{path: filepath.Join(lockDir, "ollama-inference.lock")}
}

func (l *hostRequestLock) acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open shared Ollama request lock: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = file.Close()
			return nil, fmt.Errorf("acquire shared Ollama request lock: %w", err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
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
