package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/schemas"
)

// ToolDefinition mirrors MCP tool metadata from embedded JSON schemas.
type ToolDefinition struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Execution    map[string]any `json:"execution,omitempty"`
	Meta         map[string]any `json:"_meta,omitempty"`
}

type catalogMeta struct {
	ExcludedFromCatalog []string `json:"excludedFromCatalog"`
}

// CatalogExcludedToolNames are omitted from agent-facing tools/list.
var CatalogExcludedToolNames = map[string]bool{
	"list_operations": true,
	"get_operation":   true,
	"list_tasks":      true,
	"get_task":        true,
	"agent_shell":     true,
	// Guest exec lives in the vm-exec static MCP for CPC-local cells, but tunnel hosts
	"exec_command":                true,
	"ensure_sql_connector":        true,
	"get_sql_connector_status":    true,
	"release_sql_connector":       true,
	"install_sql_forward_sidecar": true,
	"ensure_cloudflared_tunnel":   true,
	"probe_host_exposure":         true,
	"remove_host_exposure":        true,
	"ensure_host_firewall_rule":   true,
	"configure_host_network":      true,
}

// IncusOmittedToolNames are not supported on the Incus-only Linux host agent.
var IncusOmittedToolNames = map[string]bool{
	"ensure_k3d":                     true,
	"switch_infrastructure_provider": true,
	"list_vm_network_devices":        true,
}

// OmittedToolPrefixes filter bridge-backed tool families not implemented in the Go host agent.
var OmittedToolPrefixes = []string{
	"postgresql",
	"service_storage",
	"service_domain",
	"onboarding",
}

// IncusInventoryTools are included in the Incus catalog but omitted from the all-tools export subset.
var IncusInventoryTools = []string{"list_vms", "get_vm_info"}

// NormalizeProviderID maps wire/env provider values to a catalog key.
func NormalizeProviderID(providerID string) string {
	switch strings.ToLower(strings.TrimSpace(providerID)) {
	case "", "incus":
		return "incus"
	default:
		return "incus"
	}
}

func loadToolDefinitionsFile(filename string) ([]ToolDefinition, error) {
	raw, err := schemas.FS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", filename, err)
	}
	var defs []ToolDefinition
	if err := json.Unmarshal(raw, &defs); err != nil {
		return nil, fmt.Errorf("parse schema %s: %w", filename, err)
	}
	return defs, nil
}

// LoadAllToolDefinitions reads embedded schema JSON for the provider catalog.
func LoadAllToolDefinitions(providerID string) ([]ToolDefinition, error) {
	_ = NormalizeProviderID(providerID)
	defs, err := loadToolDefinitionsFile("incus-tools.json")
	if err != nil {
		return nil, err
	}
	defs = appendLocalLLMDefinitions(defs)
	defs = appendGenericHostDefinitions(defs)
	return augmentIncusInventoryTools(defs)
}

// postgresqlServiceRelaySchema is shared by every generic PostgreSQL service
// definition so the relay ownership contract is identical in fallback and
// standalone catalogs. The host agent remains product-neutral: these are
// generic loopback relay fields, not Opute-specific topology.
func postgresqlServiceRelaySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sessionId":       map[string]any{"type": "string", "minLength": 1},
			"listenHost":      map[string]any{"type": "string"},
			"listenPort":      map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
			"targetHost":      map[string]any{"type": "string"},
			"targetPort":      map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"ttlSeconds":      map[string]any{"type": "integer", "minimum": 10, "maximum": 3600},
			"relayToken":      map[string]any{"type": "string", "minLength": 32},
			"persistent":      map[string]any{"type": "boolean"},
			"replaceExisting": map[string]any{"type": "boolean"},
		},
	}
}

