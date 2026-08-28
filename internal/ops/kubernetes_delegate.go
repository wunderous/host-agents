package ops

import (
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// The kubernetes domain owns these types and operations. HostOperationsService
// keeps delegating methods so the dispatch registry and the domains that have
// not moved yet are unaffected; this file disappears with internal/ops itself.
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
// &HostOperationsService{} and never run the constructor.
func (s *HostOperationsService) kubernetes() *kubernetes.Service {
	s.k8sOnce.Do(func() {
		s.k8s = kubernetes.New(&s.shared, kubernetes.Deps{
			EnsureHostTool: func(tool string, onData func(string)) (map[string]any, error) {
				return s.EnsureHostTool(EnsureHostToolArgs{Tool: tool}, onData)
			},
			ResolveResource: func(uri, wantType string) (hostruntime.Coordinates, error) {
				return s.ResolveResource(uri, wantType)
			},
		})
	})
	return s.k8s
}

func (s *HostOperationsService) SetKubernetesProviderExecutor(executor KubernetesProviderExecutor) {
	s.kubernetes().SetKubernetesProviderExecutor(executor)
}

func (s *HostOperationsService) KubernetesProviderExecutor() KubernetesProviderExecutor {
	return s.kubernetes().KubernetesProviderExecutor()
}

func (s *HostOperationsService) ApplyManifest(args ApplyManifestArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().ApplyManifest(args, onData)
}

func (s *HostOperationsService) DeleteK8sResource(args K8sResourceArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().DeleteK8sResource(args, onData)
}

func (s *HostOperationsService) GetK8sResource(args K8sResourceArgs) (map[string]any, error) {
	return s.kubernetes().GetK8sResource(args)
}

func (s *HostOperationsService) GetK8sResourceStatus(args K8sResourceArgs) (map[string]any, error) {
	return s.kubernetes().GetK8sResourceStatus(args)
}

func (s *HostOperationsService) ListK8sEvents(args K8sEventsArgs) (map[string]any, error) {
	return s.kubernetes().ListK8sEvents(args)
}

func (s *HostOperationsService) PutK8sSecret(args PutK8sSecretArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().PutK8sSecret(args, onData)
}

func (s *HostOperationsService) ConfigureServiceDomain(args ConfigureServiceDomainArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().ConfigureServiceDomain(args, onData)
}

func (s *HostOperationsService) RemoveServiceDomain(args ConfigureServiceDomainArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().RemoveServiceDomain(args, onData)
}

func (s *HostOperationsService) InstallHelmChart(args InstallHelmChartArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().InstallHelmChart(args, onData)
}

func (s *HostOperationsService) UninstallHelmChart(args UninstallHelmChartArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().UninstallHelmChart(args, onData)
}

func (s *HostOperationsService) RenderHelmTemplate(args RenderHelmTemplateArgs, onData func(string)) (map[string]any, error) {
	return s.kubernetes().RenderHelmTemplate(args, onData)
}

func (s *HostOperationsService) ListKubernetesClusters(source string) (ClusterListResult, error) {
	return s.kubernetes().ListKubernetesClusters(source)
}

// The remaining delegation is the kubectl seam the domains that have not moved
// yet still reach for: cluster runs kubectl against a target, and will take it
// as an injected Deps seam when it moves.

func (s *HostOperationsService) runKubernetesKubectl(vmName string, kubectlArgs []string, label string) (string, error) {
	return s.kubernetes().RunKubectl(vmName, kubectlArgs, label)
}

func (s *HostOperationsService) executeKubernetesProvider(operation, targetURI string, arguments map[string]any) (map[string]any, bool, error) {
	return s.kubernetes().ExecuteProvider(operation, targetURI, arguments)
}

// HelmValuesYAML is a pure encoder with no service state; the dispatch layer
// calls it directly.
func HelmValuesYAML(raw any) string { return kubernetes.HelmValuesYAML(raw) }

func (s *HostOperationsService) ListNamespaces(vmName string) ([]string, error) {
	return s.kubernetes().ListNamespaces(vmName)
}

func (s *HostOperationsService) ListStorageClasses(vmName string) ([]string, error) {
	return s.kubernetes().ListStorageClasses(vmName)
}

func (s *HostOperationsService) ListIngressClasses(vmName string) ([]string, error) {
	return s.kubernetes().ListIngressClasses(vmName)
}

func (s *HostOperationsService) ListServices(vmName, namespace string) ([]map[string]any, error) {
	return s.kubernetes().ListServices(vmName, namespace)
}

func (s *HostOperationsService) ListPods(vmName, namespace string) ([]map[string]any, error) {
	return s.kubernetes().ListPods(vmName, namespace)
}

func (s *HostOperationsService) ListDeployments(vmName, namespace string) ([]map[string]any, error) {
	return s.kubernetes().ListDeployments(vmName, namespace)
}
