//go:build unix

package exec

import (
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Negative PID signals the process group started with Setpgid.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
