package hostagent

import (
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// These aliases name the kubernetes domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
type (
	ApplyManifestArgs          = kubernetes.ApplyManifestArgs
	ConfigureServiceDomainArgs = kubernetes.ConfigureServiceDomainArgs
	InstallHelmChartArgs       = kubernetes.InstallHelmChartArgs
	K8sEventsArgs              = kubernetes.K8sEventsArgs
	K8sResourceArgs            = kubernetes.K8sResourceArgs
	PutK8sSecretArgs           = kubernetes.PutK8sSecretArgs
	RenderHelmTemplateArgs     = kubernetes.RenderHelmTemplateArgs
	UninstallHelmChartArgs     = kubernetes.UninstallHelmChartArgs
	KubernetesProviderExecutor = kubernetes.KubernetesProviderExecutor
	KubernetesProviderRequest  = kubernetes.KubernetesProviderRequest
)

const (
	KubernetesCapabilityID            = kubernetes.KubernetesCapabilityID
	KubernetesGetClusterInfoOperation = kubernetes.KubernetesGetClusterInfoOperation
	KubernetesProvisionOperation      = kubernetes.KubernetesProvisionOperation
	KubernetesRemoveOperation         = kubernetes.KubernetesRemoveOperation
	KubernetesRestartOperation        = kubernetes.KubernetesRestartOperation
	KubernetesStatusOperation         = kubernetes.KubernetesStatusOperation
	KubernetesValidateOperation       = kubernetes.KubernetesValidateOperation
	defaultDiscoveryTimeout           = kubernetes.DefaultDiscoveryTimeout
)

// k8sOnce guards lazy construction. Unlike serving, the kubernetes domain holds
// mutable state -- the provider executor and the kubectl test seam are
// installed after construction -- so it must be one instance per service, not
// one per call. It is built lazily because tests construct a bare
// &Service{} and never run the constructor.
func (s *Service) Kubernetes() *kubernetes.Service {
	s.k8sOnce.Do(func() {
		s.k8s = kubernetes.New(&s.shared, kubernetes.Deps{
			EnsureHostTool: func(tool string, onData func(string)) (map[string]any, error) {
				return s.Host().EnsureHostTool(EnsureHostToolArgs{Tool: tool}, onData)
			},
			ResolveResource: func(uri, wantType string) (hostruntime.Coordinates, error) {
				return s.ResolveResource(uri, wantType)
			},
		})
	})
	return s.k8s
}

// The remaining delegation is the kubectl seam the domains that have not moved
// yet still reach for: cluster runs kubectl against a target, and will take it
// as an injected Deps seam when it moves.

func (s *Service) runKubernetesKubectl(vmName string, kubectlArgs []string, label string) (string, error) {
	return s.Kubernetes().RunKubectl(vmName, kubectlArgs, label)
}

func (s *Service) executeKubernetesProvider(operation, targetURI string, arguments map[string]any) (map[string]any, bool, error) {
	return s.Kubernetes().ExecuteProvider(operation, targetURI, arguments)
}

// HelmValuesYAML is a pure encoder with no service state; the dispatch layer
// calls it directly.
func HelmValuesYAML(raw any) string { return kubernetes.HelmValuesYAML(raw) }
