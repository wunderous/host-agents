// Package llm owns local model serving: the llama.cpp server (build,
// prerequisites, lifecycle), the Ollama runtime, the local LLM gateway, and the
// relay that publishes a model endpoint into a cluster.
package llm

import (
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// Deps are the cross-domain capabilities llm requires. All three are the
// kubernetes domain: publishing a local model into a cluster means applying a
// manifest to a resolved target and tearing it down again.
type Deps struct {
	// KubernetesTargetURI resolves a VM name to a canonical cluster URI.
	KubernetesTargetURI func(vmName string) (string, error)
	// ApplyManifest applies a manifest to a resolved cluster target.
	ApplyManifest func(uri, manifest string, onData func(string)) (map[string]any, error)
	// DeleteK8sResource removes a named resource from a resolved cluster target.
	DeleteK8sResource func(uri, kind, name, namespace string, onData func(string)) (map[string]any, error)
}

// Service is the llm domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps

	// relay is the persistent local-LLM relay manager. It holds live listeners,
	// so it is owned per service rather than rebuilt per call.
	relay *localLLMRelayManager
}

// New builds the llm domain over the shared runtime seam.
func New(shared *hostruntime.Shared, deps Deps, relayConfigDir, sharedHostResourceLockDir string) *Service {
	return &Service{
		shared: shared,
		deps:   deps,
		relay:  newPersistentLocalLLMRelayManagerAtWithLock(relayConfigDir, sharedHostResourceLockDir),
	}
}

// StopRelays tears down every live relay listener. Incus stack teardown calls
// this before removing the instances the relays point at.
func (s *Service) StopRelays() {
	if s != nil && s.relay != nil {
		s.relay.stopAll()
	}
}
