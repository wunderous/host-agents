//go:build !windows

package instancelock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes flock(LOCK_EX|LOCK_NB) on the open file, the primitive Go's
// admission.lock uses; the lock belongs to this open file description.
func lockFile(file *os.File) error {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
			return errLocked
		default:
			return err
		}
	}
}

func unlockFile(file *os.File) {
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
