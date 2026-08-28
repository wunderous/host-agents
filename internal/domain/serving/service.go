// Package serving owns process and service assignment reconciliation and the
// generic ingress discovery that reports where an assignment can be reached.
//
// It is the first domain extracted from internal/ops under plan sec. 7. What it
// needs from other domains it declares as Deps -- narrow function seams stated
// in primitives, never in another domain's types. That is deliberate: a seam
// spelled `BridgeIP(vmName) (string, error)` says what serving needs, while a
// seam spelled `GetVMInfo(vmName, fast) (VMInfo, error)` would drag the incus
// inventory model across the boundary and make the packages import each other.
package serving

import (
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// Deps are the cross-domain capabilities serving requires. Every field is
// required; a nil field is a wiring bug, not a supported degraded mode.
type Deps struct {
	// RunAgentShell executes a shell command on the agent host (host domain).
	RunAgentShell func(command string, onData func(string)) error
	// SetHostServiceState drives a systemd unit to a desired state and reports
	// what it observed (host domain).
	SetHostServiceState func(serviceName, state, scope string, onData func(string)) (map[string]any, error)
	// BridgeIP resolves a target's bridge IPv4 address (incus domain).
	BridgeIP func(vmName string) (string, error)
	// IngressLoadBalancer reports the load balancer address published by an
	// ingress service, if it has one (kubernetes domain).
	IngressLoadBalancer func(vmName, namespace, service string) (ip, hostname string)
}

// Service is the serving domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps
}

// New builds the serving domain over the shared runtime seam and the
// cross-domain capabilities it depends on.
func New(shared *hostruntime.Shared, deps Deps) *Service {
	return &Service{shared: shared, deps: deps}
}
