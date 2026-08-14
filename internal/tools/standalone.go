package tools

import (
	"encoding/json"
	"fmt"

	"github.com/wunderous/host-agents/schemas"
)

// StandaloneToolContract is the versioned public boundary for the Streamable
// HTTP standalone profile. The platform catalog is an implementation input,
// never the source of truth for what a standalone client may see.
type StandaloneToolContract struct {
	SchemaVersion     string                        `json:"schemaVersion"`
	ServerName        string                        `json:"serverName"`
	Provider          string                        `json:"provider"`
	Transport         string                        `json:"transport"`
	SupportedPlatform []string                      `json:"supportedPlatforms"`
	Smoke             StandaloneSmokeContract       `json:"smoke"`
	Tools             []StandaloneToolContractEntry `json:"tools"`
}

type StandaloneSmokeContract struct {
	RequiredTools  []string `json:"requiredTools"`
	ForbiddenTools []string `json:"forbiddenTools"`
}

type StandaloneToolContractEntry struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	Support        string `json:"support"`
}

func IsValidStandaloneSupportLevel(support string) bool {
	switch support {
	case "stable", "experimental", "legacy":
		return true
	default:
		return false
	}
}

// LoadStandaloneToolContract reads the checked-in standalone contract.
func LoadStandaloneToolContract() (StandaloneToolContract, error) {
	raw, err := schemas.FS.ReadFile("standalone-tools.json")
	if err != nil {
		return StandaloneToolContract{}, fmt.Errorf("read standalone tool contract: %w", err)
	}
	var contract StandaloneToolContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		return StandaloneToolContract{}, fmt.Errorf("parse standalone tool contract: %w", err)
	}
	return contract, nil
}

// ValidateStandaloneToolContract catches accidental catalog drift before a
// server can expose a changed public surface.
func ValidateStandaloneToolContract() error {
	contract, err := LoadStandaloneToolContract()
	if err != nil {
		return err
	}
	if contract.SchemaVersion == "" || contract.ServerName != "host-agent" || contract.Provider != "incus" || contract.Transport != "http" {
		return fmt.Errorf("invalid standalone tool contract metadata")
	}
	if len(contract.SupportedPlatform) == 0 {
		return fmt.Errorf("standalone tool contract has no supported platforms")
	}
	if len(contract.Smoke.RequiredTools) == 0 {
		return fmt.Errorf("standalone tool contract has no smoke required tools")
	}
	seenSmokeRequired := make(map[string]bool, len(contract.Smoke.RequiredTools))
	for _, name := range contract.Smoke.RequiredTools {
		if name == "" || seenSmokeRequired[name] {
			return fmt.Errorf("invalid or duplicate smoke required tool %q", name)
		}
		seenSmokeRequired[name] = true
	}
	seenSmokeForbidden := make(map[string]bool, len(contract.Smoke.ForbiddenTools))
	for _, name := range contract.Smoke.ForbiddenTools {
		if name == "" || seenSmokeForbidden[name] {
			return fmt.Errorf("invalid or duplicate smoke forbidden tool %q", name)
		}
		seenSmokeForbidden[name] = true
	}
	seen := make(map[string]bool, len(contract.Tools))
	for _, entry := range contract.Tools {
		if entry.Name == "" || seen[entry.Name] {
			return fmt.Errorf("invalid or duplicate standalone tool %q", entry.Name)
		}
		seen[entry.Name] = true
		if !StandaloneToolNames[entry.Name] {
			return fmt.Errorf("contract tool %q is not registered in the standalone allowlist", entry.Name)
		}
		if entry.Classification == "" || entry.Support == "" {
			return fmt.Errorf("standalone tool %q is missing classification or support", entry.Name)
		}
		if !IsValidStandaloneSupportLevel(entry.Support) {
			return fmt.Errorf("standalone tool %q has invalid support level %q", entry.Name, entry.Support)
		}
		if entry.Classification == "mutation" || entry.Classification == "destructive" || entry.Classification == "credential_bearing" {
			if !standaloneMutationToolNames[entry.Name] {
				return fmt.Errorf("contract marks %q as mutating but policy does not", entry.Name)
			}
		}
	}
	for name := range StandaloneToolNames {
		if !seen[name] {
			return fmt.Errorf("standalone allowlist tool %q is missing from the contract", name)
		}
	}
	for _, name := range contract.Smoke.RequiredTools {
		if !seen[name] {
			return fmt.Errorf("smoke required tool %q is missing from the contract", name)
		}
	}
	for _, name := range contract.Smoke.ForbiddenTools {
		if seen[name] {
			return fmt.Errorf("smoke forbidden tool %q is present in the contract", name)
		}
	}
	return nil
}

