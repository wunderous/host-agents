//go:build windows

package exec

import (
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {
	// Windows has no Setpgid; CommandContext + Process.Kill covers the direct child.
}

// Windows does not expose the user-systemd bus used by the Unix host runtime.
// Keep the platform hook total so cross-compiling the published Windows
// artifact exercises the same command-runner contract as the Unix build.
func configureCommandEnvironment(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
