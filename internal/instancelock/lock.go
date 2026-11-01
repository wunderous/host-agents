// Package instancelock keeps one Host Agent process per instance state.
//
// An agent takes an exclusive, non-blocking lock on host-agent.lock in each
// of its state roots before it opens any store or binds its listener, and
// holds it for the life of the process. A second agent on the same roots is
// refused. The kernel releases the lock when the process exits, so a crashed
// agent leaves no stale lock and the file itself is never deleted.
//
// Network filesystems are out of scope: their lock semantics are not
// guaranteed.
package instancelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the lock file created in each locked root.
const FileName = "host-agent.lock"

// errLocked is returned by the platform lock when another process holds it.
var errLocked = errors.New("locked")

// Lock holds the instance locks of one process.
type Lock struct {
	files []*os.File
}

// Acquire locks host-agent.lock in each root, in the order given, creating a
// missing root (0700) and lock file (0600). It never waits: a root another
// process holds is refused at once, and any lock already taken is released.
// Roots that resolve to the same directory are locked once.
func Acquire(roots ...string) (*Lock, error) {
	lock := &Lock{}
	seen := map[string]bool{}
	for _, root := range roots {
		dir, err := filepath.Abs(root)
		if err != nil {
			lock.Release()
			return nil, fmt.Errorf("resolve host agent instance root %q: %w", root, err)
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if err := os.MkdirAll(dir, 0o700); err != nil {
			lock.Release()
			return nil, fmt.Errorf("create host agent instance root: %w", err)
		}
		path := filepath.Join(dir, FileName)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			lock.Release()
			return nil, fmt.Errorf("open host agent instance lock: %w", err)
		}
		if err := lockFile(file); err != nil {
			_ = file.Close()
			lock.Release()
			if errors.Is(err, errLocked) {
				return nil, fmt.Errorf("host agent instance is already in use: %s is locked by another process", path)
			}
			return nil, fmt.Errorf("lock host agent instance %s: %w", path, err)
		}
		lock.files = append(lock.files, file)
	}
	return lock, nil
}

// Release unlocks and closes every held lock file. It is safe on nil and
// safe to call twice.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	for i := len(l.files) - 1; i >= 0; i-- {
		unlockFile(l.files[i])
		_ = l.files[i].Close()
	}
	l.files = nil
}

// Within reports whether path lies inside root (or is root) after both are
// made absolute.
func Within(root, path string) bool {
	root, rootErr := filepath.Abs(root)
	path, pathErr := filepath.Abs(path)
	if rootErr != nil || pathErr != nil {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