// StandaloneToolMetadata returns the public classification metadata attached
// to a standalone tools/list entry.
func StandaloneToolMetadata(name string) map[string]any {
	contract, err := LoadStandaloneToolContract()
	if err != nil {
		return nil
	}
	for _, entry := range contract.Tools {
		if entry.Name == name {
			return map[string]any{
				"opute": map[string]any{
					"classification": entry.Classification,
					"support":        entry.Support,
				},
			}
		}
	}
	return nil
}

// StandaloneToolNames is the intentionally narrow catalog exposed when the
// agent is used directly by a local MCP client. Platform routing and host
// onboarding tools are deliberately not part of this surface.
var StandaloneToolNames = map[string]bool{
	"embed_texts":                   true,
	"reconcile_serving_assignment":  true,
	"reconcile_postgresql_service":  true,
	"get_postgresql_service_status": true,
	"remove_postgresql_service":     true,
	// Optional compatibility/provider lifecycle retained for explicit TiDB
	// deployments; it is not part of the generic host catalog.
	"reconcile_tidb_service":              true,
	"get_tidb_service_status":             true,
	"remove_tidb_service":                 true,
	"configure_agent_connection":          true,
	"discover_service_ingress":            true,
	"run_host_command":                    true,
	"install_incus_stack":                 true,
	"reset_incus_stack":                   true,
	"probe_incus_gpu":                     true,
	"provision_container":                 true,
	"probe_gpu_container":                 true,
	"get_host_info":                       true,
	"check_local_prerequisites":           true,
	"get_local_status":                    true,
	"ensure_pgvector":                     true,
	"get_pgvector_status":                 true,
	"check_local_llm_prerequisites":       true,
	"ensure_local_llm_server_binary":      true,
	"list_local_llm_models":               true,
	"probe_local_llm":                     true,
	"install_local_llm_model":             true,
	"configure_local_llm_model":           true,
	"start_local_llm_runtime":             true,
	"configure_local_llm_runtime":         true,
	"stop_local_llm_runtime":              true,
	"remove_local_llm_model":              true,
	"ensure_local_llm_relay":              true,
	"remove_local_llm_relay":              true,
	"ensure_local_llm_k3s_proxy":          true,
	"remove_local_llm_k3s_proxy":          true,
	"ensure_cloudflared_tunnel":           true,
	"remove_local_llm_cloudflared_tunnel": true,
	"list_operations":                     true,
	"get_operation":                       true,
	"cancel_operation":                    true,
	"list_vms":                            true,
	"get_vm_info":                         true,
	"create_vm":                           true,
	"provision_vm":                        true,
	"start_vm":                            true,
	"stop_vm":                             true,
	"restart_vm":                          true,
	"update_vm_resources":                 true,
	"delete_vm":                           true,
	"install_k3s":                         true,
	"get_k3s_status":                      true,
	"restart_cluster":                     true,
	"uninstall_k3s":                       true,
	"list_namespaces":                     true,
	"list_ingress_classes":                true,
	"list_pods":                           true,
	"list_services":                       true,
	"install_postgresql":                  true,
	"get_postgresql_status":               true,
	"delete_postgresql":                   true,
	"run_sql":                             true,
	"apply_manifest":                      true,
	"put_k8s_secret":                      true,
	"get_k8s_resource":                    true,
	"delete_k8s_resource":                 true,
	"get_k8s_resource_status":             true,
	"list_k8s_events":                     true,
	"install_oci_registry":                true,
	"get_oci_registry_status":             true,
	"delete_oci_registry":                 true,
	"configure_k3s_registry":              true,
	"configure_service_domain":            true,
	"remove_service_domain":               true,
	"install_cloudflared_connector":       true,
	"delete_cloudflared_connector":        true,
	"ensure_oci_builder":                  true,
	"configure_oci_storage":               true,
	"inspect_container_storage":           true,
	"cleanup_container_storage":           true,
	"build_and_push_oci_image":            true,
	"stage_build_context":                 true,
	"ensure_host_tool":                    true,
	"render_helm_template":                true,
	"prepare_host_agent_artifacts":        true,
	"restart_host_service":                true,
	"set_host_service_state":              true,
	"ensure_host_service_supervisor":      true,
	"create_cloudflare_tunnel":            true,
	"get_cloudflare_tunnel_status":        true,
	"delete_cloudflare_tunnel":            true,
}

