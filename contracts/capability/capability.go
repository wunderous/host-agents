// Package capability contains stable, provider-neutral capability IDs and
// service contracts.
package capability

const (
	LLMServing = "opute.capability.llm-serving.v1"
	Tunneling  = "opute.capability.tunneling.v1"
	Kubernetes = "opute.capability.kubernetes.v1"

	KubernetesValidateOperation          = "opute.capability.kubernetes.validate"
	KubernetesProvisionOperation         = "opute.capability.kubernetes.provision"
	KubernetesStatusOperation            = "opute.capability.kubernetes.status"
	KubernetesConfigureRegistryOperation = "opute.capability.kubernetes.configure-registry"
	KubernetesRemoveOperation            = "opute.capability.kubernetes.remove"
	KubernetesRestartOperation           = "opute.capability.kubernetes.restart"
	KubernetesApplyManifestOperation     = "opute.capability.kubernetes.apply-manifest"
	KubernetesPutSecretOperation         = "opute.capability.kubernetes.put-secret"
	KubernetesGetResourceOperation       = "opute.capability.kubernetes.get-resource"
	KubernetesDeleteResourceOperation    = "opute.capability.kubernetes.delete-resource"
	KubernetesGetResourceStatusOperation = "opute.capability.kubernetes.get-resource-status"
	KubernetesListEventsOperation        = "opute.capability.kubernetes.list-events"
	KubernetesListClustersOperation      = "opute.capability.kubernetes.list-clusters"
	KubernetesGetClusterInfoOperation    = "opute.capability.kubernetes.get-cluster-info"
	KubernetesExecCommandOperation       = "opute.capability.kubernetes.exec-command"
)

type Validation struct {
	Capability string `json:"capability"`
	Contract   string `json:"contract"`
	Operation  string `json:"operation"`
}
