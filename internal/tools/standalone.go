package tools

import (
	"encoding/json"
	"fmt"

	"github.com/wunderous/host-agents/schemas"
)

// StandaloneToolContract is the versioned public boundary for the stdio
// profile. The platform catalog is an implementation input, never the source
// of truth for what a standalone client may see.
type StandaloneToolContract struct {
	SchemaVersion     string                        `json:"schemaVersion"`
	ServerName        string                        `json:"serverName"`
	Provider          string                        `json:"provider"`
	Transport         string                        `json:"transport"`
	SupportedPlatform []string                      `json:"supportedPlatforms"`
	Tools             []StandaloneToolContractEntry `json:"tools"`
}

type StandaloneToolContractEntry struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	Support        string `json:"support"`
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
	if contract.SchemaVersion == "" || contract.ServerName != "host-agent" || contract.Provider != "incus" || contract.Transport != "stdio" {
		return fmt.Errorf("invalid standalone tool contract metadata")
	}
	if len(contract.SupportedPlatform) == 0 {
		return fmt.Errorf("standalone tool contract has no supported platforms")
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
	"get_host_info":                true,
	"check_local_prerequisites":    true,
	"get_local_status":             true,
	"list_operations":              true,
	"get_operation":                true,
	"cancel_operation":             true,
	"list_vms":                     true,
	"get_vm_info":                  true,
	"create_vm":                    true,
	"provision_vm":                 true,
	"start_vm":                     true,
	"stop_vm":                      true,
	"restart_vm":                   true,
	"delete_vm":                    true,
	"install_k3s":                  true,
	"get_k3s_status":               true,
	"uninstall_k3s":                true,
	"list_namespaces":              true,
	"list_pods":                    true,
	"list_services":                true,
	"install_postgresql":           true,
	"get_postgresql_status":        true,
	"delete_postgresql":            true,
	"run_sql":                      true,
	"create_cloudflare_tunnel":     true,
	"get_cloudflare_tunnel_status": true,
	"delete_cloudflare_tunnel":     true,
}

var standaloneMutationToolNames = map[string]bool{
	"create_vm":                true,
	"provision_vm":             true,
	"start_vm":                 true,
	"stop_vm":                  true,
	"restart_vm":               true,
	"delete_vm":                true,
	"install_k3s":              true,
	"uninstall_k3s":            true,
	"install_postgresql":       true,
	"delete_postgresql":        true,
	"run_sql":                  true,
	"create_cloudflare_tunnel": true,
	"delete_cloudflare_tunnel": true,
	"cancel_operation":         true,
}

func IsStandaloneMutation(name string) bool {
	return standaloneMutationToolNames[name]
}

func StandaloneToolDefinitions() []ToolDefinition {
	defs := []ToolDefinition{
		{Name: "check_local_prerequisites", Description: "Check local Incus, Kubernetes, PostgreSQL, and Cloudflare prerequisites.", InputSchema: objectSchema(nil, nil)},
		{Name: "get_local_status", Description: "Return local provider and standalone agent status.", InputSchema: objectSchema(nil, nil)},
		{Name: "list_operations", Description: "List local standalone operations.", InputSchema: objectSchema(map[string]any{"limit": map[string]any{"type": "integer"}}, nil)},
		{Name: "get_operation", Description: "Get a local standalone operation by ID.", InputSchema: objectSchema(map[string]any{"operationId": map[string]any{"type": "string"}}, []string{"operationId"})},
		{Name: "cancel_operation", Description: "Cancel a local standalone operation.", InputSchema: objectSchema(map[string]any{"operationId": map[string]any{"type": "string"}}, []string{"operationId"})},
		{Name: "get_k3s_status", Description: "Inspect K3s service, node readiness, and Kubernetes version.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "install_postgresql", Description: "Install a single-node PostgreSQL deployment in local K3s.", InputSchema: objectSchema(map[string]any{
			"vmName":    map[string]any{"type": "string"},
			"namespace": map[string]any{"type": "string"},
			"database":  map[string]any{"type": "string"},
			"password":  map[string]any{"type": "string"},
		}, []string{"vmName", "password"})},
		{Name: "get_postgresql_status", Description: "Inspect the standalone PostgreSQL deployment.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "delete_postgresql", Description: "Delete the standalone PostgreSQL namespace and resources.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "run_sql", Description: "Run bounded SQL inside the standalone PostgreSQL pod.", InputSchema: objectSchema(map[string]any{
			"vmName":    map[string]any{"type": "string"},
			"sql":       map[string]any{"type": "string"},
			"database":  map[string]any{"type": "string"},
			"namespace": map[string]any{"type": "string"},
		}, []string{"vmName", "sql"})},
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
	return defs
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
