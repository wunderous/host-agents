package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opute-io/host-agents/schemas"
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
	"list_operations":             true,
	"get_operation":               true,
	"list_tasks":                  true,
	"get_task":                    true,
	"agent_shell":                 true,
	"ensure_sql_connector":        true,
	"get_sql_connector_status":    true,
	"release_sql_connector":       true,
	"install_sql_forward_sidecar": true,
	"ensure_cloudflared_tunnel":     true,
	"probe_host_exposure":           true,
	"remove_host_exposure":          true,
	"ensure_host_firewall_rule":     true,
	"configure_host_network":        true,
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
	return augmentIncusInventoryTools(defs)
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
