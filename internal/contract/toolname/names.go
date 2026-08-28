// Package toolname is the canonical set of host dispatch tool names.
//
// These are constants rather than string literals at the call sites so that the
// dispatch registry, the catalog cross-check, and the coverage test all agree by
// construction. Before this package the coverage test read internal/tools/dispatch.go
// as TEXT and matched `case "..."` labels; replacing the switch with a table would
// have left it matching nothing and passing vacuously, with the coverage guarantee
// gone and no failing test to say so (plan §2.4).
//
// The plan says 104 names. The switch actually declared 102 distinct labels --
// a `grep -c 'case "'` counts nested switches inside case bodies too.
package toolname

// Tool names, one constant per dispatch entry.
const (
	AgentShell                    = "agent_shell"
	ApplyManifest                 = "apply_manifest"
	BuildAndPushOCIImage          = "build_and_push_oci_image"
	CheckLocalLLMPrerequisites    = "check_local_llm_prerequisites"
	CheckLocalPrerequisites       = "check_local_prerequisites"
	CleanupContainerStorage       = "cleanup_container_storage"
	ConfigureAgentConnection      = "configure_agent_connection"
	ConfigureLocalLLMModel        = "configure_local_llm_model"
	ConfigureLocalLLMRuntime      = "configure_local_llm_runtime"
	ConfigureOCIStorage           = "configure_oci_storage"
	ConfigureServiceDomain        = "configure_service_domain"
	CreateVM                      = "create_vm"
	DeleteK8sResource             = "delete_k8s_resource"
	DeleteOCIRegistry             = "delete_oci_registry"
	DeletePostgreSQL              = "delete_postgresql"
	DeleteVM                      = "delete_vm"
	DetectHostPlatform            = "detect_host_platform"
	DiagnoseBridge                = "diagnose_bridge"
	DiscoverServiceIngress        = "discover_service_ingress"
	EnsureDocker                  = "ensure_docker"
	EnsureHostArtifact            = "ensure_host_artifact"
	EnsureHostFile                = "ensure_host_file"
	EnsureHostFirewallRule        = "ensure_host_firewall_rule"
	EnsureHostServiceSupervisor   = "ensure_host_service_supervisor"
	EnsureHostTool                = "ensure_host_tool"
	EnsureK3d                     = "ensure_k3d"
	EnsureLocalLLMK3sProxy        = "ensure_local_llm_k3s_proxy"
	EnsureLocalLLMRelay           = "ensure_local_llm_relay"
	EnsureLocalLLMServerBinary    = "ensure_local_llm_server_binary"
	EnsureOCIBuilder              = "ensure_oci_builder"
	EnsureSqlConnector            = "ensure_sql_connector"
	EnsureSQLiteDatabase          = "ensure_sqlite_database"
	ExecCommand                   = "exec_command"
	ExtractHostArchive            = "extract_host_archive"
	GetClusterDetails             = "get_cluster_details"
	GetClusterRuntimeDetails      = "get_cluster_runtime_details"
	GetHostInfo                   = "get_host_info"
	GetK8sResource                = "get_k8s_resource"
	GetK8sResourceStatus          = "get_k8s_resource_status"
	GetLocalStatus                = "get_local_status"
	GetOCIRegistryStatus          = "get_oci_registry_status"
	GetPostgreSQLServiceStatus    = "get_postgresql_service_status"
	GetPostgreSQLStatus           = "get_postgresql_status"
	GetSqlConnectorStatus         = "get_sql_connector_status"
	GetSQLiteDatabaseStatus       = "get_sqlite_database_status"
	GetVMInfo                     = "get_vm_info"
	InspectContainerStorage       = "inspect_container_storage"
	InspectHostFile               = "inspect_host_file"
	InspectHostService            = "inspect_host_service"
	InstallHelmChart              = "install_helm_chart"
	InstallIncusStack             = "install_incus_stack"
	InstallLocalLLMModel          = "install_local_llm_model"
	InstallOCIRegistry            = "install_oci_registry"
	InstallPostgreSQL             = "install_postgresql"
	ListClusters                  = "list_clusters"
	ListDeployments               = "list_deployments"
	ListHostServices              = "list_host_services"
	ListIngressClasses            = "list_ingress_classes"
	ListK8sEvents                 = "list_k8s_events"
	ListKubernetesClusters        = "list_kubernetes_clusters"
	ListLocalLLMModels            = "list_local_llm_models"
	ListNamespaces                = "list_namespaces"
	ListPods                      = "list_pods"
	ListServices                  = "list_services"
	ListStorageClasses            = "list_storage_classes"
	ListVMs                       = "list_vms"
	PrepareHostAgentArtifacts     = "prepare_host_agent_artifacts"
	ProbeGPUContainer             = "probe_gpu_container"
	ProbeHTTPEndpoint             = "probe_http_endpoint"
	ProbeIncusGPU                 = "probe_incus_gpu"
	ProbeLocalLLM                 = "probe_local_llm"
	ProbeOpenaiCompatibleServer   = "probe_openai_compatible_server"
	ProvisionContainer            = "provision_container"
	ProvisionVM                   = "provision_vm"
	PutK8sSecret                  = "put_k8s_secret"
	ReconcilePostgreSQLService    = "reconcile_postgresql_service"
	ReconcileServingAssignment    = "reconcile_serving_assignment"
	RecoverBridge                 = "recover_bridge"
	ReleasePostgreSQLServiceRelay = "release_postgresql_service_relay"
	ReleaseSqlConnector           = "release_sql_connector"
	RemoveHostFile                = "remove_host_file"
	RemoveLocalLLMK3sProxy        = "remove_local_llm_k3s_proxy"
	RemoveLocalLLMModel           = "remove_local_llm_model"
	RemoveLocalLLMRelay           = "remove_local_llm_relay"
	RemovePostgreSQLService       = "remove_postgresql_service"
	RemoveServiceDomain           = "remove_service_domain"
	RemoveSQLiteDatabase          = "remove_sqlite_database"
	RenderHelmTemplate            = "render_helm_template"
	ResetIncusStack               = "reset_incus_stack"
	RestartHostService            = "restart_host_service"
	RestartVM                     = "restart_vm"
	RunHostCommand                = "run_host_command"
	RunInstanceCommand            = "run_instance_command"
	RunSql                        = "run_sql"
	SetHostServiceState           = "set_host_service_state"
	StageBuildContext             = "stage_build_context"
	StartLocalLLMRuntime          = "start_local_llm_runtime"
	StartVM                       = "start_vm"
	StopLocalLLMRuntime           = "stop_local_llm_runtime"
	StopVM                        = "stop_vm"
	UninstallHelmChart            = "uninstall_helm_chart"
	UpdateVMResources             = "update_vm_resources"
)

