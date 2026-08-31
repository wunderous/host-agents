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

// ProviderOwnedToolNames are retained in checked-in compatibility exports for
// one migration period, but are not executable Host Agent built-ins. A
// trusted provider must dynamically declare these operations over provider
// MCP before they become available.
var ProviderOwnedToolNames = map[string]bool{
	"ensure_cloudflared_tunnel": true, "remove_local_llm_cloudflared_tunnel": true,
	"probe_host_exposure": true, "remove_host_exposure": true,
	"install_cloudflared_connector": true, "delete_cloudflared_connector": true,
	"get_cloudflare_tunnel_status": true,
}

func filterProviderOwnedDefinitions(defs []ToolDefinition) []ToolDefinition {
	filtered := make([]ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if !ProviderOwnedToolNames[def.Name] {
			filtered = append(filtered, def)
		}
	}
	return filtered
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
	"run_instance_command":        true,
	"ensure_sql_connector":        true,
	"get_sql_connector_status":    true,
	"release_sql_connector":       true,
	"install_sql_forward_sidecar": true,
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
	defs = filterProviderOwnedDefinitions(defs)
	defs = appendLocalLLMDefinitions(defs)
	defs = appendGenericHostDefinitions(defs)
	defs, err = augmentIncusInventoryTools(defs)
	if err != nil {
		return nil, err
	}
	return CanonicalizeToolDefinitions(filterProviderOwnedDefinitions(defs)), nil
}

// CanonicalizeToolDefinitions materializes only bindings explicitly declared
// by a schema or its metadata. It deliberately does not rewrite legacy names,
// inject URI fields, or consult operation names: a resource contract must be
// owned by the definition that declares it. Every wire tool also receives an
// object output schema when an older definition omitted one; MCP clients must
// never receive an explicit null outputSchema.
func CanonicalizeToolDefinitions(defs []ToolDefinition) []ToolDefinition {
	out := make([]ToolDefinition, 0, len(defs))
	for _, original := range defs {
		def := original
		if def.OutputSchema == nil {
			def.OutputSchema = map[string]any{"type": "object"}
		}
		materializeSchemaBindings(&def)
		out = append(out, def)
	}
	return out
}

// resourceTypeKeyword is an explicit schema annotation for fields carrying a
// canonical resource identity. It is deliberately field-owned: no operation
// name, description, or generic `uri` spelling is used to infer a resource.
const resourceTypeKeyword = "x-opute-resource-type"

func schemaResourceType(schema any) (string, bool) {
	property, ok := schema.(map[string]any)
	if !ok {
		return "", false
	}
	resourceType, ok := property[resourceTypeKeyword].(string)
	return strings.TrimSpace(resourceType), ok && strings.TrimSpace(resourceType) != ""
}

func resourceBindings(def ToolDefinition, direction string) []ResourceBinding {
	bindings := explicitResourceBindings(def.Meta, direction)
	var schema map[string]any
	if direction == "requires" {
		schema = def.InputSchema
	} else {
		schema = def.OutputSchema
	}
	for _, candidate := range annotatedResourceBindings(schema, direction) {
		duplicate := false
		for _, existing := range bindings {
			if existing.Argument == candidate.Argument && existing.SourcePath == candidate.SourcePath && existing.ResourceType == candidate.ResourceType {
				duplicate = true
				break
			}
		}
		if !duplicate {
			bindings = append(bindings, candidate)
		}
	}
	return bindings
}

func annotatedResourceBindings(schema map[string]any, direction string) []ResourceBinding {
	if len(schema) == 0 {
		return nil
	}
	bindings := make([]ResourceBinding, 0)
	var walk func(map[string]any, string)
	walk = func(node map[string]any, path string) {
		if resourceType, ok := schemaResourceType(node); ok {
			binding := ResourceBinding{ResourceType: resourceType}
			if direction == "requires" {
				binding.Argument = path
				binding.Required = boolValue(node["x-opute-required"], path == "uri")
			} else {
				binding.SourcePath = path
			}
			bindings = append(bindings, binding)
		}
		if properties, ok := node["properties"].(map[string]any); ok {
			for name, raw := range properties {
				property, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				childPath := name
				if path != "" {
					childPath = path + "." + name
				}
				walk(property, childPath)
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, path+"[]")
		}
	}
	walk(schema, "")
	return bindings
}

func boolValue(value any, fallback bool) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	return fallback
}

