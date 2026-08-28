// Package oci owns container image work: staging a build context, building and
// pushing images, the OCI registry configuration, and the local container
// storage policy that keeps a build from filling the host disk.
package oci

import (
	"context"
	"sync"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

// Deps are the cross-domain capabilities oci requires. All four are the
// kubernetes domain: configuring a cluster to pull from a registry means
// applying, reading, and removing manifests on a resolved target.
type Deps struct {
	KubernetesTargetURI func(vmName string) (string, error)
	ApplyManifest       func(uri, manifest string, onData func(string)) (map[string]any, error)
	GetK8sResource      func(uri, kind, name, namespace string) (map[string]any, error)
	DeleteK8sResource   func(uri, kind, name, namespace string, onData func(string)) (map[string]any, error)
}

// Service is the oci domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps

	// storagePolicyPath is where the local container storage budget is
	// persisted; storageMu serialises the read-modify-write around it.
	storagePolicyPath string
	storageMu         sync.Mutex

	// Container command seams keep runtime adapter tests independent of an
	// installed container runtime.
	containerCommandFn          func(context.Context, string, ...string) ([]byte, error)
	containerStreamingCommandFn func(context.Context, string, []string, func(string)) error
}

// New builds the oci domain over the shared runtime seam.
func New(shared *hostruntime.Shared, deps Deps, storagePolicyPath string) *Service {
	return &Service{shared: shared, deps: deps, storagePolicyPath: storagePolicyPath}
}
