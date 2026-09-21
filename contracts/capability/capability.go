// Package capability contains stable, provider-neutral capability IDs and
// service contracts.
package capability

const (
	LLMServing     = "opute.capability.llm-serving.v1"
	Tunneling      = "opute.capability.tunneling.v1"
	Kubernetes     = "opute.capability.kubernetes.v1"
	NetworkOverlay = "opute.capability.network-overlay.v1" // deprecated: fan-out alias / migration only

	// Exclusive HA networking seams (ADR-0016 + mesh-runtime prerequisites).
	MeshRuntime    = "opute.capability.mesh-runtime.v1"
	MeshMembership = "opute.capability.mesh-membership.v1"
	PrivateMesh    = "opute.capability.private-mesh.v1"
	PublicIngress  = "opute.capability.public-ingress.v1"

	KubernetesValidateOperation               = "opute.capability.kubernetes.validate"
	KubernetesProvisionOperation              = "opute.capability.kubernetes.provision"
	KubernetesStatusOperation                 = "opute.capability.kubernetes.status"
	KubernetesConfigureRegistryOperation      = "opute.capability.kubernetes.configure-registry"
	KubernetesRemoveOperation                 = "opute.capability.kubernetes.remove"
	KubernetesRestartOperation                = "opute.capability.kubernetes.restart"
	KubernetesApplyManifestOperation          = "opute.capability.kubernetes.apply-manifest"
	KubernetesPutSecretOperation              = "opute.capability.kubernetes.put-secret"
	KubernetesGetResourceOperation            = "opute.capability.kubernetes.get-resource"
	KubernetesDeleteResourceOperation         = "opute.capability.kubernetes.delete-resource"
	KubernetesGetResourceStatusOperation      = "opute.capability.kubernetes.get-resource-status"
	KubernetesListEventsOperation             = "opute.capability.kubernetes.list-events"
	KubernetesListClustersOperation           = "opute.capability.kubernetes.list-clusters"
	KubernetesGetClusterInfoOperation         = "opute.capability.kubernetes.get-cluster-info"
	KubernetesExecCommandOperation            = "opute.capability.kubernetes.exec-command"
	KubernetesInspectGuestStorageOperation    = "opute.capability.kubernetes.inspect-guest-storage"
	KubernetesPruneUnusedImagesOperation      = "opute.capability.kubernetes.prune-unused-images"
	KubernetesGarbageCollectRegistryOperation = "opute.capability.kubernetes.garbage-collect-registry"
	KubernetesTrimGuestStorageOperation       = "opute.capability.kubernetes.trim-guest-storage"
	KubernetesInspectMembershipOperation      = "opute.capability.kubernetes.inspect-membership"
	KubernetesPrepareHAOperation              = "opute.capability.kubernetes.prepare-ha"
	KubernetesPrepareJoinOperation            = "opute.capability.kubernetes.prepare-join"
	KubernetesGetJoinReceiverKeyOperation     = "opute.capability.kubernetes.get-join-receiver-key"
	KubernetesRedeemJoinOperation             = "opute.capability.kubernetes.redeem-join"
	KubernetesJoinNodeOperation               = "opute.capability.kubernetes.join-node"
	KubernetesEnsureHAEndpointOperation       = "opute.capability.kubernetes.ensure-ha-endpoint"
	KubernetesRemoveNodeOperation             = "opute.capability.kubernetes.remove-node"
	KubernetesRecoverQuorumOperation          = "opute.capability.kubernetes.recover-quorum"

	// Deprecated network-overlay.* ops (migration aliases). Prefer the three-seam IDs below.
	NetworkOverlayValidateOperation               = "opute.capability.network-overlay.validate"
	NetworkOverlayPrepareMembershipOperation      = "opute.capability.network-overlay.prepare-membership"
	NetworkOverlayJoinMembershipOperation         = "opute.capability.network-overlay.join-membership"
	NetworkOverlayAttachTargetOperation           = "opute.capability.network-overlay.attach-target"
	NetworkOverlayProbeReachabilityOperation      = "opute.capability.network-overlay.probe-reachability"
	NetworkOverlayEnsureHAEndpointOperation       = "opute.capability.network-overlay.ensure-ha-endpoint"
	NetworkOverlayRemoveHAEndpointOperation       = "opute.capability.network-overlay.remove-ha-endpoint"
	NetworkOverlayRemoveMembershipOperation       = "opute.capability.network-overlay.remove-membership"
	NetworkOverlayEnrollOperation                 = "opute.capability.network-overlay.enroll"
	NetworkOverlayEnsurePrivateMeshOperation      = "opute.capability.network-overlay.ensure-private-mesh"
	NetworkOverlayEnsurePrivateServiceOperation   = "opute.capability.network-overlay.ensure-private-service"
	NetworkOverlayEnsurePublicIngressOperation    = "opute.capability.network-overlay.ensure-public-ingress"
	NetworkOverlayPromotePublicIngressOperation   = "opute.capability.network-overlay.promote-public-ingress"
	NetworkOverlayProbeOperation                  = "opute.capability.network-overlay.probe"
	NetworkOverlayReportTwoNodeReadinessOperation = "opute.capability.network-overlay.report-two-node-readiness"

	// mesh-runtime.v1 — install/configure mesh agent + control-plane prerequisites
	MeshRuntimeValidateOperation           = "opute.capability.mesh-runtime.validate"
	MeshRuntimeEnsureAgentOperation        = "opute.capability.mesh-runtime.ensure-agent"
	MeshRuntimeEnsureControlPlaneOperation = "opute.capability.mesh-runtime.ensure-control-plane"
	MeshRuntimeStatusOperation             = "opute.capability.mesh-runtime.status"

	// mesh-membership.v1
	MeshMembershipEnrollOperation = "opute.capability.mesh-membership.enroll"
	MeshMembershipStatusOperation = "opute.capability.mesh-membership.status"
	MeshMembershipLeaveOperation  = "opute.capability.mesh-membership.leave"

	// private-mesh.v1
	PrivateMeshEnsureOperation        = "opute.capability.private-mesh.ensure"
	PrivateMeshEnsureServiceOperation = "opute.capability.private-mesh.ensure-service"
	PrivateMeshProbeOperation         = "opute.capability.private-mesh.probe"

	// public-ingress.v1
	PublicIngressEnsureOperation  = "opute.capability.public-ingress.ensure"
	PublicIngressPromoteOperation = "opute.capability.public-ingress.promote"
	PublicIngressProbeOperation   = "opute.capability.public-ingress.probe"
)

type Validation struct {
	Capability string `json:"capability"`
	Contract   string `json:"contract"`
	Operation  string `json:"operation"`
}