func materializeSchemaBindings(def *ToolDefinition) {
	if def == nil {
		return
	}
	meta := make(map[string]any, len(def.Meta)+2)
	for key, value := range def.Meta {
		if key != "argumentProducers" {
			meta[key] = value
		}
	}
	for _, direction := range []string{"requires", "produces"} {
		bindings := resourceBindings(*def, direction)
		if len(bindings) == 0 {
			continue
		}
		items := make([]map[string]any, 0, len(bindings))
		for _, binding := range bindings {
			item := map[string]any{"resourceType": binding.ResourceType}
			if binding.Argument != "" {
				item["argument"] = binding.Argument
			}
			if binding.SourcePath != "" {
				item["sourcePath"] = binding.SourcePath
			}
			if binding.SelectorID != "" {
				item["selectorId"] = binding.SelectorID
			}
			if binding.Required {
				item["required"] = true
			}
			items = append(items, item)
		}
		meta[direction] = items
	}
	if len(meta) == 0 {
		def.Meta = nil
	} else {
		def.Meta = meta
	}
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
		"ensure_sqlite_database":           true,
		"get_sqlite_database_status":       true,
		"remove_sqlite_database":           true,
		"reconcile_serving_assignment":     true,
		"configure_agent_connection":       true,
		"discover_service_ingress":         true,
		"reconcile_postgresql_service":     true,
		"get_postgresql_service_status":    true,
		"remove_postgresql_service":        true,
		"release_postgresql_service_relay": true,
		"install_incus_stack":              true,
		"probe_incus_gpu":                  true,
		"provision_container":              true,
		"run_instance_command":             true,
		"probe_gpu_container":              true,
		"ensure_oci_builder":               true,
		"configure_oci_storage":            true,
		"inspect_container_storage":        true,
		"cleanup_container_storage":        true,
		"build_and_push_oci_image":         true,
		"stage_build_context":              true,
		"ensure_host_tool":                 true,
		"detect_host_platform":             true,
		"run_host_command":                 true,
		"set_host_service_state":           true,
		"inspect_host_service":             true,
		"list_host_services":               true,
		"ensure_host_service_supervisor":   true,
		"ensure_host_file":                 true,
		"remove_host_file":                 true,
		"ensure_host_artifact":             true,
		"extract_host_archive":             true,
		"inspect_host_file":                true,
		"probe_openai_compatible_server":   true,
		"probe_http_endpoint":              true,
		"apply_manifest":                   true,
		"delete_k8s_resource":              true,
		"put_k8s_secret":                   true,
		"install_oci_registry":             true,
		"configure_service_domain":         true,
		"remove_service_domain":            true,
		"reset_incus_stack":                true,
		"validate_host_plan":               true,
		"run_host_plan":                    true,
		"get_host_plan_run":                true,
		"validate_runtime_recipe":          true,
		"run_runtime_recipe":               true,
		"get_runtime_recipe_run":           true,
		"validate_tunnel_recipe":           true,
		"run_tunnel_recipe":                true,
		"get_tunnel_run":                   true,
		"opute.provider.install":           true,
		"opute.provider.validate":          true,
		"opute.provider.status":            true,
		"opute.provider.reload":            true,
		"opute.provider.teardown":          true,
		"get_capability_catalog":           true,
		"open_assistant_session":           true,
		"get_host_capacity":                true,
		"reconcile_host_resource_policy":   true,
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
		Name: "ensure_sqlite_database", Title: "Ensure isolated SQLite database", Description: "Provision an isolated caller-scoped SQLite database file. The caller owns schema and migrations; the host agent owns only the file lifecycle.", InputSchema: map[string]any{"type": "object", "required": []string{"consumerId", "databaseName"}, "properties": map[string]any{"consumerId": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`}, "databaseName": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`}}}, OutputSchema: map[string]any{"type": "object", "required": []string{"provider", "consumerId", "databaseName", "path", "exists"}},
	}, ToolDefinition{
		Name: "get_sqlite_database_status", Title: "Get SQLite database status", Description: "Inspect an isolated caller-scoped SQLite database file without changing its schema or data.", InputSchema: map[string]any{"type": "object", "required": []string{"consumerId", "databaseName"}, "properties": map[string]any{"consumerId": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`}, "databaseName": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`}}}, OutputSchema: map[string]any{"type": "object", "required": []string{"provider", "consumerId", "databaseName", "path", "exists"}},
	}, ToolDefinition{
		Name: "remove_sqlite_database", Title: "Remove isolated SQLite database", Description: "Remove an isolated caller-scoped SQLite database and its sidecars after explicit confirmation.", InputSchema: map[string]any{"type": "object", "required": []string{"consumerId", "databaseName", "confirm"}, "properties": map[string]any{"consumerId": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`}, "databaseName": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`}, "confirm": map[string]any{"type": "boolean"}}}, OutputSchema: map[string]any{"type": "object", "required": []string{"provider", "consumerId", "databaseName", "path", "exists"}},
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
		Name: "configure_agent_connection", Title: "Configure generic agent connection", Description: "Atomically write and remove caller-supplied agent connection environment values, then optionally restart the declared service. Values are redacted from results.", InputSchema: map[string]any{"type": "object", "required": []string{"envFile", "environment"}, "properties": map[string]any{"envFile": map[string]any{"type": "string"}, "environment": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "remove": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "serviceName": map[string]any{"type": "string"}, "restart": map[string]any{"type": "boolean"}, "scope": map[string]any{"type": "string", "enum": []string{"user", "system"}}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "discover_service_ingress", Title: "Discover service ingress", Description: "Resolve caller-declared service ingress endpoints on an explicit Kubernetes target. No product hostnames or ports are inferred.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName", "endpoints"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "endpoints": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object"}}, "ingressNamespace": map[string]any{"type": "string"}, "ingressService": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "install_incus_stack", Title: "Install Incus virtualization stack", Description: "Install or upgrade a pinned Incus feature release from the signed Zabbly repository. QEMU is optional for VM profiles; GPU container profiles do not install it.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"incusPackage": map[string]any{"type": "string"}, "qemuPackage": map[string]any{"type": "string"}, "gpuPackages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "incusChannel": map[string]any{"type": "string", "enum": []string{"stable", "lts-7.0", "lts-6.0"}}, "incusVersion": map[string]any{"type": "string"}, "installQemu": map[string]any{"type": "boolean"}}},
	}, ToolDefinition{
		Name: "probe_incus_gpu", Title: "Probe Incus GPU capability", Description: "Inspect WSL GPU devices/libraries and host virtualization versions; does not claim container GPU inference success.", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "provision_container", Title: "Provision Incus system container", Description: "Launch or reuse a persistent Incus system container with optional GPU, WSL GPU libraries, nesting, and model volume.", InputSchema: map[string]any{"type": "object", "required": []string{"containerName"}, "properties": map[string]any{"containerName": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "disk": map[string]any{"type": "string"}, "gpu": map[string]any{"type": "boolean"}, "wslGpuLibs": map[string]any{"type": "boolean"}, "nesting": map[string]any{"type": "boolean"}, "port": map[string]any{"type": "integer"}, "modelVolume": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"},
	}, ToolDefinition{
		Name: "run_instance_command", Title: "Run typed instance command", Description: "Execute a provider-declared argv on a resolved Incus VM or system container. The target must be a canonical tenant-scoped URI.", InputSchema: map[string]any{"type": "object", "required": []string{"uri", "command"}, "properties": map[string]any{"uri": map[string]any{"type": "string", "pattern": `^(vm|container):[a-z][a-z0-9-]{0,31}:.+$`}, "command": map[string]any{"type": "string", "minLength": 1}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "timeoutMs": map[string]any{"type": "integer", "minimum": 0, "maximum": 7200000}}}, OutputSchema: map[string]any{"type": "object", "required": []string{"uri", "exitCode", "stdout", "stderr"}},
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
		Name: "get_postgresql_service_status", Title: "Get PostgreSQL service status", Description: "Read SQL-gated CloudNativePG PostgreSQL service readiness without returning credentials. When already ready, an optional localRelay is reconciled without enqueueing a second CNPG task.", InputSchema: map[string]any{"type": "object", "required": []string{"vmName"}, "properties": map[string]any{"vmName": map[string]any{"type": "string"}, "clusterName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "instances": map[string]any{"type": "integer"}, "localRelay": postgresqlServiceRelaySchema()}}, OutputSchema: map[string]any{"type": "object"},
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
		Name:         "ensure_host_artifact",
		Title:        "Ensure verified host artifact",
		Description:  "Download a caller-declared HTTPS artifact into the user's home directory and verify its SHA-256 before installation.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"uri", "destination", "sha256"}, "properties": map[string]any{"uri": map[string]any{"type": "string", "format": "uri"}, "destination": map[string]any{"type": "string", "minLength": 1}, "sha256": map[string]any{"type": "string", "pattern": `^(sha256:)?[0-9a-fA-F]{64}$`}, "executable": map[string]any{"type": "boolean"}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"destination", "sha256", "changed"}},
	}, ToolDefinition{
		Name:         "run_host_command",
		Title:        "Run bounded host command",
		Description:  "Run a caller-declared bounded host command for a generic serving or infrastructure assignment.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{"command": map[string]any{"type": "string", "minLength": 1}, "timeoutMs": map[string]any{"type": "integer", "minimum": 0, "maximum": 7200000}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"exitCode", "stdout", "stderr"}},
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
		Description: "Start, stop, restart, enable, or disable a validated host service selected by canonical tenant-scoped URI; user scope is the default.",
		InputSchema: map[string]any{"type": "object", "required": []string{"uri", "state"}, "properties": map[string]any{
			"uri":   map[string]any{"type": "string", "minLength": 1, "description": "Canonical Host Agent resource URI returned by discovery.", resourceTypeKeyword: "host-service"},
			"state": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "enable", "disable"}},
			"scope": map[string]any{"type": "string", "enum": []string{"user", "system"}},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"serviceName", "state", "scope", "status"}},
	}, ToolDefinition{
		Name:         "inspect_host_service",
		Title:        "Inspect host service",
		Description:  "Read systemd service state for a caller-declared host service without changing it.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"serviceName"}, "properties": map[string]any{"serviceName": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_.@:-]+$`}, "scope": map[string]any{"type": "string", "enum": []string{"user", "system"}}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"serviceName", "scope", "status", "active", "enabled", "exitCode"}},
	}, ToolDefinition{
		Name:        "ensure_host_service_supervisor",
		Title:       "Ensure host service supervisor",
		Description: "Ensure the caller-declared host service supervisor is available and persistent; user-scoped services receive an idempotent persistent user-manager contract.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"scope": map[string]any{"type": "string", "enum": []string{"user", "system"}},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"scope", "status", "persistent"}},
	}, ToolDefinition{
		Name:        "probe_openai_compatible_server",
		Title:       "Probe OpenAI-compatible server",
		Description: "Probe a caller-declared OpenAI-compatible runtime for model discovery and optional streaming chat readiness.",
		InputSchema: map[string]any{"type": "object", "required": []string{"endpoint"}, "properties": map[string]any{
			"endpoint":    map[string]any{"type": "string", "format": "uri"},
			"modelRef":    map[string]any{"type": "string"},
			"includeChat": map[string]any{"type": "boolean"},
			"bearerToken": map[string]any{"type": "string", "writeOnly": true},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"servingContract", "apiBaseUrl", "models", "ready"}},
	}, ToolDefinition{
		Name:        "probe_http_endpoint",
		Title:       "Probe HTTP endpoint",
		Description: "Probe a caller-declared HTTP(S) endpoint for provider-neutral reachability evidence.",
		InputSchema: map[string]any{"type": "object", "required": []string{"endpoint"}, "properties": map[string]any{
			"endpoint": map[string]any{"type": "string", "format": "uri"},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"endpoint", "ready"}},
	}, ToolDefinition{
		Name:        "ensure_host_file",
		Title:       "Ensure managed host file",
		Description: "Atomically reconcile a caller-declared file beneath the current user's home directory, returning only path, mode, change, and content hash evidence.",
		InputSchema: map[string]any{"type": "object", "required": []string{"path", "content"}, "properties": map[string]any{
			"path": map[string]any{"type": "string", "minLength": 1}, "content": map[string]any{"type": "string"}, "mode": map[string]any{"type": "integer", "minimum": 384, "maximum": 493},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"path", "changed", "contentSha256", "mode"}},
	}, ToolDefinition{
		Name:        "remove_host_file",
		Title:       "Remove managed host file",
		Description: "Remove one caller-owned regular file beneath the current user's home directory after explicit confirmation and an optional content hash check.",
		InputSchema: map[string]any{"type": "object", "required": []string{"path", "confirm"}, "properties": map[string]any{
			"path": map[string]any{"type": "string", "minLength": 1}, "expectedSha256": map[string]any{"type": "string"}, "confirm": map[string]any{"type": "boolean"},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"path", "exists", "removed"}},
	}, ToolDefinition{
		Name:         "extract_host_archive",
		Title:        "Extract verified host archive",
		Description:  "Extract a verified caller-declared host archive beneath the user's home directory after rejecting traversal entries.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"archivePath", "destination"}, "properties": map[string]any{"archivePath": map[string]any{"type": "string", "minLength": 1}, "destination": map[string]any{"type": "string", "minLength": 1}, "format": map[string]any{"type": "string", "enum": []string{"tar.zst"}}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"archivePath", "destination", "entries", "changed"}},
	}, ToolDefinition{
		Name:        "inspect_host_file",
		Title:       "Inspect managed host file",
		Description: "Inspect a caller-declared file beneath the current user's home directory without returning its content.",
		InputSchema: map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{
			"path": map[string]any{"type": "string", "minLength": 1}, "expectedSha256": map[string]any{"type": "string"}, "expectedContent": map[string]any{"type": "string", "writeOnly": true},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"path", "exists", "regular", "executable", "matches"}},
	}, ToolDefinition{
		Name:        "ensure_host_tool",
		Title:       "Ensure generic host tool",
		Description: "Ensure an explicitly allowlisted generic host build/runtime tool is installed and available.",
		InputSchema: map[string]any{"type": "object", "required": []string{"tool"}, "properties": map[string]any{
			"tool": map[string]any{"type": "string", "enum": []string{"bun", "gcc", "g++", "go", "podman", "buildah", "buildkitd", "cloudflared", "helm", "cmake", "ninja", "nvcc"}},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"tool", "path", "available"}},
	}, ToolDefinition{
		Name:        "detect_host_platform",
		Title:       "Detect host platform",
		Description: "Detect the operating system and CPU identity of the host running this agent, distinguishing native Windows, macOS, WSL1/WSL2, and native Linux, and reporting the CPU architecture and family including Apple M-series silicon.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "os", "kind", "cpu"}, "properties": map[string]any{
			"contractVersion":     map[string]any{"type": "string", "const": "host-platform.v1"},
			"os":                  map[string]any{"type": "string", "enum": []string{"linux", "macos", "windows"}},
			"kind":                map[string]any{"type": "string", "enum": []string{"linux", "macos", "windows-native", "wsl1", "wsl2"}},
			"kernel":              map[string]any{"type": "string"},
			"kernelVersion":       map[string]any{"type": "string"},
			"distribution":        map[string]any{"type": "string"},
			"distributionVersion": map[string]any{"type": "string"},
			"wsl":                 map[string]any{"type": "object", "required": []string{"version", "interop"}, "properties": map[string]any{"version": map[string]any{"type": "integer"}, "distro": map[string]any{"type": "string"}, "interop": map[string]any{"type": "boolean"}}},
			"cpu": map[string]any{"type": "object", "required": []string{"architecture", "family"}, "properties": map[string]any{
				"architecture": map[string]any{"type": "string"},
				"family":       map[string]any{"type": "string", "enum": []string{"x86-64", "x86", "arm64", "arm", "apple-silicon", "unknown"}},
				"vendor":       map[string]any{"type": "string"},
				"model":        map[string]any{"type": "string"},
				"series":       map[string]any{"type": "string"},
				"variant":      map[string]any{"type": "string"},
				"logicalCores": map[string]any{"type": "integer"},
			}},
			"evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}},
	}, ToolDefinition{
		Name:         "terminate_wsl_distribution",
		Title:        "Terminate WSL distribution",
		Description:  "Terminate exactly one named WSL distribution through the tested Windows interop capability. Requires host approval.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"distro"}, "properties": map[string]any{"distro": map[string]any{"type": "string", "minLength": 1}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"distro", "terminated"}},
	}, ToolDefinition{
		Name:         "shutdown_wsl",
		Title:        "Shutdown WSL environment",
		Description:  "Shutdown the complete WSL2 environment through the tested Windows lifecycle capability. Requires explicit destructive approval.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"shutdown"}},
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
			"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "data": map[string]any{"type": "object", "writeOnly": true, "additionalProperties": map[string]any{"type": "string"}},
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
	}, ToolDefinition{
		Name:        "opute.provider.install",
		Title:       "Install provider",
		Description: "Connect to a trusted provider MCP module, validate its neutral install manifest, execute its selected declarative recipe, and optionally activate the resulting provider generation.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"source": map[string]any{"type": "string", "minLength": 1}, "descriptor": map[string]any{"type": "object"}, "endpoint": map[string]any{"type": "string", "format": "uri"}, "token": map[string]any{"type": "string", "writeOnly": true}, "mode": map[string]any{"type": "string"}, "recipeSource": map[string]any{"type": "string"}, "revision": map[string]any{"type": "string"}, "sha256": map[string]any{"type": "string"}, "inputs": map[string]any{"type": "object"}, "activate": map[string]any{"type": "boolean"}, "resume": map[string]any{"type": "boolean"},
		}},
		OutputSchema: map[string]any{"type": "object"},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "heavy", "cpuCores": 2, "memoryBytes": 2147483648, "tasks": 8}},
	}, ToolDefinition{
		Name:         "opute.provider.validate",
		Title:        "Validate provider",
		Description:  "Run a provider-declared capability validation operation through the generic provider MCP contract.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"provider"}, "properties": map[string]any{"provider": map[string]any{"type": "string", "minLength": 1}, "operation": map[string]any{"type": "string"}, "inputs": map[string]any{"type": "object"}}},
		OutputSchema: map[string]any{"type": "object"},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "opute.provider.status",
		Title:        "Get provider status",
		Description:  "Read the connected provider and active provider-generation state without changing host state.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"provider"}, "properties": map[string]any{"provider": map[string]any{"type": "string", "minLength": 1}}},
		OutputSchema: map[string]any{"type": "object"},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "opute.provider.reload",
		Title:        "Reload provider",
		Description:  "Load a new trusted provider module generation and reconcile its selected recipe before activation.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"source": map[string]any{"type": "string", "minLength": 1}, "descriptor": map[string]any{"type": "object"}, "endpoint": map[string]any{"type": "string", "format": "uri"}, "token": map[string]any{"type": "string", "writeOnly": true}, "mode": map[string]any{"type": "string"}, "recipeSource": map[string]any{"type": "string"}, "revision": map[string]any{"type": "string"}, "sha256": map[string]any{"type": "string"}, "inputs": map[string]any{"type": "object"}, "activate": map[string]any{"type": "boolean"}}},
		OutputSchema: map[string]any{"type": "object"},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "heavy", "cpuCores": 2, "memoryBytes": 2147483648, "tasks": 8}},
	}, ToolDefinition{
		Name:         "opute.provider.teardown",
		Title:        "Teardown provider",
		Description:  "Ask the connected provider for a generic teardown host plan, validate it, execute it durably, and retire the provider only after the plan succeeds.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"provider", "confirm"}, "properties": map[string]any{"provider": map[string]any{"type": "string", "minLength": 1}, "generation": map[string]any{"type": "string", "minLength": 1}, "inputs": map[string]any{"type": "object"}, "confirm": map[string]any{"type": "boolean"}, "resume": map[string]any{"type": "boolean"}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"runId", "status", "catalogRevision"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "heavy", "cpuCores": 2, "memoryBytes": 2147483648, "tasks": 8}},
	}, ToolDefinition{
		Name:         "validate_host_plan",
		Title:        "Validate host plan",
		Description:  "Validate a generic host-plan.v1 document against the current authorized capability catalog without changing host state.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"plan"}, "properties": map[string]any{"plan": map[string]any{}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"valid", "contractVersion", "catalogRevision"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "run_host_plan",
		Title:        "Run host plan",
		Description:  "Execute an explicit generic host-plan.v1 document with durable idempotency, readiness validation, recovery, and resume state.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"plan"}, "properties": map[string]any{"plan": map[string]any{}, "resume": map[string]any{"type": "boolean"}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"runId", "status", "catalogRevision"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "heavy", "cpuCores": 2, "memoryBytes": 2147483648, "tasks": 8}},
	}, ToolDefinition{
		Name:         "get_host_plan_run",
		Title:        "Get host plan run",
		Description:  "Read the durable status, node results, and revision metadata for a host-plan.v1 run.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"runId"}, "properties": map[string]any{"runId": map[string]any{"type": "string", "minLength": 1}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"runId", "status", "nodes"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:        "validate_runtime_recipe",
		Title:       "Validate runtime recipe",
		Description: "Resolve and validate an external runtime-recipe.v1 source and its embedded host-plan.v1 without changing host state.",
		InputSchema: map[string]any{"type": "object", "required": []string{"source"}, "properties": map[string]any{
			"source": map[string]any{"type": "string", "minLength": 1}, "revision": map[string]any{"type": "string"}, "sha256": map[string]any{"type": "string"},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"valid", "recipeId", "recipeVersion", "recipeHash", "rawSha256", "plan"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:        "run_runtime_recipe",
		Title:       "Run runtime recipe",
		Description: "Execute an externally sourced runtime-recipe.v1 through the existing durable host-plan runner; with activate=true, validate its neutral serving contract and commit it as the active runtime only after the run succeeds.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"source": map[string]any{"type": "string", "minLength": 1}, "revision": map[string]any{"type": "string"}, "sha256": map[string]any{"type": "string"},
			"inputs": map[string]any{"type": "object"}, "resume": map[string]any{"type": "boolean"}, "runId": map[string]any{"type": "string", "minLength": 1}, "activate": map[string]any{"type": "boolean"},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"runId", "status", "catalogRevision"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "heavy", "cpuCores": 2, "memoryBytes": 2147483648, "tasks": 8}},
	}, ToolDefinition{
		Name:         "get_runtime_recipe_run",
		Title:        "Get runtime recipe run",
		Description:  "Read the durable status, expanded plan evidence, and recipe provenance for a runtime-recipe.v1 run.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"runId"}, "properties": map[string]any{"runId": map[string]any{"type": "string", "minLength": 1}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"runId", "status", "nodes", "recipe"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "validate_tunnel_recipe",
		Title:        "Validate tunnel recipe",
		Description:  "Resolve and validate an external tunnel-recipe.v1 source and its embedded host-plan.v1 without changing host state.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"source"}, "properties": map[string]any{"source": map[string]any{"type": "string", "minLength": 1}, "revision": map[string]any{"type": "string"}, "sha256": map[string]any{"type": "string"}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"valid", "recipeId", "recipeVersion", "recipeHash", "rawSha256", "plan"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "run_tunnel_recipe",
		Title:        "Run tunnel recipe",
		Description:  "Execute an external tunnel-recipe.v1 through the existing durable host-plan runner; activation validates generic HTTP exposure before replacing the active tunnel capability.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"source": map[string]any{"type": "string", "minLength": 1}, "revision": map[string]any{"type": "string"}, "sha256": map[string]any{"type": "string"}, "inputs": map[string]any{"type": "object"}, "resume": map[string]any{"type": "boolean"}, "runId": map[string]any{"type": "string", "minLength": 1}, "activate": map[string]any{"type": "boolean"}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"runId", "status", "catalogRevision"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "heavy", "cpuCores": 2, "memoryBytes": 2147483648, "tasks": 8}},
	}, ToolDefinition{
		Name:         "get_tunnel_run",
		Title:        "Get tunnel run",
		Description:  "Read durable tunnel-recipe.v1 provenance and host-plan state.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"runId"}, "properties": map[string]any{"runId": map[string]any{"type": "string", "minLength": 1}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"runId", "status", "nodes", "recipe"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "get_capability_catalog",
		Title:        "Get capability catalog",
		Description:  "Return the immutable authorized capability snapshot and revision used for typed proposals and host plans.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object", "required": []string{"providerId", "catalogRevision", "tools"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "open_assistant_session",
		Title:        "Open assistant session",
		Description:  "Negotiate the bounded assistant-session.v1 contract and bind the client to the current capability revision.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"sessionId", "supportedContractVersions"}, "properties": map[string]any{"sessionId": map[string]any{"type": "string", "minLength": 1}, "tenantId": map[string]any{"type": "string", "minLength": 1}, "supportedContractVersions": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}, "catalogRevision": map[string]any{"type": "string"}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "sessionId", "catalogRevision", "tenantId"}},
		Meta:         map[string]any{"resourceCost": map[string]any{"class": "control"}},
	}, ToolDefinition{
		Name:         "get_host_capacity",
		Title:        "Get host capacity",
		Description:  "Read provider-neutral host capacity, reservations, pressure, and resource-enforcement state.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"policyRevision", "enforcement", "effectiveLimits", "currentUsage", "reservations", "queue"}},
		Meta:         map[string]any{"effect": "read"},
	}, ToolDefinition{
		Name:        "reconcile_host_resource_policy",
		Title:       "Reconcile host resource policy",
		Description: "Reconcile an already-authorized versioned host resource policy against one exact host-service URI; raw systemd, cgroup, WSL, and shell controls are not accepted.",
		InputSchema: map[string]any{"type": "object", "required": []string{"policyRevision", "uri"}, "properties": map[string]any{
			"policyRevision": map[string]any{"type": "string", "minLength": 1},
			"uri":            map[string]any{"type": "string", "minLength": 1, resourceTypeKeyword: "host-service"},
			"scope":          map[string]any{"type": "string", "enum": []string{"user", "system"}},
		}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"reconciled", "policyRevision", "enforcement", "capacity"}},
		Meta: map[string]any{
			"effect":        "mutation",
			"needsApproval": true,
			// Reconciliation is the bounded control-plane recovery path. A zero
			// cost control request is intentionally admitted while workload
			// enforcement is unknown so it can repair that boundary.
			"resourceCost": map[string]any{"class": "control"},
		},
	}, ToolDefinition{
		Name:        "list_host_services",
		Title:       "List Host Services",
		Description: "List systemd services on the host and return their canonical tenant-scoped resource URIs.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"user", "system"},
					"description": "Systemd unit scope: user (default) or system",
				},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []string{"services", "total"},
			"properties": map[string]any{
				"services": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"uri", "serviceName", "scope", "status", "active", "enabled"},
						"properties": map[string]any{
							"uri": map[string]any{
								"type":              "string",
								resourceTypeKeyword: "host-service",
								"description":       "Canonical Host Agent resource URI for host service.",
							},
							"serviceName": map[string]any{"type": "string"},
							"scope":       map[string]any{"type": "string"},
							"status":      map[string]any{"type": "string"},
							"active":      map[string]any{"type": "boolean"},
							"enabled":     map[string]any{"type": "boolean"},
						},
					},
				},
				"total": map[string]any{"type": "integer"},
			},
		},
		Meta: map[string]any{
			"effect": "read",
			"produces": []map[string]any{
				{
					"resourceType": "host-service",
					"sourcePath":   "services[].uri",
				},
			},
		},
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
	runtimeEnum := []string{"ollama", "llama-cpp"}
	llamaRuntimeEnum := []string{"llama-cpp"}
	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		seen[d.Name] = true
	}
	inputs := map[string]map[string]any{
		"check_local_llm_prerequisites": {"type": "object", "properties": map[string]any{}},
		"ensure_local_llm_server_binary": {"type": "object", "properties": map[string]any{
			"runtime":           map[string]any{"type": "string", "enum": llamaRuntimeEnum},
			"sourceUri":         map[string]any{"type": "string", "format": "uri"},
			"sourceSha256":      map[string]any{"type": "string"},
			"sourceRevision":    map[string]any{"type": "string"},
			"outputPath":        map[string]any{"type": "string"},
			"cudaArchitectures": map[string]any{"type": "string"},
		}},
		"list_local_llm_models": {"type": "object", "properties": map[string]any{"runtime": map[string]any{"type": "string", "enum": runtimeEnum}, "includeChat": map[string]any{"type": "boolean"}}},
		"probe_local_llm": {"type": "object", "properties": map[string]any{
			"runtime":     map[string]any{"type": "string", "enum": runtimeEnum},
			"includeChat": map[string]any{"type": "boolean"},
			"modelRef":    map[string]any{"type": "string"},
			"model":       map[string]any{"type": "string", "description": "Generic alias for modelRef."},
			"role":        map[string]any{"type": "string", "enum": []string{"language", "embedding"}},
			"modelPreset": map[string]any{"type": "string", "enum": []string{"lfm2-2.6b", "lfm2.5-thinking", "qwen3.5", "qwen3.5-0.8b"}},
			"numGpu":      map[string]any{"type": "integer"},
			"numCtx":      map[string]any{"type": "integer"},
		}},
		"install_local_llm_model": {"type": "object", "properties": map[string]any{
			"runtime":                 map[string]any{"type": "string", "enum": runtimeEnum},
			"modelFamily":             map[string]any{"type": "string"},
			"modelVariant":            map[string]any{"type": "string"},
			"installSource":           map[string]any{"type": "string"},
			"modelRef":                map[string]any{"type": "string"},
			"model":                   map[string]any{"type": "string", "description": "Generic alias for modelRef."},
			"role":                    map[string]any{"type": "string", "enum": []string{"language", "embedding"}, "description": "Generic model role. Embedding models are not made the chat default unless setDefault is explicit."},
			"setDefault":              map[string]any{"type": "boolean", "description": "For Ollama, keep this model as the default chat/runtime model; set false for an embedding-only resident model."},
			"modelPreset":             map[string]any{"type": "string", "enum": []string{"lfm2-2.6b", "lfm2.5-thinking", "qwen3.5", "qwen3.5-0.8b"}},
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
			"runtime":     map[string]any{"type": "string", "enum": runtimeEnum},
			"modelRef":    map[string]any{"type": "string", "description": "Arbitrary Ollama model reference."},
			"contextSize": map[string]any{"type": "integer", "description": "Persistent context size in tokens. Omit to read the current value."},
			"numCtx":      map[string]any{"type": "integer", "description": "Compatibility alias for contextSize."},
		}},
		"start_local_llm_runtime": {"type": "object", "properties": map[string]any{"runtime": map[string]any{"type": "string", "enum": runtimeEnum}, "runtimeId": map[string]any{"type": "string"}}},
		"configure_local_llm_runtime": {"type": "object", "properties": map[string]any{
			"gpuOverheadMiB":  map[string]any{"type": "integer"},
			"maxLoadedModels": map[string]any{"type": "integer"},
			"numParallel":     map[string]any{"type": "integer"},
			"flashAttention":  map[string]any{"type": "boolean"},
		}},
		"stop_local_llm_runtime": {"type": "object", "properties": map[string]any{"runtime": map[string]any{"type": "string", "enum": runtimeEnum}, "runtimeId": map[string]any{"type": "string"}}},
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
	}
	for name, schema := range inputs {
		if !seen[name] {
			desc := "LEGACY compatibility wrapper for a local runtime operation"
			switch name {
			case "check_local_llm_prerequisites":
				desc = "Inspect shared Ollama readiness and alternate llama-cpp GPU/CUDA diagnostics."
			case "ensure_local_llm_server_binary":
				desc = "Build and verify a pinned CUDA llama-server binary from source on the host."
			case "install_local_llm_model":
				desc = "Install or adopt a model in the shared Ollama runtime, or explicitly select the alternate llama-cpp runtime."
			case "configure_local_llm_model":
				desc = "Read or persist an arbitrary Ollama model's context size in the shared host runtime; the effective managed model reference is returned."
			case "start_local_llm_runtime":
				desc = "Start the one host-wide Ollama service or the explicitly selected llama-cpp service."
			case "configure_local_llm_runtime":
				desc = "Inspect the host-wide runtime policy: two resident Ollama models and one serialized request."
			case "probe_local_llm":
				desc = "Probe local LLM readiness, loaded model identity, and runtime residency."
			case "stop_local_llm_runtime":
				desc = "Stop the alternate llama-cpp runtime; shared Ollama remains running for other Platform instances."
			case "remove_local_llm_model":
				desc = "Remove an alternate llama-cpp adoption record; shared Ollama artifacts are retained."
			}
			output := map[string]any{"type": "object"}
			if name == "list_local_llm_models" || name == "probe_local_llm" {
				output = map[string]any{
					"type": "object",
					"properties": map[string]any{
						"models": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"uri":  map[string]any{"type": "string"},
									"name": map[string]any{"type": "string"},
								},
								"required": []string{"uri", "name"},
							},
						},
					},
				}
			}
			defs = append(defs, ToolDefinition{Name: name, Title: name, Description: desc, InputSchema: schema, OutputSchema: output})
		}
	}
	// The checked-in schema snapshots may still contain retired local-LLM
	// definitions. Replace those entries at catalog assembly time so the
	// host-agent MCP contract advertises the shared Ollama default plus the
	// explicitly selectable llama-cpp alternate.
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
		filtered = append(filtered, ToolDefinition{Name: name, Title: name, Description: "LEGACY compatibility wrapper for a local runtime operation; prefer runtime-recipe.v1", InputSchema: schema, OutputSchema: map[string]any{"type": "object"}})
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
	return CanonicalizeToolDefinitions(out), nil
}
