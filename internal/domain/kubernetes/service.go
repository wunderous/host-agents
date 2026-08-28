// Package kubernetes owns every operation that reaches a cluster: manifest
// apply, resource read and delete, events, secrets, Helm, service exposure, and
// the provider-delegated kubectl execution all of those are built on.
//
// It is the most depended-upon domain -- cluster, llm, oci, postgres, and
// serving all need kubectl against a target -- so its exported surface is
// deliberately wider than serving's. Those consumers take the exported methods
// as an injected seam; none of them import this package.
package kubernetes

import (
	"context"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

// Deps are the cross-domain capabilities kubernetes requires.
type Deps struct {
	// EnsureHostTool installs or verifies a host binary such as helm (host domain).
	EnsureHostTool func(tool string, onData func(string)) (map[string]any, error)
	// ResolveResource turns a resource URI into coordinates. It stays outside
	// hostruntime because resolution may have to ask incus whether an instance
	// exists -- see S9.2 rule 3.
	ResolveResource func(uri, wantType string) (hostruntime.Coordinates, error)
}

// Service is the kubernetes domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps

	// executor is the Kubernetes provider plugin. Cluster access is always
	// delegated to it; the host agent never shells out to kubectl itself.
	executor KubernetesProviderExecutor
	// kubectlRunner is an explicit test seam for the PostgreSQL service
	// ordering and readiness contract.
	kubectlRunner KubectlRunner
}

// KubectlRunner is the test seam for provider-delegated kubectl execution.
type KubectlRunner func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error)

// New builds the kubernetes domain over the shared runtime seam.
func New(shared *hostruntime.Shared, deps Deps) *Service {
	return &Service{shared: shared, deps: deps}
}

// SetKubectlRunner installs the kubectl test seam.
func (s *Service) SetKubectlRunner(runner KubectlRunner) { s.kubectlRunner = runner }

// DefaultDiscoveryTimeout bounds a kubectl call made for discovery rather than
// for a caller-initiated change.
const DefaultDiscoveryTimeout = 45 * time.Second
