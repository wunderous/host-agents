//go:build unix

package exec

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// configureCommandEnvironment restores the two variables systemctl --user
// needs when the host agent is launched as a standalone process. WSL's login
// shell normally supplies them, but a recovered or boot-started agent can be
// started directly by PID 1 and therefore has neither variable in its
// environment. Without this, every user-systemd operation fails even though
// the user bus is healthy.
func configureCommandEnvironment(cmd *exec.Cmd) {
	if cmd == nil || !isUserSystemctl(cmd.Args) {
		return
	}

	env := cmd.Env
	if env == nil {
		env = os.Environ()
	} else {
		env = append([]string(nil), env...)
	}

	runtimeDir := environmentValue(env, "XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = fmt.Sprintf("/run/user/%s", strconv.Itoa(os.Getuid()))
		env = appendEnvironmentValue(env, "XDG_RUNTIME_DIR", runtimeDir)
	}
	if environmentValue(env, "DBUS_SESSION_BUS_ADDRESS") == "" {
		env = appendEnvironmentValue(env, "DBUS_SESSION_BUS_ADDRESS", "unix:path="+strings.TrimRight(runtimeDir, "/")+"/bus")
	}
	cmd.Env = env
}

func isUserSystemctl(argv []string) bool {
	if len(argv) < 2 || (argv[0] != "systemctl" && !strings.HasSuffix(argv[0], "/systemctl")) {
		return false
	}
	for _, arg := range argv[1:] {
		if arg == "--user" {
			return true
		}
	}
	return false
}

func environmentValue(env []string, name string) string {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func appendEnvironmentValue(env []string, name, value string) []string {
	prefix := name + "="
	for index, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if strings.TrimPrefix(entry, prefix) != "" {
				return env
			}
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
