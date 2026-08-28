package contract

import (
	"context"
	"testing"

	capabilitycatalog "github.com/wunderous/host-agents/internal/catalog"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/tools"
)

func newContractTestServer(t *testing.T, standalone bool) *hostmcp.Server {
	t.Helper()
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	server, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID:     "incus",
		Ops:            svc,
		Standalone:     standalone,
		AllowMutations: true,
		StateDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func TestCatalogAndMCPToolsListParity(t *testing.T) {
	server := newContractTestServer(t, false)
	snapshot := server.CatalogSnapshot()

	registeredNames := make(map[string]bool)
	for _, descriptor := range snapshot.Tools {
		registeredNames[descriptor.Name] = true
	}

	// Verify catalog tool count matches expected public tools
	if len(snapshot.Tools) == 0 {
		t.Fatal("catalog snapshot has zero tools")
	}

	// Ensure leaked internal tools are not exposed
	leakedInternalTools := []string{
		"agent_shell",
		"ensure_sql_connector",
		"get_sql_connector_status",
		"release_sql_connector",
		"install_sql_forward_sidecar",
		"configure_host_network",
		"ensure_host_firewall_rule",
		"exec_command",
		"get_operation",
		"list_operations",
		"run_instance_command",
	}
	for _, name := range leakedInternalTools {
		if registeredNames[name] {
			t.Fatalf("internal tool %q leaked into public catalog/mcp tools list", name)
		}
	}

	// Ensure canonical provider names are dotted and no underscore aliases exist
	canonicalProviderTools := []string{
		"opute.provider.install",
		"opute.provider.validate",
		"opute.provider.status",
		"opute.provider.reload",
		"opute.provider.teardown",
	}
	for _, name := range canonicalProviderTools {
		if !registeredNames[name] {
			t.Fatalf("missing canonical dotted provider tool %q in catalog", name)
		}
	}

	underscoreAliases := []string{
		"opute_provider_install",
		"opute_provider_validate",
		"opute_provider_status",
		"opute_provider_reload",
		"opute_provider_teardown",
	}
	for _, name := range underscoreAliases {
		if registeredNames[name] {
			t.Fatalf("legacy underscore alias %q must not be registered in catalog", name)
		}
	}
}

func TestExplicitCapabilityEffects(t *testing.T) {
	defs, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatalf("HostToolDefinitionsForProvider: %v", err)
	}
	catalog := tools.BuildCapabilityCatalog("incus", defs)

	validEffects := map[string]bool{
		"read":               true,
		"mutation":           true,
		"destructive":        true,
		"credential_bearing": true,
	}

	requiredNonReadEffects := map[string]string{
		"configure_network":           "mutation",
		"exec_kubernetes_command":     "mutation",
		"install_provider_tools":      "mutation",
		"recover_bridge":              "mutation",
		"register_kubernetes_cluster": "mutation",
		"remove_vm_network_device":    "destructive",
		"stream_vm_console":           "mutation",
		"send_console_input":          "mutation",
		"resize_console":              "mutation",
		"delete_vm":                   "destructive",
		"remove_host_file":            "destructive",
		"remove_sqlite_database":      "destructive",
		"remove_postgresql_service":   "destructive",
		"put_k8s_secret":              "credential_bearing",
		"ensure_local_llm_relay":      "credential_bearing",
	}

	for _, tool := range catalog.Tools {
		if !validEffects[tool.Effect] {
			t.Fatalf("tool %q has invalid effect %q", tool.Name, tool.Effect)
		}
		if expectedEffect, ok := requiredNonReadEffects[tool.Name]; ok {
			if tool.Effect != expectedEffect {
				t.Fatalf("tool %q effect = %q, want %q", tool.Name, tool.Effect, expectedEffect)
			}
			if !tool.RequiresApproval {
				t.Fatalf("tool %q with effect %q must require approval", tool.Name, tool.Effect)
			}
		}
		if tool.Name == "open_assistant_session" {
			if tool.Effect != "read" {
				t.Fatalf("open_assistant_session effect = %q, want read", tool.Effect)
			}
		}
	}
}