var standaloneMutationToolNames = map[string]bool{
	"reconcile_serving_assignment":        true,
	"configure_agent_connection":          true,
	"reconcile_postgresql_service":        true,
	"remove_postgresql_service":           true,
	"run_host_command":                    true,
	"install_incus_stack":                 true,
	"reset_incus_stack":                   true,
	"provision_container":                 true,
	"probe_gpu_container":                 true,
	"create_vm":                           true,
	"provision_vm":                        true,
	"start_vm":                            true,
	"stop_vm":                             true,
	"restart_vm":                          true,
	"update_vm_resources":                 true,
	"delete_vm":                           true,
	"install_k3s":                         true,
	"restart_cluster":                     true,
	"uninstall_k3s":                       true,
	"install_postgresql":                  true,
	"delete_postgresql":                   true,
	"run_sql":                             true,
	"apply_manifest":                      true,
	"put_k8s_secret":                      true,
	"delete_k8s_resource":                 true,
	"install_oci_registry":                true,
	"delete_oci_registry":                 true,
	"configure_k3s_registry":              true,
	"configure_service_domain":            true,
	"remove_service_domain":               true,
	"install_cloudflared_connector":       true,
	"delete_cloudflared_connector":        true,
	"ensure_oci_builder":                  true,
	"configure_oci_storage":               true,
	"cleanup_container_storage":           true,
	"build_and_push_oci_image":            true,
	"stage_build_context":                 true,
	"ensure_host_tool":                    true,
	"prepare_host_agent_artifacts":        true,
	"restart_host_service":                true,
	"set_host_service_state":              true,
	"ensure_host_service_supervisor":      true,
	"create_cloudflare_tunnel":            true,
	"delete_cloudflare_tunnel":            true,
	"cancel_operation":                    true,
	"ensure_local_llm_server_binary":      true,
	"install_local_llm_model":             true,
	"configure_local_llm_model":           true,
	"start_local_llm_runtime":             true,
	"configure_local_llm_runtime":         true,
	"stop_local_llm_runtime":              true,
	"remove_local_llm_model":              true,
	"ensure_local_llm_relay":              true,
	"remove_local_llm_relay":              true,
	"ensure_local_llm_k3s_proxy":          true,
	"remove_local_llm_k3s_proxy":          true,
	"ensure_cloudflared_tunnel":           true,
	"remove_local_llm_cloudflared_tunnel": true,
	"ensure_pgvector":                     true,
	"reconcile_tidb_service":              true,
	"remove_tidb_service":                 true,
}

func IsStandaloneMutation(name string) bool {
	return standaloneMutationToolNames[name]
}

