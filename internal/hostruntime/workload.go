package hostruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
)

const defaultWorkloadSystemdRunPath = "/usr/bin/systemd-run"

// RunHostWorkloadContext runs one host workload inside an operation-scoped
// systemd unit. The scope is opaque identity data; it is hashed before it is
// used as a unit name so neither commands nor caller-controlled identifiers
// become systemd syntax.
func (r *Runtime) RunHostWorkloadContext(ctx context.Context, scope string, command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return hostexec.Result{}, errors.New("workload command is required")
	}
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	seconds := int64(timeout / time.Second)
	if timeout%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	unit := workloadUnitName(scope, command)
	argv := []string{
		defaultWorkloadSystemdRunPath,
		"--unit=" + unit,
		"--wait",
		"--collect",
		"--pipe",
		"--property=Slice=opute-workload.slice",
		"--property=KillMode=control-group",
		"--property=MemoryHigh=5G",
		"--property=MemoryMax=6G",
		"--property=MemorySwapMax=1G",
		"--property=CPUQuota=600%",
		"--property=CPUWeight=100",
		"--property=TasksMax=4096",
		"--property=RuntimeMaxSec=" + formatSeconds(seconds),
		"--property=TimeoutStopSec=30s",
	}
	// A system-scoped Host Agent runs as root under the system manager. Asking
	// systemd-run for a user bus from that service fails with "No medium found"
	// and leaves typed host commands unable to reach their declared workload
	// boundary. Non-root agents retain the user-manager projection.
	if os.Geteuid() != 0 {
		argv = append(argv[:1], append([]string{"--user"}, argv[1:]...)...)
	}
	argv = append(argv, command...)
	return hostexec.RunCommandContext(ctx, argv, onData, timeout+30*time.Second)
}

func workloadUnitName(scope string, command []string) string {
	seed := strings.TrimSpace(scope) + "\x00" + strings.Join(command, "\x00")
	if strings.TrimSpace(scope) == "" {
		seed += "\x00" + time.Now().UTC().Format(time.RFC3339Nano)
	}
	digest := sha256.Sum256([]byte(seed))
	return "opute-host-workload-" + hex.EncodeToString(digest[:])[:24]
}

func formatSeconds(seconds int64) string {
	return strconv.FormatInt(seconds, 10) + "s"
}
