// Package incus owns the local instance runtime: launching and deleting VMs and
// system containers, their images, devices, storage quotas, ownership labels,
// the inventory it reports, and the standalone stack built on top of it.
package incus

import (
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

// defaultDiscoveryTimeout bounds a read-only incus query. The kubernetes domain
// has its own copy of this budget; S4.3 rule 1 forbids importing it.
const defaultDiscoveryTimeout = 45 * time.Second

// Deps are the cross-domain capabilities incus requires.
type Deps struct {
	// ProbeIncusGPU reports the host's virtualization and GPU tooling. It is
	// host-level capability detection, so the host domain owns it.
	ProbeIncusGPU func(args map[string]any) (map[string]any, error)

	// ReinstallIncusStack installs the instance runtime's host packages with
	// default settings, which is what a stack reset needs to reconcile. That is
	// host tooling, not an instance operation.
	ReinstallIncusStack func(onData func(string)) (map[string]any, error)

	// RevokeRelays stops every in-process relay pointed at a guest. A stack
	// reset deletes those guests, so the listeners must be revoked first. It is
	// one seam rather than three because incus does not need to know which
	// domains hold relays.
	RevokeRelays func()
}

// Service is the incus domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps

	// resetCheckpointPath persists the progress of a stack reset so an
	// interrupted one can be resumed rather than restarted.
	resetCheckpointPath string
}

// New builds the incus domain over the shared runtime seam.
func New(shared *hostruntime.Shared, deps Deps, resetCheckpointPath string) *Service {
	return &Service{shared: shared, deps: deps, resetCheckpointPath: resetCheckpointPath}
}
