//go:build windows

package exec

import (
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {
	// Windows has no Setpgid; CommandContext + Process.Kill covers the direct child.
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