// All returns every dispatch tool name. The dispatch registry's key set must
// equal this exactly: a tool here with no handler is an unroutable capability,
// and a handler with no name here is unreachable.
func All() []string {
	return []string{
		AgentShell,
		ApplyManifest,
		BuildAndPushOCIImage,
		CheckLocalLLMPrerequisites,
		CheckLocalPrerequisites,
		CleanupContainerStorage,
		ConfigureAgentConnection,
		ConfigureLocalLLMModel,
		ConfigureLocalLLMRuntime,
		ConfigureOCIStorage,
		ConfigureServiceDomain,
		CreateVM,
		DeleteK8sResource,
		DeleteOCIRegistry,
		DeletePostgreSQL,
		DeleteVM,
		DetectHostPlatform,
		DiagnoseBridge,
		DiscoverServiceIngress,
		EnsureDocker,
		EnsureHostArtifact,
		EnsureHostFile,
		EnsureHostFirewallRule,
		EnsureHostServiceSupervisor,
		EnsureHostTool,
		EnsureK3d,
		EnsureLocalLLMK3sProxy,
		EnsureLocalLLMRelay,
		EnsureLocalLLMServerBinary,
		EnsureOCIBuilder,
		EnsureSqlConnector,
		EnsureSQLiteDatabase,
		ExecCommand,
		ExtractHostArchive,
		GetClusterDetails,
		GetClusterRuntimeDetails,
		GetHostInfo,
		GetK8sResource,
		GetK8sResourceStatus,
		GetLocalStatus,
		GetOCIRegistryStatus,
		GetPostgreSQLServiceStatus,
		GetPostgreSQLStatus,
		GetSqlConnectorStatus,
		GetSQLiteDatabaseStatus,
		GetVMInfo,
		InspectContainerStorage,
		InspectHostFile,
		InspectHostService,
		InstallHelmChart,
		InstallIncusStack,
		InstallLocalLLMModel,
		InstallOCIRegistry,
		InstallPostgreSQL,
		ListClusters,
		ListDeployments,
		ListHostServices,
		ListIngressClasses,
		ListK8sEvents,
		ListKubernetesClusters,
		ListLocalLLMModels,
		ListNamespaces,
		ListPods,
		ListServices,
		ListStorageClasses,
		ListVMs,
		PrepareHostAgentArtifacts,
		ProbeGPUContainer,
		ProbeHTTPEndpoint,
		ProbeIncusGPU,
		ProbeLocalLLM,
		ProbeOpenaiCompatibleServer,
		ProvisionContainer,
		ProvisionVM,
		PutK8sSecret,
		ReconcilePostgreSQLService,
		ReconcileServingAssignment,
		RecoverBridge,
		ReleasePostgreSQLServiceRelay,
		ReleaseSqlConnector,
		RemoveHostFile,
		RemoveLocalLLMK3sProxy,
		RemoveLocalLLMModel,
		RemoveLocalLLMRelay,
		RemovePostgreSQLService,
		RemoveServiceDomain,
		RemoveSQLiteDatabase,
		RenderHelmTemplate,
		ResetIncusStack,
		RestartHostService,
		RestartVM,
		RunHostCommand,
		RunInstanceCommand,
		RunSql,
		SetHostServiceState,
		StageBuildContext,
		StartLocalLLMRuntime,
		StartVM,
		StopLocalLLMRuntime,
		StopVM,
		UninstallHelmChart,
		UpdateVMResources,
	}
}
