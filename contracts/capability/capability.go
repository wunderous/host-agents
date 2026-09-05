// Package capability contains stable, provider-neutral capability IDs and
// service contracts.
package capability

const (
	LLMServing     = "opute.capability.llm-serving.v1"
	Tunneling      = "opute.capability.tunneling.v1"
	Kubernetes     = "opute.capability.kubernetes.v1"
	NetworkOverlay = "opute.capability.network-overlay.v1"

	KubernetesValidateOperation           = "opute.capability.kubernetes.validate"
	KubernetesProvisionOperation          = "opute.capability.kubernetes.provision"
	KubernetesStatusOperation             = "opute.capability.kubernetes.status"
	KubernetesConfigureRegistryOperation  = "opute.capability.kubernetes.configure-registry"
	KubernetesRemoveOperation             = "opute.capability.kubernetes.remove"
	KubernetesRestartOperation            = "opute.capability.kubernetes.restart"
	KubernetesApplyManifestOperation      = "opute.capability.kubernetes.apply-manifest"
	KubernetesPutSecretOperation          = "opute.capability.kubernetes.put-secret"
	KubernetesGetResourceOperation        = "opute.capability.kubernetes.get-resource"
	KubernetesDeleteResourceOperation     = "opute.capability.kubernetes.delete-resource"
	KubernetesGetResourceStatusOperation  = "opute.capability.kubernetes.get-resource-status"
	KubernetesListEventsOperation         = "opute.capability.kubernetes.list-events"
	KubernetesListClustersOperation       = "opute.capability.kubernetes.list-clusters"
	KubernetesGetClusterInfoOperation     = "opute.capability.kubernetes.get-cluster-info"
	KubernetesExecCommandOperation        = "opute.capability.kubernetes.exec-command"
	KubernetesInspectMembershipOperation  = "opute.capability.kubernetes.inspect-membership"
	KubernetesPrepareHAOperation          = "opute.capability.kubernetes.prepare-ha"
	KubernetesPrepareJoinOperation        = "opute.capability.kubernetes.prepare-join"
	KubernetesGetJoinReceiverKeyOperation = "opute.capability.kubernetes.get-join-receiver-key"
	KubernetesRedeemJoinOperation         = "opute.capability.kubernetes.redeem-join"
	KubernetesJoinNodeOperation           = "opute.capability.kubernetes.join-node"
	KubernetesEnsureHAEndpointOperation   = "opute.capability.kubernetes.ensure-ha-endpoint"
	KubernetesRemoveNodeOperation         = "opute.capability.kubernetes.remove-node"
	KubernetesRecoverQuorumOperation      = "opute.capability.kubernetes.recover-quorum"

	NetworkOverlayValidateOperation          = "opute.capability.network-overlay.validate"
	NetworkOverlayPrepareMembershipOperation = "opute.capability.network-overlay.prepare-membership"
	NetworkOverlayJoinMembershipOperation    = "opute.capability.network-overlay.join-membership"
	NetworkOverlayAttachTargetOperation      = "opute.capability.network-overlay.attach-target"
	NetworkOverlayProbeReachabilityOperation = "opute.capability.network-overlay.probe-reachability"
	NetworkOverlayEnsureHAEndpointOperation  = "opute.capability.network-overlay.ensure-ha-endpoint"
	NetworkOverlayRemoveHAEndpointOperation  = "opute.capability.network-overlay.remove-ha-endpoint"
	NetworkOverlayRemoveMembershipOperation  = "opute.capability.network-overlay.remove-membership"
)

type Validation struct {
	Capability string `json:"capability"`
	Contract   string `json:"contract"`
	Operation  string `json:"operation"`
}
