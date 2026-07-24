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
	"list_operations":                 true,
	"get_operation":                   true,
	"list_tasks":                      true,
	"get_task":                        true,
	"agent_shell":                     true,
	"ensure_sql_connector":            true,
	"get_sql_connector_status":        true,
	"release_sql_connector":           true,
	"install_sql_forward_sidecar":     true,
	"ensure_cloudflared_tunnel":       true,
	"ensure_platform_opute_stack":     true,
	"provision_platform_opute_tunnel": true,
	"probe_host_exposure":             true,
	"remove_host_exposure":            true,
	"ensure_host_firewall_rule":       true,
	"configure_host_network":          true,
}

// IncusOmittedToolNames are not supported on the Incus-only Linux host agent.
var IncusOmittedToolNames = map[string]bool{
	"ensure_k3d":                     true,
	"switch_infrastructure_provider": true,
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

func appendGenericHostDefinitions(defs []ToolDefinition) []ToolDefinition {
	needed := map[string]bool{
		"ensure_oci_builder":            true,
		"ensure_host_tool":              true,
		"set_host_service_state":        true,
		"apply_manifest":                true,
		"delete_k8s_resource":            true,
		"put_k8s_secret":                true,
		"install_oci_registry":          true,
		"configure_k3s_registry":        true,
		"install_cloudflared_connector": true,
		"delete_cloudflared_connector":  true,
		"configure_service_domain":      true,
		"remove_service_domain":         true,
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
		Name:         "ensure_oci_builder",
		Title:        "Ensure OCI image builder",
		Description:  "Ensure a generic host-side OCI image builder is installed and available.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"builder": map[string]any{"type": "string", "enum": []string{"auto", "podman", "buildah", "buildkit"}}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"builder", "path", "available"}},
	}, ToolDefinition{
		Name:         "ensure_host_tool",
		Title:        "Ensure generic host tool",
		Description:  "Ensure an explicitly allowlisted generic host build/runtime tool is installed and available.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"tool"}, "properties": map[string]any{"tool": map[string]any{"type": "string", "enum": []string{"go", "podman", "buildah", "buildkitd", "cloudflared"}}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"tool", "path", "available"}},
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
		Name:        "ensure_host_tool",
		Title:       "Ensure generic host tool",
		Description: "Ensure an explicitly allowlisted generic host build/runtime tool is installed and available.",
		InputSchema: map[string]any{"type": "object", "required": []string{"tool"}, "properties": map[string]any{
			"tool": map[string]any{"type": "string", "enum": []string{"go", "podman", "buildah", "buildkitd", "cloudflared"}},
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
		"check_local_llm_prerequisites":       {"type": "object", "properties": map[string]any{}},
		"list_local_llm_models":               {"type": "object", "properties": map[string]any{"includeChat": map[string]any{"type": "boolean"}}},
		"probe_local_llm":                     {"type": "object", "properties": map[string]any{"includeChat": map[string]any{"type": "boolean"}}},
		"install_local_llm_model":             {"type": "object", "required": []string{"modelRef"}, "properties": map[string]any{"modelRef": map[string]any{"type": "string"}}},
		"start_local_llm_runtime":             {"type": "object", "properties": map[string]any{}},
		"stop_local_llm_runtime":              {"type": "object", "properties": map[string]any{}},
		"remove_local_llm_model":              {"type": "object", "required": []string{"modelRef"}, "properties": map[string]any{"modelRef": map[string]any{"type": "string"}, "purge": map[string]any{"type": "boolean"}}},
		"ensure_local_llm_relay":              {"type": "object", "required": []string{"sessionId", "listenHost", "listenPort", "targetHost", "targetPort", "incomingToken", "allowedSourceCIDRs"}, "properties": map[string]any{"upstreamToken": map[string]any{"type": "string"}, "allowedSourceCIDRs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}},
		"remove_local_llm_relay":              {"type": "object", "required": []string{"sessionId"}, "properties": map[string]any{}},
		"ensure_local_llm_k3s_proxy":          {"type": "object", "required": []string{"vmName", "nodePort", "relayHost", "relayPort", "relayToken", "bearerKey"}, "properties": map[string]any{}},
		"remove_local_llm_k3s_proxy":          {"type": "object", "required": []string{"vmName"}, "properties": map[string]any{}},
		"remove_local_llm_cloudflared_tunnel": {"type": "object", "required": []string{"bindingId"}, "properties": map[string]any{}},
	}
	for name, schema := range inputs {
		if !seen[name] {
			defs = append(defs, ToolDefinition{Name: name, Title: name, Description: "Opute-managed local Ollama operation", InputSchema: schema, OutputSchema: map[string]any{"type": "object"}})
		}
	}
	return defs
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
