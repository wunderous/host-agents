package provider

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
)

const (
	DefaultIncusPath     = "incus"
	DefaultSystemctlPath = "/usr/bin/systemctl"
)

// ID identifies the VM provider runtime.
type ID string

const IDIncus ID = "incus"

// RequireLinux returns an error when not running on Linux (native or WSL).
func RequireLinux() error {
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
	return RequireLinux()
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
	return DefaultIncusPath
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
	argv := append([]string{r.cfg.ProviderBinary}, args...)
	return hostexec.RunCommandContext(ctx, argv, onData, timeout)
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
