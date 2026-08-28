package exec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// The helpers below are the plain process primitives -- no streaming protocol,
// no timeout policy -- that more than one domain needs. They live here rather
// than in hostruntime because they hold no identity or configuration: they are
// os/exec with the error handling everyone was otherwise re-writing.
//
// Each is a var so a focused unit test can replace it without changing the
// production adapter contract.

// Command builds a cancellable command.
var Command = func(ctx context.Context, command string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, command, args...)
}

// LookPath resolves a binary on PATH.
var LookPath = func(command string) (string, error) { return exec.LookPath(command) }

// Run executes a command and returns its combined output. On failure the
// output is folded into the error, because a bare "exit status 1" is useless.
var Run = func(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := Command(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return nil, fmt.Errorf("%w: %s", err, text)
		}
	}
	return output, err
}

// RunPrivilegedPackage runs a package-manager command under non-interactive
// sudo. The -n is deliberate: a package install that blocks on a password
// prompt would hang an agent nobody is watching.
func RunPrivilegedPackage(ctx context.Context, command string, args ...string) error {
	cmd := Command(ctx, "sudo", append([]string{"-n", command}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}
