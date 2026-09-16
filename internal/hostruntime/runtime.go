package hostruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	hostexec "github.com/wunderous/host-agents/internal/exec"
)

const (
	defaultIncusPath     = "incus"
	DefaultSystemctlPath = "/usr/bin/systemctl"
)

// ID identifies the VM provider runtime.
type ID string

const IDIncus ID = "incus"

// requireLinux returns an error when not running on Linux (native or WSL).
func requireLinux() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("opute-host-agent requires Linux (native or WSL); unsupported platform %q", runtime.GOOS)
	}
	return nil
}

// DefaultProviderID picks the provider when unset.
func DefaultProviderID() ID {
	return IDIncus
}

// NormalizeProviderID maps wire/env provider values to a catalog key.
func NormalizeProviderID(raw string) ID {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "incus":
		return IDIncus
	default:
		return DefaultProviderID()
	}
}

// RequireSupportedPlatform validates OS/provider pairing.
func RequireSupportedPlatform(providerID ID) error {
	pid := NormalizeProviderID(string(providerID))
	if pid != IDIncus {
		return fmt.Errorf("unsupported provider %q", pid)
	}
	return requireLinux()
}

// ResolveConfig picks the provider CLI binary from environment and defaults.
func ResolveConfig(providerID ID) Config {
	if providerID == "" {
		providerID = DefaultProviderID()
	}
	pid := NormalizeProviderID(string(providerID))
	binary := firstNonEmpty(
		os.Getenv("OPUTE_INCUS_BINARY_PATH"),
		os.Getenv("OPUTE_VM_BINARY_PATH"),
	)
	if binary == "" {
		binary = resolveIncusBinary()
	}
	return Config{
		ProviderID:     pid,
		ProviderBinary: binary,
	}
}

// Config holds resolved provider binary path.
type Config struct {
	ProviderID     ID
	ProviderBinary string
}

func resolveIncusBinary() string {
	for _, path := range []string{"/usr/bin/incus", "/snap/bin/incus"} {
		if fileExists(path) {
			return path
		}
	}
	return defaultIncusPath
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// Runtime executes provider and host commands.
type Runtime struct {
	cfg Config
}

func NewRuntime(cfg Config) *Runtime {
	return &Runtime{cfg: cfg}
}

func (r *Runtime) ProviderBinary() string { return r.cfg.ProviderBinary }

func (r *Runtime) ReadProviderID() ID { return r.cfg.ProviderID }

// RunProvider runs a provider CLI subcommand.
func (r *Runtime) RunProvider(args []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return r.RunProviderContext(context.Background(), args, onData, timeout)
}

// RunProviderContext runs a provider CLI subcommand with cancellation.
func (r *Runtime) RunProviderContext(ctx context.Context, args []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if err := r.requireProviderBinary(); err != nil {
		return hostexec.Result{}, err
	}
	argv := append([]string{r.cfg.ProviderBinary}, args...)
	return hostexec.RunCommandContext(ctx, argv, onData, timeout)
}

// requireProviderBinary separates "this host has no virtualization stack" from
// "the provider command failed".
//
// A host with nothing installed is the normal starting state -- it is the state
// every new user's host agent is in, and it is where the first inventory read of
// a bootstrap lands. Reporting it as `exec: "incus": executable file not found
// in $PATH` handed the caller a shell detail and no typed way to tell an empty
// host from a broken one, so list_vms could not be answered on the one host
// shape it most needs to answer for.
//
// It resolves the execution handle rather than performing a provider operation:
// it asks the filesystem whether the binary exists, asks the provider nothing,
// and changes nothing -- the same S9.2 rule-3 category as ContainerLookPath,
// and the opposite of runVMExec, which asks incus about ownership and is
// therefore incus-owned. The remediation comes from the tool-name contract so
// the message cannot drift from the capability that resolves it.
func (r *Runtime) requireProviderBinary() error {
	binary := strings.TrimSpace(r.cfg.ProviderBinary)
	if binary == "" {
		return fmt.Errorf("virtualization_stack_absent: this host agent has no virtualization provider configured")
	}
	if _, err := lookPath(binary); err != nil {
		return fmt.Errorf("virtualization_stack_absent: the %s virtualization stack is not installed on this host; install it with %s",
			firstNonEmpty(string(r.cfg.ProviderID), binary), toolname.InstallIncusStack)
	}
	return nil
}

// RunHost runs a command on the host OS.
func (r *Runtime) RunHost(command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return r.RunHostContext(context.Background(), command, onData, timeout)
}

// RunHostContext runs a command on the host OS with cancellation.
func (r *Runtime) RunHostContext(ctx context.Context, command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return hostexec.RunCommandContext(ctx, command, onData, timeout)
}

// RunVMExec runs a command inside a VM via provider exec.
func (r *Runtime) RunVMExec(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return r.RunVMExecContext(context.Background(), vmName, guestArgv, onData, timeout)
}

// RunVMExecContext runs a command inside a VM via provider exec with cancellation.
func (r *Runtime) RunVMExecContext(ctx context.Context, vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	execArgs := append([]string{"exec", vmName, "--"}, guestArgv...)
	return r.RunProviderContext(ctx, execArgs, onData, timeout)
}

// RunVMExecWithStdinContext executes a guest command while keeping the
// supplied input off the provider process argv. This is required for
// Kubernetes Secret manifests and other credential-bearing payloads.
func (r *Runtime) RunVMExecWithStdinContext(ctx context.Context, vmName string, guestArgv []string, input []byte, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if err := r.requireProviderBinary(); err != nil {
		return hostexec.Result{}, err
	}
	execArgs := append([]string{"exec", vmName, "--"}, guestArgv...)
	return hostexec.RunCommandWithStdinContext(ctx, append([]string{r.cfg.ProviderBinary}, execArgs...), input, onData, timeout)
}

// NewVMInteractiveCommand builds the provider command used by the host-owned
// PTY stream. The caller owns lifecycle, input, resize, and cancellation.
func (r *Runtime) NewVMInteractiveCommand(vmName string) (*exec.Cmd, error) {
	if strings.TrimSpace(vmName) == "" {
		return nil, fmt.Errorf("vmName is required")
	}
	if err := r.requireProviderBinary(); err != nil {
		return nil, err
	}
	return exec.Command(r.cfg.ProviderBinary, "exec", vmName, "--", "bash", "-il"), nil
}

// NeedsDirectSpawn reports whether provider commands should bypass PTY (JSON / machine-readable).
func (r *Runtime) NeedsDirectSpawn(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "query" {
		return true
	}
	if args[0] == "list" {
		for _, a := range args {
			if a == "--format" {
				return true
			}
		}
	}
	return false
}
