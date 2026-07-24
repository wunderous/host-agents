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
	"get_host_info":                       true,
	"check_local_prerequisites":           true,
	"get_local_status":                    true,
	"check_local_llm_prerequisites":       true,
	"list_local_llm_models":               true,
	"probe_local_llm":                     true,
	"install_local_llm_model":             true,
	"start_local_llm_runtime":             true,
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
	"delete_vm":                           true,
	"install_k3s":                         true,
	"get_k3s_status":                      true,
	"uninstall_k3s":                       true,
	"list_namespaces":                     true,
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
	"install_oci_registry":                true,
	"get_oci_registry_status":             true,
	"delete_oci_registry":                 true,
	"configure_k3s_registry":              true,
	"configure_service_domain":            true,
	"remove_service_domain":               true,
	"install_cloudflared_connector":       true,
	"delete_cloudflared_connector":        true,
	"ensure_oci_builder":                  true,
	"ensure_host_tool":                    true,
	"set_host_service_state":              true,
	"create_cloudflare_tunnel":            true,
	"get_cloudflare_tunnel_status":        true,
	"delete_cloudflare_tunnel":            true,
}

var standaloneMutationToolNames = map[string]bool{
	"create_vm":                           true,
	"provision_vm":                        true,
	"start_vm":                            true,
	"stop_vm":                             true,
	"restart_vm":                          true,
	"delete_vm":                           true,
	"install_k3s":                         true,
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
	"ensure_host_tool":                    true,
	"set_host_service_state":              true,
	"create_cloudflare_tunnel":            true,
	"delete_cloudflare_tunnel":            true,
	"cancel_operation":                    true,
	"install_local_llm_model":             true,
	"start_local_llm_runtime":             true,
	"stop_local_llm_runtime":              true,
	"remove_local_llm_model":              true,
	"ensure_local_llm_relay":              true,
	"remove_local_llm_relay":              true,
	"ensure_local_llm_k3s_proxy":          true,
	"remove_local_llm_k3s_proxy":          true,
	"ensure_cloudflared_tunnel":           true,
	"remove_local_llm_cloudflared_tunnel": true,
}

func IsStandaloneMutation(name string) bool {
	return standaloneMutationToolNames[name]
}