func appendGenericHostDefinitions(defs []ToolDefinition) []ToolDefinition {
	needed := map[string]bool{
		"embed_texts":                      true,
		"reconcile_serving_assignment":     true,
		"configure_agent_connection":       true,
		"discover_service_ingress":         true,
		"reconcile_postgresql_service":     true,
		"get_postgresql_service_status":    true,
		"remove_postgresql_service":        true,
		"release_postgresql_service_relay": true,
		"reconcile_tidb_service":           true,
		"get_tidb_service_status":          true,
		"remove_tidb_service":              true,
		"install_incus_stack":              true,
		"probe_incus_gpu":                  true,
		"provision_container":              true,
		"probe_gpu_container":              true,
		"ensure_oci_builder":               true,
		"configure_oci_storage":            true,
		"inspect_container_storage":        true,
		"cleanup_container_storage":        true,
		"build_and_push_oci_image":         true,
		"stage_build_context":              true,
		"ensure_host_tool":                 true,
		"set_host_service_state":           true,
		"ensure_host_service_supervisor":   true,
		"apply_manifest":                   true,
		"delete_k8s_resource":              true,
		"put_k8s_secret":                   true,
		"install_oci_registry":             true,
		"configure_k3s_registry":           true,
		"install_cloudflared_connector":    true,
		"delete_cloudflared_connector":     true,
		"configure_service_domain":         true,
		"remove_service_domain":            true,
		"ensure_pgvector":                  true,
		"get_pgvector_status":              true,
		"reset_incus_stack":                true,
	}
	seen := make(map[string]bool, len(needed))
	for _, definition := range defs {
		if needed[definition.Name] {
			seen[definition.Name] = true
		}
	}
	if len(seen) == len(needed) {
		return defs
	}
	defs = append(defs, ToolDefinition{
		Name: "embed_texts", Title: "Generate host-local embeddings", Description: "Generate embeddings through the host-local, configured embedding service. The endpoint and model are controlled by the host agent.", InputSchema: map[string]any{"type": "object", "required": []string{"texts"}, "properties": map[string]any{"texts": map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 8192}}}}, OutputSchema: map[string]any{"type": "object", "required": []string{"model", "dimensions", "embeddings"}},
	}, ToolDefinition{
		Name: "reconcile_serving_assignment", Title: "Reconcile generic serving assignment", Description: "Validate and reconcile a product-neutral serving assignment against an explicit host target. Rejects VM or ambiguous targets and returns bounded readiness evidence.", InputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "assignmentId", "generation", "idempotencyKey", "service", "mode", "runtime", "target", "artifact", "endpoints", "readiness", "exposure"}, "properties": map[string]any{"contractVersion": map[string]any{"type": "string", "const": "serving-assignment.v1"}, "assignmentId": map[string]any{"type": "string"}, "generation": map[string]any{"type": "integer", "minimum": 1}, "idempotencyKey": map[string]any{"type": "string"}, "service": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"dev-process", "oci-release"}}, "runtime": map[string]any{"type": "string", "enum": []string{"process", "podman", "kubernetes"}}, "target": map[string]any{"type": "object"}, "artifact": map[string]any{"type": "object"}, "endpoints": map[string]any{"type": "array", "minItems": 1}, "readiness": map[string]any{"type": "array", "minItems": 1}, "exposure": map[string]any{"type": "object"}, "serviceUnit": map[string]any{"type": "string"}, "desiredState": map[string]any{"type": "string", "enum": []string{"start", "restart"}}, "restartPolicy": map[string]any{"type": "string", "enum": []string{"no", "on-failure", "always"}}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "reconcile_postgresql_service", Title: "Reconcile PostgreSQL service", Description: "Reconcile a caller-defined PostgreSQL service and databases on an explicit Kubernetes target. Credentials remain operator-owned and are never returned.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "databases", "consumerSecretName", "consumerSecretLabel", "serviceOwner", "servicePartOf"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "instances": map[string]any{"type": "integer"}, "storageClass": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []string{"delete", "retain"}}, "databases": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}, "consumerSecretName": map[string]any{"type": "string"}, "consumerSecretLabel": map[string]any{"type": "string"}, "serviceOwner": map[string]any{"type": "string"}, "servicePartOf": map[string]any{"type": "string"}, "relayDeviceName": map[string]any{"type": "string"}, "restartConsumers": map[string]any{"type": "boolean"}, "localRelay": postgresqlServiceRelaySchema()}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "get_postgresql_service_status", Title: "Get PostgreSQL service status", Description: "Read SQL-gated readiness for a caller-defined PostgreSQL service without returning credentials.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "databases", "consumerSecretName", "consumerSecretLabel", "serviceOwner", "servicePartOf"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "instances": map[string]any{"type": "integer"}, "databases": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}, "consumerSecretName": map[string]any{"type": "string"}, "consumerSecretLabel": map[string]any{"type": "string"}, "serviceOwner": map[string]any{"type": "string"}, "servicePartOf": map[string]any{"type": "string"}, "relayDeviceName": map[string]any{"type": "string"}, "localRelay": postgresqlServiceRelaySchema()}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "remove_postgresql_service", Title: "Remove PostgreSQL service", Description: "Remove a caller-defined PostgreSQL service and owned data after explicit confirmation.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "consumerSecretName", "serviceOwner", "servicePartOf", "confirm"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "consumerSecretName": map[string]any{"type": "string"}, "consumerSecretLabel": map[string]any{"type": "string"}, "serviceOwner": map[string]any{"type": "string"}, "servicePartOf": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []string{"delete", "retain"}}, "confirm": map[string]any{"type": "boolean"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "release_postgresql_service_relay", Title: "Release PostgreSQL service relay", Description: "Release one caller-owned PostgreSQL service relay without changing the underlying service or databases. The relay capability is required and is never returned in results.", InputSchema: map[string]any{"type": "object", "required": []string{"sessionId", "relayToken"}, "properties": map[string]any{"sessionId": map[string]any{"type": "string", "minLength": 1}, "relayToken": map[string]any{"type": "string", "minLength": 32}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "configure_agent_connection", Title: "Configure generic agent connection", Description: "Write caller-supplied agent connection environment and optionally restart the declared service. Values are redacted from results.", InputSchema: map[string]any{"type": "object", "required": []string{"envFile", "environment"}, "properties": map[string]any{"envFile": map[string]any{"type": "string"}, "environment": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "serviceName": map[string]any{"type": "string"}, "restart": map[string]any{"type": "boolean"}, "scope": map[string]any{"type": "string", "enum": []string{"user", "system"}}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "discover_service_ingress", Title: "Discover service ingress", Description: "Resolve caller-declared service ingress endpoints on an explicit Kubernetes target. No product hostnames or ports are inferred.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "endpoints"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "endpoints": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object"}}, "ingressNamespace": map[string]any{"type": "string"}, "ingressService": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "install_incus_stack", Title: "Install Incus virtualization stack", Description: "Install or upgrade a pinned Incus feature release from the signed Zabbly repository. QEMU is optional for VM profiles; GPU container profiles do not install it.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"incusPackage": map[string]any{"type": "string"}, "qemuPackage": map[string]any{"type": "string"}, "gpuPackages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "incusChannel": map[string]any{"type": "string", "enum": []string{"stable", "lts-7.0", "lts-6.0"}}, "incusVersion": map[string]any{"type": "string"}, "installQemu": map[string]any{"type": "boolean"}}},
	}, ToolDefinition{
		Name: "probe_incus_gpu", Title: "Probe Incus GPU capability", Description: "Inspect WSL GPU devices/libraries and host virtualization versions; does not claim container GPU inference success.", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "provision_container", Title: "Provision Incus system container", Description: "Launch or reuse a persistent Incus system container with optional GPU, WSL GPU libraries, nesting, and model volume.", InputSchema: map[string]any{"type": "object", "required": []string{"containerName"}, "properties": map[string]any{"containerName": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "disk": map[string]any{"type": "string"}, "gpu": map[string]any{"type": "boolean"}, "wslGpuLibs": map[string]any{"type": "boolean"}, "nesting": map[string]any{"type": "boolean"}, "port": map[string]any{"type": "integer"}, "modelVolume": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "probe_gpu_container", Title: "Probe system container GPU", Description: "Launch a disposable Incus system container, probe GPU visibility and NVML, then delete it.", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:         "ensure_oci_builder",
		Title:        "Ensure OCI image builder",
		Description:  "Ensure a generic host-side OCI image builder is installed and available.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"builder": map[string]any{"type": "string", "enum": []string{"auto", "podman", "buildah", "buildkit"}}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"builder", "path", "available"}},
	}, ToolDefinition{
		Name:        "configure_oci_storage",
		Title:       "Configure OCI storage retention",
		Description: "Persist an age-gated OCI image storage budget and optionally prune unused images.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"runtime":       map[string]any{"type": "string", "enum": []string{"auto", "podman"}},
			"maxBytes":      map[string]any{"type": "integer", "minimum": 0},
			"minAgeSeconds": map[string]any{"type": "integer", "minimum": 3600},
			"pruneNow":      map[string]any{"type": "boolean"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:         "inspect_container_storage",
		Title:        "Inspect container runtime storage",
		Description:  "Inspect runtime-reported image, container, volume, and build-cache storage usage without changing state.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"runtime": map[string]any{"type": "string", "enum": []string{"auto", "podman"}}}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "cleanup_container_storage",
		Title:       "Clean up container runtime storage",
		Description: "Age-gated cleanup of unused images and supported build cache; containers, volumes, networks, and running image references are preserved.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"runtime":       map[string]any{"type": "string", "enum": []string{"auto", "podman"}},
			"maxBytes":      map[string]any{"type": "integer", "minimum": 0},
			"minAgeSeconds": map[string]any{"type": "integer", "minimum": 3600},
			"dryRun":        map[string]any{"type": "boolean"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "build_and_push_oci_image",
		Title:       "Build and push OCI image",
		Description: "Build a generic OCI image from a host-local context directory and push it to a registry.",
		InputSchema: map[string]any{"type": "object", "required": []string{"contextDir", "image"}, "properties": map[string]any{
			"contextDir":       map[string]any{"type": "string"},
			"dockerfile":       map[string]any{"type": "string"},
			"image":            map[string]any{"type": "string"},
			"builder":          map[string]any{"type": "string", "enum": []string{"auto", "podman", "buildah", "buildkit"}},
			"insecureRegistry": map[string]any{"type": "boolean"},
			"untagAfterPush":   map[string]any{"type": []string{"boolean", "null"}},
			"platform":         map[string]any{"type": "string"},
			"buildArgs":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "stage_build_context",
		Title:       "Stage build context",
		Description: "Write a caller-provided build context into an allowlisted host directory for build_and_push_oci_image.",
		InputSchema: map[string]any{"type": "object", "required": []string{"destDir", "files"}, "properties": map[string]any{
			"destDir":      map[string]any{"type": "string"},
			"files":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"fileEncoding": map[string]any{"type": "string", "enum": []string{"utf8", "base64"}},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "reconcile_postgresql_service", Title: "Reconcile PostgreSQL service", Description: "Reconcile a caller-defined CloudNativePG PostgreSQL service. Credentials remain operator-owned and are never returned.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "databases", "consumerSecretName", "consumerSecretLabel", "serviceOwner", "servicePartOf"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "instances": map[string]any{"type": "integer"}, "storageClass": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []string{"delete", "retain"}}, "restartConsumers": map[string]any{"type": "boolean"}, "localRelay": postgresqlServiceRelaySchema(),
		}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "reconcile_tidb_service", Title: "Reconcile TiDB service", Description: "Install or reconcile an explicitly selected TiDB service. Credentials remain operator-owned and are not returned.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "clusterName", "namespace"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "pdReplicas": map[string]any{"type": "integer"}, "tikvReplicas": map[string]any{"type": "integer"}, "tidbReplicas": map[string]any{"type": "integer"}, "storageClass": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "tidbVersion": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []string{"delete", "retain"}},
		}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "get_tidb_service_status", Title: "Get TiDB service status", Description: "Read TiDB service readiness without returning credentials.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "clusterName", "namespace"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "remove_tidb_service", Title: "Remove TiDB service", Description: "Destructively remove a caller-defined TidbCluster. Requires confirm=true.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "clusterName", "namespace", "confirm"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []string{"delete", "retain"}}, "confirm": map[string]any{"type": "boolean"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "get_postgresql_service_status", Title: "Get PostgreSQL service status", Description: "Read SQL-gated CloudNativePG PostgreSQL service readiness without returning credentials. When already ready, an optional localRelay is reconciled without enqueueing a second CNPG task.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "instances": map[string]any{"type": "integer"}, "localRelay": postgresqlServiceRelaySchema()}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "ensure_pgvector", Title: "Ensure pgvector", Description: "Reconcile a pinned pgvector CloudNativePG image and ensure the vector extension in selected databases. Credentials remain CNPG-owned and are never returned.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "databases": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
		}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "get_pgvector_status", Title: "Get pgvector status", Description: "Read pgvector image and per-database extension readiness without changing the Cluster or databases.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "databases": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
		}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "remove_postgresql_service", Title: "Remove PostgreSQL service", Description: "Destructively remove a caller-defined PostgreSQL CNPG service and owned data while preserving the operator. Requires confirm=true.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "confirm"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "retentionPolicy": map[string]any{"type": "string", "enum": []string{"delete", "retain"}}, "localRelay": postgresqlServiceRelaySchema(), "confirm": map[string]any{"type": "boolean"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "reset_incus_stack", Title: "Reset Incus stack", Description: "Fail-closed, ownership-checked, confirmation-gated reset of explicitly selected disposable Incus instances. Returns redacted resumable phase evidence.", InputSchema: map[string]any{"type": "object", "required": []string{"confirm", "reinstall", "disposableHostFingerprint", "expectedHostFingerprint", "disposableHostAuthorization"}, "properties": map[string]any{"instanceNames": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "instancePrefix": map[string]any{"type": "string"}, "confirm": map[string]any{"type": "boolean"}, "reinstall": map[string]any{"type": "boolean"}, "dryRun": map[string]any{"type": "boolean"}, "disposableHostFingerprint": map[string]any{"type": "string"}, "expectedHostFingerprint": map[string]any{"type": "string"}, "disposableHostAuthorization": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:         "ensure_host_tool",
		Title:        "Ensure generic host tool",
		Description:  "Ensure an explicitly allowlisted generic host build/runtime tool is installed and available.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"tool"}, "properties": map[string]any{"tool": map[string]any{"type": "string", "enum": []string{"bun", "gcc", "g++", "go", "podman", "buildah", "buildkitd", "cloudflared", "helm", "cmake", "ninja", "nvcc"}}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"tool", "path", "available"}},
	}, ToolDefinition{
		Name:         "render_helm_template",
		Title:        "Render Helm template",
		Description:  "Render a Helm chart to Kubernetes manifests on the host using helm template.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"chartPath", "releaseName"}, "properties": map[string]any{"chartPath": map[string]any{"type": "string"}, "releaseName": map[string]any{"type": "string"}, "valuesFiles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "set": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "namespace": map[string]any{"type": "string"}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"manifest"}},
	}, ToolDefinition{
		Name:         "prepare_host_agent_artifacts",
		Title:        "Prepare host-agent artifacts",
		Description:  "Build Linux host-agent binaries into a caller-selected directory for platform image builds.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"sourceDir", "destDir"}, "properties": map[string]any{"sourceDir": map[string]any{"type": "string"}, "destDir": map[string]any{"type": "string"}, "archs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "set_host_service_state",
		Title:       "Set host service state",
		Description: "Start, stop, restart, enable, or disable a validated host service; user scope is the default.",
		InputSchema: map[string]any{"type": "object", "required": []string{"serviceName", "state"}, "properties": map[string]any{
			"serviceName": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_.@:-]+$`},
			"state":       map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "enable", "disable"}},
			"scope":       map[string]any{"type": "string", "enum": []string{"user", "system"}},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"serviceName", "state", "scope", "status"}},
	}, ToolDefinition{
		Name:        "ensure_host_service_supervisor",
		Title:       "Ensure host service supervisor",
		Description: "Ensure the caller-declared host service supervisor is available and persistent; user-scoped services receive an idempotent persistent user-manager contract.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"scope": map[string]any{"type": "string", "enum": []string{"user", "system"}},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"scope", "status", "persistent"}},
	}, ToolDefinition{
		Name:        "ensure_host_tool",
		Title:       "Ensure generic host tool",
		Description: "Ensure an explicitly allowlisted generic host build/runtime tool is installed and available.",
		InputSchema: map[string]any{"type": "object", "required": []string{"tool"}, "properties": map[string]any{
			"tool": map[string]any{"type": "string", "enum": []string{"bun", "gcc", "g++", "go", "podman", "buildah", "buildkitd", "cloudflared", "helm", "cmake", "ninja", "nvcc"}},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"tool", "path", "available"}},
	}, ToolDefinition{
		Name:        "apply_manifest",
		Title:       "Apply Kubernetes manifest",
		Description: "Apply a generic Kubernetes manifest to a VM-backed cluster.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "manifest"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "manifest": map[string]any{"type": "string"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "delete_k8s_resource",
		Title:       "Delete Kubernetes resource",
		Description: "Delete a generic Kubernetes resource from a VM-backed cluster.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "kind", "resourceName"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"}, "resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "put_k8s_secret",
		Title:       "Put Kubernetes Secret",
		Description: "Create or replace a generic Kubernetes Secret without returning its values.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "name", "data"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "data": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "install_oci_registry",
		Title:       "Install OCI registry",
		Description: "Install a generic OCI registry inside a VM-backed Kubernetes cluster.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "storageClass": map[string]any{"type": "string"}, "nodePort": map[string]any{"type": "integer"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "configure_k3s_registry",
		Title:       "Configure K3s registry",
		Description: "Configure a K3s cluster to pull images from an OCI registry endpoint.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "endpoint"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "endpoint": map[string]any{"type": "string"}, "registry": map[string]any{"type": "string"}, "insecure": map[string]any{"type": "boolean"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "install_cloudflared_connector",
		Title:       "Install Cloudflare connector",
		Description: "Deploy a token-backed Cloudflare connector inside Kubernetes.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "token"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "token": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "replicas": map[string]any{"type": "integer"}, "localTargets": map[string]any{"type": "array"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "delete_cloudflared_connector",
		Title:       "Delete Cloudflare connector",
		Description: "Delete the in-cluster Cloudflare connector resources.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "configure_service_domain",
		Title:       "Configure service domain",
		Description: "Map a Kubernetes Service to a caller-selected hostname through the configured ingress class.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "namespace", "ingressName", "hostname", "serviceName", "servicePort"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "ingressName": map[string]any{"type": "string"}, "hostname": map[string]any{"type": "string"}, "serviceName": map[string]any{"type": "string"}, "servicePort": map[string]any{"type": "integer"}, "ingressClass": map[string]any{"type": "string"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name:        "remove_service_domain",
		Title:       "Remove service domain",
		Description: "Remove a caller-selected Kubernetes Service domain mapping.",
		InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "namespace", "ingressName"}, "properties": map[string]any{
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "ingressName": map[string]any{"type": "string"},
		}},
		OutputSchema: map[string]any{"type": "object"},
	})
	// Keep the embedded JSON catalogs authoritative where a definition already
	// exists, while allowing the Go catalog to fill newly implemented generic
	// operations until the generated files are refreshed.
	unique := make([]ToolDefinition, 0, len(defs))
	seen = make(map[string]bool, len(defs))
	for _, definition := range defs {
		if seen[definition.Name] {
			continue
		}
		seen[definition.Name] = true
		unique = append(unique, definition)
	}
	return unique
}

func appendLocalLLMDefinitions(defs []ToolDefinition) []ToolDefinition {
	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		seen[d.Name] = true
	}
	inputs := map[string]map[string]any{
		"check_local_llm_prerequisites": {"type": "object", "properties": map[string]any{}},
		"ensure_local_llm_server_binary": {"type": "object", "properties": map[string]any{
			"runtime":           map[string]any{"type": "string", "enum": []string{"llama-cpp"}},
			"sourceUri":         map[string]any{"type": "string", "format": "uri"},
			"sourceSha256":      map[string]any{"type": "string"},
			"sourceRevision":    map[string]any{"type": "string"},
			"outputPath":        map[string]any{"type": "string"},
			"cudaArchitectures": map[string]any{"type": "string"},
		}},
		"list_local_llm_models": {"type": "object", "properties": map[string]any{"runtime": map[string]any{"type": "string", "enum": []string{"llama-cpp"}}, "includeChat": map[string]any{"type": "boolean"}}},
		"probe_local_llm": {"type": "object", "properties": map[string]any{
			"runtime":     map[string]any{"type": "string", "enum": []string{"llama-cpp"}},
			"includeChat": map[string]any{"type": "boolean"},
			"modelRef":    map[string]any{"type": "string"},
			"modelPreset": map[string]any{"type": "string", "enum": []string{"qwen3.5", "qwen3.5-0.8b"}},
			"numGpu":      map[string]any{"type": "integer"},
			"numCtx":      map[string]any{"type": "integer"},
		}},
		"install_local_llm_model": {"type": "object", "properties": map[string]any{
			"runtime":                 map[string]any{"type": "string", "enum": []string{"llama-cpp"}},
			"modelFamily":             map[string]any{"type": "string"},
			"modelVariant":            map[string]any{"type": "string"},
			"installSource":           map[string]any{"type": "string"},
			"modelRef":                map[string]any{"type": "string"},
			"modelPreset":             map[string]any{"type": "string", "enum": []string{"qwen3.5", "qwen3.5-0.8b"}},
			"createAs":                map[string]any{"type": "string"},
			"numGpu":                  map[string]any{"type": "integer"},
			"numCtx":                  map[string]any{"type": "integer"},
			"template":                map[string]any{"type": "string"},
			"artifactPath":            map[string]any{"type": "string"},
			"artifactSha256":          map[string]any{"type": "string"},
			"artifactUri":             map[string]any{"type": "string", "format": "uri"},
			"baseModel":               map[string]any{"type": "string"},
			"revision":                map[string]any{"type": "string"},
			"tokenizerRevision":       map[string]any{"type": "string"},
			"chatTemplateHash":        map[string]any{"type": "string"},
			"chatTemplate":            map[string]any{"type": "string"},
			"chatTemplateKwargs":      map[string]any{"type": "string"},
			"contextSize":             map[string]any{"type": "integer"},
			"gpuLayers":               map[string]any{"type": "integer"},
			"binaryPath":              map[string]any{"type": "string"},
			"binaryVersion":           map[string]any{"type": "string"},
			"binarySha256":            map[string]any{"type": "string"},
			"binaryUri":               map[string]any{"type": "string", "format": "uri"},
			"binarySource":            map[string]any{"type": "string", "enum": []string{"host-build"}},
			"sourceRevision":          map[string]any{"type": "string"},
			"sourceSha256":            map[string]any{"type": "string"},
			"binaryBuildSourceUri":    map[string]any{"type": "string", "format": "uri"},
			"binaryBuildSourceSha256": map[string]any{"type": "string"},
			"binaryBuildRevision":     map[string]any{"type": "string"},
			"cudaEnabled":             map[string]any{"type": "boolean"},
			"cudaArchitectures":       map[string]any{"type": "string"},
			"quantization":            map[string]any{"type": "string", "enum": []string{"Q4_K_M"}},
			"port":                    map[string]any{"type": "integer"},
		}},
		"configure_local_llm_model": {"type": "object", "properties": map[string]any{
			"modelRef":    map[string]any{"type": "string"},
			"modelPreset": map[string]any{"type": "string", "enum": []string{"qwen3.5", "qwen3.5-0.8b"}},
			"fromRef":     map[string]any{"type": "string"},
			"numGpu":      map[string]any{"type": "integer"},
			"numCtx":      map[string]any{"type": "integer"},
			"template":    map[string]any{"type": "string"},
		}},
		"start_local_llm_runtime": {"type": "object", "properties": map[string]any{"runtime": map[string]any{"type": "string", "enum": []string{"llama-cpp"}}, "runtimeId": map[string]any{"type": "string"}}},
		"configure_local_llm_runtime": {"type": "object", "properties": map[string]any{
			"gpuOverheadMiB":  map[string]any{"type": "integer"},
			"maxLoadedModels": map[string]any{"type": "integer"},
			"numParallel":     map[string]any{"type": "integer"},
			"flashAttention":  map[string]any{"type": "boolean"},
		}},
		"stop_local_llm_runtime": {"type": "object", "properties": map[string]any{"runtime": map[string]any{"type": "string", "enum": []string{"llama-cpp"}}, "runtimeId": map[string]any{"type": "string"}}},
		"remove_local_llm_model": {"type": "object", "required": []string{"modelRef"}, "properties": map[string]any{"modelRef": map[string]any{"type": "string"}, "purge": map[string]any{"type": "boolean"}}},
		"ensure_local_llm_relay": {"type": "object", "required": []string{"sessionId", "listenHost", "listenPort", "targetHost", "targetPort", "incomingToken", "allowedSourceCIDRs"}, "properties": map[string]any{"upstreamToken": map[string]any{"type": "string"}, "allowedSourceCIDRs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}},
		"remove_local_llm_relay": {"type": "object", "required": []string{"sessionId"}, "properties": map[string]any{}},
		"ensure_local_llm_k3s_proxy": {"type": "object", "required": []string{"vmName", "nodePort", "relayHost", "relayPort", "relayToken", "bearerKey"}, "properties": map[string]any{
			"namespace": map[string]any{"type": "string"}, "secretName": map[string]any{"type": "string"},
			"configMapName": map[string]any{"type": "string"}, "deploymentName": map[string]any{"type": "string"},
			"serviceName": map[string]any{"type": "string"}, "containerImage": map[string]any{"type": "string"},
		}},
		"remove_local_llm_k3s_proxy": {"type": "object", "required": []string{"vmName"}, "properties": map[string]any{
			"namespace": map[string]any{"type": "string"},
		}},
		"remove_local_llm_cloudflared_tunnel": {"type": "object", "required": []string{"bindingId"}, "properties": map[string]any{}},
	}
	for name, schema := range inputs {
		if !seen[name] {
			desc := "Opute-managed local llama-server operation"
			switch name {
			case "check_local_llm_prerequisites":
				desc = "Inspect local llama-server readiness and GPU/CUDA diagnostics."
			case "ensure_local_llm_server_binary":
				desc = "Build and verify a pinned CUDA llama-server binary from source on the host."
			case "install_local_llm_model":
				desc = "Exclusively switch the resident generation model: unload the current model, then load and GPU-verify the requested pinned artifact."
			case "configure_local_llm_model":
				desc = "Configure a local llama-server GGUF artifact."
			case "start_local_llm_runtime":
				desc = "Start/restart the Opute-managed llama-server unit with one generation model resident."
			case "configure_local_llm_runtime":
				desc = "Persist and apply Opute-managed llama-server runtime settings."
			case "probe_local_llm":
				desc = "Probe llama-server readiness, loaded model identity, and GPU residency."
			case "stop_local_llm_runtime":
				desc = "Unload the resident generation model without deleting artifacts or tool embeddings."
			case "remove_local_llm_model":
				desc = "Remove a local llama-server artifact adoption record."
			}
			defs = append(defs, ToolDefinition{Name: name, Title: name, Description: desc, InputSchema: schema, OutputSchema: map[string]any{"type": "object"}})
		}
	}
	// The checked-in schema snapshots may still contain retired local-LLM
	// definitions. Replace those entries at catalog assembly time so the
	// host-agent MCP contract advertises only the managed llama-server runtime.
	localNames := make(map[string]struct{}, len(inputs))
	for name := range inputs {
		localNames[name] = struct{}{}
	}
	filtered := make([]ToolDefinition, 0, len(defs)+len(inputs))
	for _, definition := range defs {
		if _, ok := localNames[definition.Name]; ok {
			continue
		}
		filtered = append(filtered, definition)
	}
	for name, schema := range inputs {
		filtered = append(filtered, ToolDefinition{Name: name, Title: name, Description: "Opute-managed llama-server operation", InputSchema: schema, OutputSchema: map[string]any{"type": "object"}})
	}
	return filtered
}

func augmentIncusInventoryTools(defs []ToolDefinition) ([]ToolDefinition, error) {
	seen := make(map[string]bool, len(defs))
	for _, tool := range defs {
		seen[tool.Name] = true
	}
	raw, err := schemas.FS.ReadFile("all-tools.json")
	if err != nil {
		return defs, nil
	}
	var all []ToolDefinition
	if err := json.Unmarshal(raw, &all); err != nil {
		return defs, nil
	}
	want := make(map[string]bool, len(IncusInventoryTools))
	for _, name := range IncusInventoryTools {
		want[name] = true
	}
	for _, tool := range all {
		if want[tool.Name] && !seen[tool.Name] {
			defs = append(defs, tool)
			seen[tool.Name] = true
		}
	}
	return defs, nil
}

func loadCatalogMeta() (catalogMeta, error) {
	raw, err := schemas.FS.ReadFile("catalog-meta.json")
	if err != nil {
		return catalogMeta{}, err
	}
	var meta catalogMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return catalogMeta{}, err
	}
	return meta, nil
}

// IsOmittedToolName reports bridge-backed or platform-unsupported tools omitted from the Go agent.
func IsOmittedToolName(name string) bool {
	if name == "reconcile_postgresql_service" || name == "get_postgresql_service_status" || name == "remove_postgresql_service" || name == "release_postgresql_service_relay" {
		return false
	}
	if IncusOmittedToolNames[name] {
		return true
	}
	for _, prefix := range OmittedToolPrefixes {
		if strings.Contains(name, prefix) {
			return true
		}
	}
	return false
}

// HostToolDefinitionsForProvider returns catalog tools visible to Incus host agents.
func HostToolDefinitionsForProvider(providerID string) ([]ToolDefinition, error) {
	defs, err := LoadAllToolDefinitions(providerID)
	if err != nil {
		return nil, err
	}
	meta, err := loadCatalogMeta()
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool, len(CatalogExcludedToolNames)+len(meta.ExcludedFromCatalog))
	for name := range CatalogExcludedToolNames {
		excluded[name] = true
	}
	for _, name := range meta.ExcludedFromCatalog {
		excluded[name] = true
	}

	filtered := make([]ToolDefinition, 0, len(defs))
	for _, tool := range defs {
		if excluded[tool.Name] || IsOmittedToolName(tool.Name) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered, nil
}

// HostToolNamesForProvider returns tool names for describeHost.supportedTools.
func HostToolNamesForProvider(providerID string) ([]string, error) {
	defs, err := HostToolDefinitionsForProvider(providerID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names, nil
}

// LoadCatalogExcludedDispatchToolDefinitions returns host-internal tools that must be
// registered on the MCP server (tools/call) but omitted from agent-facing tools/list.
func LoadCatalogExcludedDispatchToolDefinitions() ([]ToolDefinition, error) {
	raw, err := schemas.FS.ReadFile("all-tools.json")
	if err != nil {
		return nil, fmt.Errorf("read schema all-tools.json: %w", err)
	}
	var all []ToolDefinition
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("parse schema all-tools.json: %w", err)
	}
	byName := make(map[string]ToolDefinition, len(all))
	for _, tool := range all {
		byName[tool.Name] = tool
	}
	out := make([]ToolDefinition, 0, len(CatalogExcludedToolNames))
	for name := range CatalogExcludedToolNames {
		if IsOmittedToolName(name) {
			continue
		}
		tool, ok := byName[name]
		if !ok {
			continue
		}
		out = append(out, tool)
	}
	return out, nil
}