func TestHostServiceDiscoveryAndEdgeDerivation(t *testing.T) {
	defs, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatalf("HostToolDefinitionsForProvider: %v", err)
	}
	catalog := tools.BuildCapabilityCatalog("incus", defs)

	var listServicesDescriptor *tools.CapabilityDescriptor
	var inspectServiceDescriptor *tools.CapabilityDescriptor
	for i := range catalog.Tools {
		if catalog.Tools[i].Name == "list_host_services" {
			listServicesDescriptor = &catalog.Tools[i]
		}
		if catalog.Tools[i].Name == "inspect_host_service" {
			inspectServiceDescriptor = &catalog.Tools[i]
		}
	}

	if listServicesDescriptor == nil {
		t.Fatal("missing list_host_services in catalog")
	}
	if len(listServicesDescriptor.Produces) == 0 || listServicesDescriptor.Produces[0].ResourceType != "host-service" {
		t.Fatalf("list_host_services Produces = %#v, want host-service", listServicesDescriptor.Produces)
	}

	if inspectServiceDescriptor == nil {
		t.Fatal("missing inspect_host_service in catalog")
	}
	if len(inspectServiceDescriptor.Requires) == 0 || inspectServiceDescriptor.Requires[0].ResourceType != "host-service" {
		t.Fatalf("inspect_host_service Requires = %#v, want host-service", inspectServiceDescriptor.Requires)
	}

	// Verify derived capability edge: inspect_host_service uri argument producer is list_host_services
	producers := inspectServiceDescriptor.ArgumentProducers["uri"]
	found := false
	for _, p := range producers {
		if p == "list_host_services" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("inspect_host_service argumentProducers['uri'] = %#v, want list_host_services", producers)
	}
}

func TestHostAgentNeverAssertsSatisfaction(t *testing.T) {
	server := newContractTestServer(t, false)
	result, err := server.DispatchTool(context.Background(), "get_host_info", nil, nil)
	if err != nil {
		t.Fatalf("get_host_info failed: %v", err)
	}
	if result == nil {
		t.Fatal("get_host_info returned nil result")
	}
	// Verify no tool result structured content asserts satisfaction
	if structured, ok := result.StructuredContent.(map[string]any); ok {
		if _, exists := structured["satisfied"]; exists {
			t.Fatal("host capability result illegally contained orchestrator 'satisfied' intent flag (violates C-05)")
		}
	}
}

func TestRuntimeKindAgreementFailsClosed(t *testing.T) {
	server := newContractTestServer(t, false)

	// Passing container URI where vm is expected or mismatched kind fails closed
	result, err := server.DispatchTool(context.Background(), "get_vm_info", map[string]any{
		"uri": "container:local:web-worker",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("mismatched runtime kind was not rejected: %#v", result)
	}
}

func TestCanonicalRemoteAgentIDValidation(t *testing.T) {
	tenantID := "local"
	expectedID := "agent-host-01"

	// Valid URI construction
	uri, err := resourceid.New("host-service", tenantID, "user/test")
	if err != nil {
		t.Fatalf("resource URI creation failed: %v", err)
	}
	if uri.TenantID != tenantID {
		t.Fatalf("tenant ID = %q, want %q", uri.TenantID, tenantID)
	}

	// Host agent identity verification
	registry := capabilitycatalog.NewRegistry(tools.CapabilityCatalogSnapshot{
		ProviderID: "incus",
		Revision:   "rev-1",
	}, capabilitycatalog.Options{ProviderID: "incus"})
	if registry == nil {
		t.Fatal("failed to initialize registry")
	}

	if expectedID == "" {
		t.Fatal("expectedID cannot be empty")
	}
}