func StandaloneToolDefinitions() []ToolDefinition {
	defs := []ToolDefinition{
		{Name: "check_local_prerequisites", Description: "Check local Incus, Kubernetes, PostgreSQL, and Cloudflare prerequisites.", InputSchema: objectSchema(nil, nil)},
		{Name: "get_local_status", Description: "Return local provider and standalone agent status.", InputSchema: objectSchema(nil, nil)},
		{Name: "check_local_llm_prerequisites", Description: "Inspect local Ollama runtime prerequisites.", InputSchema: objectSchema(nil, nil)},
		{Name: "list_local_llm_models", Description: "List local Ollama models.", InputSchema: objectSchema(nil, nil)},
		{Name: "probe_local_llm", Description: "Probe the local Ollama OpenAI-compatible endpoint.", InputSchema: objectSchema(map[string]any{"includeChat": map[string]any{"type": "boolean"}}, nil)},
		{Name: "install_local_llm_model", Description: "Install Ollama and pull an Ollama registry model.", InputSchema: objectSchema(map[string]any{"modelRef": map[string]any{"type": "string"}}, []string{"modelRef"})},
		{Name: "start_local_llm_runtime", Description: "Start the local Ollama runtime.", InputSchema: objectSchema(nil, nil)},
		{Name: "stop_local_llm_runtime", Description: "Stop the local Ollama runtime.", InputSchema: objectSchema(nil, nil)},
		{Name: "remove_local_llm_model", Description: "Remove a local Ollama model.", InputSchema: objectSchema(map[string]any{"modelRef": map[string]any{"type": "string"}}, []string{"modelRef"})},
		{Name: "ensure_local_llm_relay", Description: "Bind an authenticated relay from the Incus bridge to local Ollama.", InputSchema: objectSchema(map[string]any{"sessionId": map[string]any{"type": "string"}, "listenHost": map[string]any{"type": "string"}, "listenPort": map[string]any{"type": "integer"}, "targetHost": map[string]any{"type": "string"}, "targetPort": map[string]any{"type": "integer"}, "allowedSourceIP": map[string]any{"type": "string"}, "relayToken": map[string]any{"type": "string"}}, []string{"sessionId", "listenHost", "listenPort", "targetHost", "targetPort", "allowedSourceIP", "relayToken"})},
		{Name: "remove_local_llm_relay", Description: "Remove a local Ollama relay.", InputSchema: objectSchema(map[string]any{"sessionId": map[string]any{"type": "string"}}, []string{"sessionId"})},
		{Name: "ensure_local_llm_k3s_proxy", Description: "Deploy the authenticated local Ollama proxy on the dedicated gateway VM.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "nodePort": map[string]any{"type": "integer"}, "relayHost": map[string]any{"type": "string"}, "relayPort": map[string]any{"type": "integer"}, "relayToken": map[string]any{"type": "string"}, "bearerKey": map[string]any{"type": "string"}}, []string{"vmName", "nodePort", "relayHost", "relayPort", "relayToken", "bearerKey"})},
		{Name: "remove_local_llm_k3s_proxy", Description: "Remove the local Ollama proxy from the dedicated gateway VM.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "ensure_cloudflared_tunnel", Description: "Run a token-authenticated Cloudflare connector for an allowed origin.", InputSchema: objectSchema(map[string]any{"bindingId": map[string]any{"type": "string"}, "hostname": map[string]any{"type": "string"}, "localTarget": map[string]any{"type": "string"}, "runToken": map[string]any{"type": "string"}, "quick": map[string]any{"type": "boolean"}}, []string{"bindingId", "hostname", "localTarget", "runToken"})},
		{Name: "remove_local_llm_cloudflared_tunnel", Description: "Remove the Cloudflare connector for a local Ollama exposure.", InputSchema: objectSchema(map[string]any{"bindingId": map[string]any{"type": "string"}}, []string{"bindingId"})},
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
		{Name: "apply_manifest", Description: "Apply a generic Kubernetes manifest to a VM-backed cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "manifest": map[string]any{"type": "string"}}, []string{"vmName", "manifest"})},
		{Name: "put_k8s_secret", Description: "Create or replace a generic Kubernetes Secret without exposing its values in tool results.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "data": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}, []string{"vmName", "name", "data"})},
		{Name: "get_k8s_resource", Description: "Fetch a generic Kubernetes resource from a VM-backed cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"}, "resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName", "kind", "resourceName"})},
		{Name: "delete_k8s_resource", Description: "Delete a generic Kubernetes resource from a VM-backed cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"}, "resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName", "kind", "resourceName"})},
		{Name: "get_k8s_resource_status", Description: "Inspect readiness of a generic Kubernetes resource.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "resourceKind": map[string]any{"type": "string"}, "resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName", "resourceKind", "resourceName", "namespace"})},
		{Name: "install_oci_registry", Description: "Install a generic OCI registry inside a VM-backed Kubernetes cluster.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "storageSize": map[string]any{"type": "string"}, "storageClass": map[string]any{"type": "string"}, "nodePort": map[string]any{"type": "integer"}}, []string{"vmName"})},
		{Name: "get_oci_registry_status", Description: "Inspect the generic OCI registry deployment.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "delete_oci_registry", Description: "Delete the generic OCI registry namespace and its data resources.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "configure_k3s_registry", Description: "Configure a K3s cluster to pull images from an OCI registry endpoint.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "endpoint": map[string]any{"type": "string"}, "registry": map[string]any{"type": "string"}, "insecure": map[string]any{"type": "boolean"}}, []string{"vmName", "endpoint"})},
		{Name: "configure_service_domain", Description: "Map a configurable DNS hostname to a Kubernetes Service through Ingress.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "ingressName": map[string]any{"type": "string"}, "hostname": map[string]any{"type": "string"}, "serviceName": map[string]any{"type": "string"}, "servicePort": map[string]any{"type": "integer"}, "ingressClass": map[string]any{"type": "string"}}, []string{"vmName", "namespace", "ingressName", "hostname", "serviceName", "servicePort"})},
		{Name: "remove_service_domain", Description: "Remove a configurable Kubernetes service domain mapping.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "ingressName": map[string]any{"type": "string"}}, []string{"vmName", "namespace", "ingressName"})},
		{Name: "install_cloudflared_connector", Description: "Deploy a token-backed Cloudflare connector inside Kubernetes, with optional generic local-port-to-service mappings.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "token": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "replicas": map[string]any{"type": "integer"}, "localTargets": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"localPort", "target"}, "properties": map[string]any{"localPort": map[string]any{"type": "integer"}, "target": map[string]any{"type": "string"}}}}}, []string{"vmName", "token"})},
		{Name: "delete_cloudflared_connector", Description: "Delete the in-cluster Cloudflare connector namespace and resources.", InputSchema: objectSchema(map[string]any{"vmName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"}}, []string{"vmName"})},
		{Name: "ensure_oci_builder", Description: "Ensure a generic host-side OCI image builder is installed and available.", InputSchema: objectSchema(map[string]any{"builder": map[string]any{"type": "string", "enum": []string{"auto", "podman", "buildah", "buildkit"}}}, nil)},
		{Name: "ensure_host_tool", Description: "Ensure an explicitly allowlisted generic host build/runtime tool is installed and available.", InputSchema: objectSchema(map[string]any{"tool": map[string]any{"type": "string", "enum": []string{"go", "podman", "buildah", "buildkitd", "cloudflared"}}}, []string{"tool"})},
		{Name: "set_host_service_state", Description: "Start, stop, restart, enable, or disable a validated host service.", InputSchema: objectSchema(map[string]any{"serviceName": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.@:-]+$"}, "state": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "enable", "disable"}}, "scope": map[string]any{"type": "string", "enum": []string{"user", "system"}}}, []string{"serviceName", "state"})},
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
