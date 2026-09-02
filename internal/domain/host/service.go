// Package host owns everything that acts on the agent's own machine: shell
// execution, files and archives, artifacts, firewall rules, platform probes,
// host tooling, HTTP probes, systemd service state, and bridge diagnostics.
package host

import (
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// Deps are the cross-domain capabilities host requires.
type Deps struct {
	// ResolveResource turns a resource URI into coordinates. See S9.2 rule 3
	// for why this is not a hostruntime member.
	ResolveResource func(uri, wantType string) (hostruntime.Coordinates, error)
	// VMInventoryCapacity reports what the incus inventory can still hold; the
	// host description carries it (incus domain).
	VMInventoryCapacity func() (vminfo.VMInventoryCapacity, error)
	// RunVMExec executes a command inside a VM. It is an incus operation, not a
	// hostruntime handle -- it performs an ownership check first.
	RunVMExec func(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error)
	// RunVMExecWithStdin keeps credential-bearing input off the provider argv and
	// task metadata while preserving the same ownership check as RunVMExec.
	RunVMExecWithStdin func(vmName string, guestArgv []string, input []byte, onData func(string), timeout time.Duration) (hostexec.Result, error)
	// SupportedTools lists the tool names this agent serves for a provider.
	SupportedTools func(providerID string) []string
}

// Service is the host domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps
}

// New builds the host domain over the shared runtime seam.
func New(shared *hostruntime.Shared, deps Deps) *Service {
	return &Service{shared: shared, deps: deps}
}