func StandaloneToolDefinitions() []ToolDefinition {
	defs := []ToolDefinition{
		{Name: "embed_texts", Description: "Generate embeddings through the host-local, configured embedding service.", InputSchema: objectSchema(map[string]any{"texts": map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 8192}}}, []string{"texts"})},
		{Name: "run_host_command", Description: "Run a caller-declared generic host command for a serving or infrastructure assignment with an optional bounded timeout.", InputSchema: objectSchema(map[string]any{"command": map[string]any{"type": "string", "minLength": 1}, "timeoutMs": map[string]any{"type": "integer", "minimum": 0, "maximum": 7200000, "description": "Optional execution budget in milliseconds; zero uses the host runner default."}}, []string{"command"})},
		{Name: "reconcile_serving_assignment", Description: "Validate and reconcile a product-neutral serving assignment on an explicit host target. Rejects VM or ambiguous targets; process assignments use a generic user service unit and all runtimes return bounded readiness evidence.", InputSchema: objectSchema(map[string]any{
			"contractVersion": map[string]any{"type": "string", "const": "serving-assignment.v1"},
			"assignmentId":    map[string]any{"type": "string"}, "generation": map[string]any{"type": "integer", "minimum": 1},
			"idempotencyKey": map[string]any{"type": "string"}, "service": map[string]any{"type": "string"},
			"mode":    map[string]any{"type": "string", "enum": []any{"dev-process", "oci-release"}},
			"runtime": map[string]any{"type": "string", "enum": []any{"process", "podman", "kubernetes"}},
			"target":  map[string]any{"type": "object"}, "artifact": map[string]any{"type": "object"},
			"endpoints": map[string]any{"type": "array", "minItems": 1}, "readiness": map[string]any{"type": "array", "minItems": 1},
			"exposure": map[string]any{"type": "object"}, "serviceUnit": map[string]any{"type": "string"},
			"desiredState": map[string]any{"type": "string", "enum": []any{"start", "restart"}},
		}, []string{"contractVersion", "assignmentId", "generation", "idempotencyKey", "service", "mode", "runtime", "target", "artifact", "endpoints", "readiness", "exposure"})},
		{Name: "configure_agent_connection", Description: "Write caller-supplied generic agent connection environment and optionally restart the declared service.", InputSchema: objectSchema(map[string]any{"envFile": map[string]any{"type": "string"}, "environment": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "serviceName": map[string]any{"type": "string"}, "restart": map[string]any{"type": "boolean"}, "scope": map[string]any{"type": "string", "enum": []any{"user", "system"}}}, []string{"envFile", "environment"})},
		{Name: "discover_service_ingress", Description: "Resolve caller-declared ingress endpoints without product-specific hostname inference.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "endpoints": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "ingressNamespace": map[string]any{"type": "string"}, "ingressService": map[string]any{"type": "string"}}, []string{"vmName", "endpoints"})},
		{Name: "reconcile_postgresql_service", Description: "Reconcile a caller-defined PostgreSQL service and databases on an explicit Kubernetes target.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "instances": map[string]any{"type": "integer"}, "storageClass": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string"}, "databases": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "consumerSecretName": map[string]any{"type": "string"}, "consumerSecretLabel": map[string]any{"type": "string"}, "serviceOwner": map[string]any{"type": "string"}, "servicePartOf": map[string]any{"type": "string"}, "relayDeviceName": map[string]any{"type": "string"}, "restartConsumers": map[string]any{"type": "boolean"}, "localRelay": map[string]any{"type": "object"}}, []string{"vmName", "databases", "consumerSecretName", "consumerSecretLabel", "serviceOwner", "servicePartOf"})},
		{Name: "get_postgresql_service_status", Description: "Read SQL-gated readiness for a caller-defined PostgreSQL service.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "instances": map[string]any{"type": "integer"}, "databases": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "consumerSecretName": map[string]any{"type": "string"}, "consumerSecretLabel": map[string]any{"type": "string"}, "serviceOwner": map[string]any{"type": "string"}, "servicePartOf": map[string]any{"type": "string"}, "relayDeviceName": map[string]any{"type": "string"}, "localRelay": map[string]any{"type": "object"}}, []string{"vmName", "databases", "consumerSecretName", "consumerSecretLabel", "serviceOwner", "servicePartOf"})},
		{Name: "remove_postgresql_service", Description: "Remove a caller-defined PostgreSQL service after explicit confirmation.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "consumerSecretName": map[string]any{"type": "string"}, "consumerSecretLabel": map[string]any{"type": "string"}, "serviceOwner": map[string]any{"type": "string"}, "servicePartOf": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string"}, "confirm": map[string]any{"type": "boolean"}}, []string{"vmName", "consumerSecretName", "serviceOwner", "servicePartOf", "confirm"})},
		{Name: "check_local_prerequisites", Description: "Check local Incus, Kubernetes, PostgreSQL, and Cloudflare prerequisites.", InputSchema: objectSchema(nil, nil)},
		{Name: "get_local_status", Description: "Return local provider and standalone agent status.", InputSchema: objectSchema(nil, nil)},
		{Name: "reset_incus_stack", Description: "Fail-closed, ownership-checked, confirmation-gated reset of explicitly selected disposable Incus instances. Returns redacted resumable phase evidence.", InputSchema: objectSchema(map[string]any{"instanceNames": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "instancePrefix": map[string]any{"type": "string"}, "confirm": map[string]any{"type": "boolean"}, "reinstall": map[string]any{"type": "boolean"}, "dryRun": map[string]any{"type": "boolean"}, "disposableHostFingerprint": map[string]any{"type": "string"}, "expectedHostFingerprint": map[string]any{"type": "string"}, "disposableHostAuthorization": map[string]any{"type": "string"}}, []string{"confirm", "reinstall", "disposableHostFingerprint", "expectedHostFingerprint", "disposableHostAuthorization"})},
		{Name: "reconcile_tidb_service", Description: "Install or reconcile TiDB Operator and a SQL-gated caller-defined TiDB service. TiDB Operator owns credentials and the host agent projects redacted connection URLs through the consumer Secret.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "pdReplicas": map[string]any{"type": "integer"}, "tikvReplicas": map[string]any{"type": "integer"}, "tidbReplicas": map[string]any{"type": "integer"}, "storageClass": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "tidbVersion": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []any{"delete", "retain"}}}, []string{"vmName"})},
		{Name: "get_tidb_service_status", Description: "Read SQL-gated TiDB Operator platform readiness without returning credentials.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "remove_tidb_service", Description: "Destructively remove the caller-defined TidbCluster and owned data while preserving TiDB Operator. Requires confirm=true.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []any{"delete", "retain"}}, "confirm": map[string]any{"type": "boolean"}}, []string{"vmName", "confirm"})},
		{Name: "ensure_pgvector", Description: "Reconcile a pinned pgvector CloudNativePG image and ensure the vector extension in selected databases. Credentials remain CNPG-owned and are never returned.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "databases": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1}}, []string{"vmName"})},
		{Name: "get_pgvector_status", Description: "Read pgvector image and per-database extension readiness without changing the Cluster or databases.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "databases": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1}}, []string{"vmName"})},
		{Name: "check_local_llm_prerequisites", Description: "Inspect host prerequisites for the managed llama-server runtime.", InputSchema: objectSchema(nil, nil)},
		{Name: "list_local_llm_models", Description: "List models served by llama-server.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}}, nil)},
		{Name: "probe_local_llm", Description: "Probe llama-server readiness, model identity, and GPU residency.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}, "includeChat": map[string]any{"type": "boolean"}, "modelRef": map[string]any{"type": "string"}}, nil)},
		{Name: "install_local_llm_model", Description: "Exclusively load a pinned Q4_K_M GGUF generation model: unload the current llama-server model first, then start and GPU-verify the requested model. Tool embeddings remain resident.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}, "modelRef": map[string]any{"type": "string"}, "artifactPath": map[string]any{"type": "string"}, "artifactSha256": map[string]any{"type": "string"}, "artifactUri": map[string]any{"type": "string", "format": "uri"}, "baseModel": map[string]any{"type": "string"}, "revision": map[string]any{"type": "string"}, "tokenizerRevision": map[string]any{"type": "string"}, "chatTemplateHash": map[string]any{"type": "string"}, "chatTemplate": map[string]any{"type": "string"}, "chatTemplateKwargs": map[string]any{"type": "string"}, "contextSize": map[string]any{"type": "integer"}, "gpuLayers": map[string]any{"type": "integer"}, "binaryPath": map[string]any{"type": "string"}, "binaryVersion": map[string]any{"type": "string"}, "binarySha256": map[string]any{"type": "string"}, "binaryUri": map[string]any{"type": "string"}, "quantization": map[string]any{"type": "string", "enum": []any{"Q4_K_M"}}, "port": map[string]any{"type": "integer"}}, nil)},
		{Name: "configure_local_llm_model", Description: "Configure a llama-server GGUF artifact manifest.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}}, nil)},
		{Name: "start_local_llm_runtime", Description: "Start the managed llama-server systemd user service with one configured generation model resident.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}}, nil)},
		{Name: "configure_local_llm_runtime", Description: "Configure llama-server through its pinned service manifest.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}}, nil)},
		{Name: "stop_local_llm_runtime", Description: "Unload the resident generation model by stopping llama-server without deleting installed artifacts or tool embeddings.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}}, nil)},
		{Name: "remove_local_llm_model", Description: "Remove a llama-server artifact adoption record.", InputSchema: objectSchema(map[string]any{"runtime": map[string]any{"type": "string", "enum": []any{"llama-cpp"}}, "modelRef": map[string]any{"type": "string"}}, []string{"modelRef"})},
		{Name: "ensure_local_llm_relay", Description: "Bind an authenticated relay to llama-server OpenAI-compatible /v1 endpoints.", InputSchema: objectSchema(map[string]any{"sessionId": map[string]any{"type": "string"}, "listenHost": map[string]any{"type": "string"}, "listenPort": map[string]any{"type": "integer"}, "targetHost": map[string]any{"type": "string"}, "targetPort": map[string]any{"type": "integer"}, "allowedSourceIP": map[string]any{"type": "string"}, "relayToken": map[string]any{"type": "string"}}, []string{"sessionId", "listenHost", "listenPort", "targetHost", "targetPort", "allowedSourceIP", "relayToken"})},
		{Name: "remove_local_llm_relay", Description: "Remove the local-runtime relay.", InputSchema: objectSchema(map[string]any{"sessionId": map[string]any{"type": "string"}}, []string{"sessionId"})},
		{Name: "ensure_local_llm_k3s_proxy", Description: "LEGACY: deploy an nginx NodePort proxy on a gateway VM. Prefer ensure_local_llm_relay plus generic Traefik route/domain tools.", InputSchema: objectSchema(map[string]any{
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"},
			"secretName": map[string]any{"type": "string"}, "configMapName": map[string]any{"type": "string"},
			"deploymentName": map[string]any{"type": "string"}, "serviceName": map[string]any{"type": "string"},
			"containerImage": map[string]any{"type": "string"}, "nodePort": map[string]any{"type": "integer"},
			"relayHost": map[string]any{"type": "string"}, "relayPort": map[string]any{"type": "integer"},
			"relayToken": map[string]any{"type": "string"}, "bearerKey": map[string]any{"type": "string"},
		}, []string{"vmName", "nodePort", "relayHost", "relayPort", "relayToken", "bearerKey"})},
		{Name: "remove_local_llm_k3s_proxy", Description: "LEGACY: remove a k3s proxy namespace from a gateway VM. Pass namespace when not using the one-release default.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "ensure_cloudflared_tunnel", Description: "Run a token-authenticated Cloudflare connector for a caller-declared host-local target.", InputSchema: objectSchema(map[string]any{"bindingId": map[string]any{"type": "string"}, "hostname": map[string]any{"type": "string"}, "localTarget": map[string]any{"type": "string"}, "runToken": map[string]any{"type": "string"}, "connector": map[string]any{"type": "string", "const": "host"}, "allowedLocalPorts": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "quick": map[string]any{"type": "boolean"}}, []string{"bindingId", "hostname", "localTarget", "runToken", "connector"})},
		{Name: "remove_local_llm_cloudflared_tunnel", Description: "Remove the Cloudflare connector for a local llama-server exposure.", InputSchema: objectSchema(map[string]any{"bindingId": map[string]any{"type": "string"}}, []string{"bindingId"})},
		{Name: "list_operations", Description: "List local standalone operations.", InputSchema: objectSchema(map[string]any{"limit": map[string]any{"type": "integer"}}, nil)},
		{Name: "get_operation", Description: "Get a local standalone operation by ID.", InputSchema: objectSchema(map[string]any{"operationId": map[string]any{"type": "string"}}, []string{"operationId"})},
		{Name: "cancel_operation", Description: "Cancel a local standalone operation.", InputSchema: objectSchema(map[string]any{"operationId": map[string]any{"type": "string"}}, []string{"operationId"})},
		{Name: "get_k3s_status", Description: "Inspect K3s service, node readiness, and Kubernetes version.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "restart_cluster", Description: "Restart K3s inside a VM and verify the service becomes active again.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "install_postgresql", Description: "Install a single-node CloudNativePG Cluster in local K3s. CNPG owns credentials.", InputSchema: objectSchema(map[string]any{
			"vmName":    map[string]any{"type": "string"},
			"namespace": map[string]any{"type": "string"},
			"database":  map[string]any{"type": "string"},
		}, []string{"vmName"})},
		{Name: "get_postgresql_status", Description: "Inspect the standalone CloudNativePG Cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "delete_postgresql", Description: "Delete the standalone PostgreSQL namespace and resources.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "run_sql", Description: "Run bounded SQL inside the standalone PostgreSQL pod.", InputSchema: objectSchema(map[string]any{
			"vmName":    map[string]any{"type": "string"},
			"sql":       map[string]any{"type": "string"},
			"database":  map[string]any{"type": "string"},
			"namespace": map[string]any{"type": "string"},
		}, []string{"vmName", "sql"})},
		{Name: "apply_manifest", Description: "Apply a generic Kubernetes manifest to a VM-backed cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "manifest": map[string]any{"type": "string"}}, []string{"vmName", "manifest"})},
		{Name: "put_k8s_secret", Description: "Create or replace a generic Kubernetes Secret without exposing its values in tool results.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "data": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}, []string{"vmName", "name", "data"})},
		{Name: "get_k8s_resource", Description: "Fetch a generic Kubernetes resource from a VM-backed cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"}, "resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName", "kind", "resourceName"})},
		{Name: "delete_k8s_resource", Description: "Delete a generic Kubernetes resource from a VM-backed cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"}, "resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName", "kind", "resourceName"})},
		{Name: "get_k8s_resource_status", Description: "Inspect readiness of a generic Kubernetes resource.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "resourceKind": map[string]any{"type": "string"}, "resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName", "resourceKind", "resourceName", "namespace"})},
		{Name: "list_k8s_events", Description: "List bounded, read-only Kubernetes events for an explicit cluster target.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}}, []string{"vmName"})},
		{Name: "install_oci_registry", Description: "Install a generic OCI registry inside a VM-backed Kubernetes cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "storageClass": map[string]any{"type": "string"}, "nodePort": map[string]any{"type": "integer"}}, []string{"vmName"})},
		{Name: "get_oci_registry_status", Description: "Inspect the generic OCI registry deployment.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "delete_oci_registry", Description: "Delete the generic OCI registry namespace and its data resources.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "configure_k3s_registry", Description: "Configure a K3s cluster to pull images from an OCI registry endpoint.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "endpoint": map[string]any{"type": "string"}, "registry": map[string]any{"type": "string"}, "insecure": map[string]any{"type": "boolean"}}, []string{"vmName", "endpoint"})},
		{Name: "configure_service_domain", Description: "Map a configurable DNS hostname to a Kubernetes Service through Ingress.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "ingressName": map[string]any{"type": "string"}, "hostname": map[string]any{"type": "string"}, "serviceName": map[string]any{"type": "string"}, "servicePort": map[string]any{"type": "integer"}, "ingressClass": map[string]any{"type": "string"}}, []string{"vmName", "namespace", "ingressName", "hostname", "serviceName", "servicePort"})},
		{Name: "remove_service_domain", Description: "Remove a configurable Kubernetes service domain mapping.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "ingressName": map[string]any{"type": "string"}}, []string{"vmName", "namespace", "ingressName"})},
		{Name: "install_cloudflared_connector", Description: "Deploy a token-backed Cloudflare connector inside Kubernetes, with optional generic local-port-to-service mappings.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "token": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "replicas": map[string]any{"type": "integer"}, "localTargets": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"localPort", "target"}, "properties": map[string]any{"localPort": map[string]any{"type": "integer"}, "target": map[string]any{"type": "string"}}}}}, []string{"vmName", "token"})},
		{Name: "delete_cloudflared_connector", Description: "Delete the in-cluster Cloudflare connector namespace and resources.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "ensure_oci_builder", Description: "Ensure a generic host-side OCI image builder is installed and available.", InputSchema: objectSchema(map[string]any{"builder": map[string]any{"type": "string", "enum": []string{"auto", "podman", "buildah", "buildkit"}}}, nil)},
		{Name: "configure_oci_storage", Description: "Persist an age-gated OCI image storage budget and optionally prune unused images.", InputSchema: objectSchema(map[string]any{
			"runtime":       map[string]any{"type": "string", "enum": []string{"auto", "podman"}},
			"maxBytes":      map[string]any{"type": "integer", "minimum": 0},
			"minAgeSeconds": map[string]any{"type": "integer", "minimum": 3600},
			"pruneNow":      map[string]any{"type": "boolean"},
		}, nil)},
		{Name: "inspect_container_storage", Description: "Inspect runtime-reported image, container, volume, and build-cache storage usage without changing state.", InputSchema: objectSchema(map[string]any{
			"runtime": map[string]any{"type": "string", "enum": []string{"auto", "podman"}},
		}, nil)},
		{Name: "cleanup_container_storage", Description: "Age-gated cleanup of unused images and supported build cache; containers, volumes, networks, and running image references are preserved.", InputSchema: objectSchema(map[string]any{
			"runtime":       map[string]any{"type": "string", "enum": []string{"auto", "podman"}},
			"maxBytes":      map[string]any{"type": "integer", "minimum": 0},
			"minAgeSeconds": map[string]any{"type": "integer", "minimum": 3600},
			"dryRun":        map[string]any{"type": "boolean"},
		}, nil)},
		{Name: "build_and_push_oci_image", Description: "Build a generic OCI image from a host-local context directory and push it to a registry.", InputSchema: objectSchema(map[string]any{
			"contextDir":       map[string]any{"type": "string"},
			"dockerfile":       map[string]any{"type": "string"},
			"image":            map[string]any{"type": "string"},
			"builder":          map[string]any{"type": "string", "enum": []string{"auto", "podman", "buildah", "buildkit"}},
			"insecureRegistry": map[string]any{"type": "boolean"},
			"untagAfterPush":   map[string]any{"type": []string{"boolean", "null"}},
			"platform":         map[string]any{"type": "string"},
			"buildArgs":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		}, []string{"contextDir", "image"})},
		{Name: "stage_build_context", Description: "Write a caller-provided build context into an allowlisted host directory for build_and_push_oci_image.", InputSchema: objectSchema(map[string]any{
			"destDir":      map[string]any{"type": "string"},
			"files":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"fileEncoding": map[string]any{"type": "string", "enum": []string{"utf8", "base64"}},
		}, []string{"destDir", "files"})},
		{Name: "install_incus_stack", Description: "Install or upgrade a pinned Incus feature release from the signed Zabbly repository. QEMU is optional for VM profiles; GPU container profiles do not install it.", InputSchema: objectSchema(map[string]any{"incusPackage": map[string]any{"type": "string"}, "qemuPackage": map[string]any{"type": "string"}, "gpuPackages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "incusChannel": map[string]any{"type": "string", "enum": []string{"stable", "lts-7.0", "lts-6.0"}}, "incusVersion": map[string]any{"type": "string"}, "installQemu": map[string]any{"type": "boolean"}}, nil)},
		{Name: "probe_incus_gpu", Description: "Inspect WSL GPU devices/libraries and host virtualization versions; does not claim container GPU inference success.", InputSchema: objectSchema(nil, nil)},
		{Name: "provision_container", Description: "Launch or reuse a persistent Incus system container with optional GPU, WSL GPU libraries, nesting, and model volume.", InputSchema: objectSchema(map[string]any{"containerName": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "disk": map[string]any{"type": "string"}, "gpu": map[string]any{"type": "boolean"}, "wslGpuLibs": map[string]any{"type": "boolean"}, "nesting": map[string]any{"type": "boolean"}, "port": map[string]any{"type": "integer"}, "modelVolume": map[string]any{"type": "string"}}, []string{"containerName"})},
		{Name: "probe_gpu_container", Description: "Launch a disposable Incus system container, probe GPU visibility and NVML, then delete it.", InputSchema: objectSchema(nil, nil)},
		{Name: "ensure_host_tool", Description: "Ensure an explicitly allowlisted generic host build/runtime or CUDA build tool is installed and available.", InputSchema: objectSchema(map[string]any{"tool": map[string]any{"type": "string", "enum": []string{"bun", "gcc", "g++", "go", "podman", "buildah", "buildkitd", "cloudflared", "helm", "cmake", "ninja", "nvcc"}}}, []string{"tool"})},
		{Name: "render_helm_template", Description: "Render a Helm chart to Kubernetes manifests on the host using helm template.", InputSchema: objectSchema(map[string]any{"chartPath": map[string]any{"type": "string"}, "releaseName": map[string]any{"type": "string"}, "valuesFiles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "set": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "namespace": map[string]any{"type": "string"}}, []string{"chartPath", "releaseName"})},
		{Name: "prepare_host_agent_artifacts", Description: "Build Linux host-agent binaries into a caller-selected directory for platform image builds.", InputSchema: objectSchema(map[string]any{"sourceDir": map[string]any{"type": "string"}, "destDir": map[string]any{"type": "string"}, "archs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, []string{"sourceDir", "destDir"})},
		{Name: "restart_host_service", Description: "Restart a validated host service through the host service supervisor.", InputSchema: objectSchema(map[string]any{"serviceName": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.@:-]+$"}}, []string{"serviceName"})},
		{Name: "set_host_service_state", Description: "Start, stop, restart, enable, or disable a validated host service.", InputSchema: objectSchema(map[string]any{"serviceName": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.@:-]+$"}, "state": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "enable", "disable"}}, "scope": map[string]any{"type": "string", "enum": []string{"user", "system"}}}, []string{"serviceName", "state"})},
		{Name: "ensure_host_service_supervisor", Description: "Ensure a caller-declared host service supervisor is ready and persistent.", InputSchema: objectSchema(map[string]any{"scope": map[string]any{"type": "string", "enum": []string{"user", "system"}}}, nil)},
		{Name: "create_cloudflare_tunnel", Description: "Start a token-authenticated Cloudflare Tunnel for an allowed local target.", InputSchema: objectSchema(map[string]any{
			"bindingId":   map[string]any{"type": "string"},
			"hostname":    map[string]any{"type": "string"},
			"localTarget": map[string]any{"type": "string"},
			"runToken":    map[string]any{"type": "string"},
			"quick":       map[string]any{"type": "boolean"},
		}, []string{"bindingId", "localTarget"})},
		{Name: "get_cloudflare_tunnel_status", Description: "Inspect a local Cloudflare Tunnel.", InputSchema: objectSchema(map[string]any{"bindingId": map[string]any{"type": "string"}, "localTarget": map[string]any{"type": "string"}}, []string{"bindingId"})},
		{Name: "delete_cloudflare_tunnel", Description: "Stop a local Cloudflare Tunnel.", InputSchema: objectSchema(map[string]any{"bindingId": map[string]any{"type": "string"}}, []string{"bindingId"})},
	}
	return appendLocalLLMDefinitions(defs)
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	out := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}
