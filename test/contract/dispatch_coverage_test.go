package contract_test

import (
	"testing"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/tools"
)

var requiredTunnelInventoryDispatch = []string{
	"list_vms",
	"get_vm_info",
	"list_clusters",
	"get_cluster_details",
	"get_cluster_runtime_details",
}

var requiredPlatformPostgresDispatch = []string{
	"reconcile_postgresql_service",
	"get_postgresql_service_status",
	"remove_postgresql_service",
	"release_postgresql_service_relay",
	"reset_incus_stack",
}

var requiredVmResourceDispatch = []string{
	"update_vm_resources",
}

func TestVmResourceToolsHaveDispatchCoverage(t *testing.T) {
	dispatched := loadDispatchToolNames(t)
	incus, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	catalog := make(map[string]bool, len(incus))
	for _, tool := range incus {
		catalog[tool.Name] = true
	}
	for _, name := range requiredVmResourceDispatch {
		if !dispatched[name] {
			t.Fatalf("VM resource tool %q is not registered in the dispatch registry", name)
		}
		if !catalog[name] {
			t.Fatalf("VM resource tool %q missing from the tunnel catalog", name)
		}
		if !tools.IsStandaloneMutation(name) {
			t.Fatalf("VM resource tool %q must be a standalone mutation", name)
		}
		if !tools.StandaloneToolNames[name] {
			t.Fatalf("VM resource tool %q missing from the standalone catalog", name)
		}
	}
}

func TestPlatformPostgresAndResetToolsHaveDispatchCoverage(t *testing.T) {
	dispatched := loadDispatchToolNames(t)
	incus, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	catalog := make(map[string]bool, len(incus))
	for _, tool := range incus {
		catalog[tool.Name] = true
	}
	for _, name := range requiredPlatformPostgresDispatch {
		if !dispatched[name] {
			t.Fatalf("platform/reset tool %q is not registered in the dispatch registry", name)
		}
		if !catalog[name] {
			t.Fatalf("platform/reset tool %q missing from the tunnel catalog", name)
		}
	}
}

func TestPlatformPostgresAndResetToolsHaveStandaloneCoverage(t *testing.T) {
	defs := tools.StandaloneToolDefinitions()
	definitions := make(map[string]bool, len(defs))
	for _, tool := range defs {
		definitions[tool.Name] = true
	}
	for _, name := range requiredPlatformPostgresDispatch {
		if !tools.StandaloneToolNames[name] {
			t.Fatalf("platform/reset tool %q missing from standalone catalog", name)
		}
		if !definitions[name] {
			t.Fatalf("platform/reset tool %q missing from standalone tool definitions", name)
		}
	}
	for _, name := range []string{"reconcile_postgresql_service", "remove_postgresql_service", "release_postgresql_service_relay", "reset_incus_stack"} {
		if !tools.IsStandaloneMutation(name) {
			t.Fatalf("mutation tool %q must be a standalone mutation", name)
		}
	}
	if tools.IsStandaloneMutation("get_postgresql_service_status") {
		t.Fatal("get_postgresql_service_status must remain read-only")
	}
}

func TestSQLiteProvisioningToolsHaveDispatchAndStandaloneCoverage(t *testing.T) {
	dispatched := loadDispatchToolNames(t)
	incus, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	catalog := make(map[string]bool, len(incus))
	for _, tool := range incus {
		catalog[tool.Name] = true
	}
	for _, name := range []string{"ensure_sqlite_database", "get_sqlite_database_status", "remove_sqlite_database"} {
		if !dispatched[name] || !catalog[name] || !tools.StandaloneToolNames[name] {
			t.Fatalf("SQLite provisioning tool %q is missing dispatch, catalog, or standalone coverage", name)
		}
	}
}

func TestRequiredInventoryToolsHaveDispatchCase(t *testing.T) {
	dispatched := loadDispatchToolNames(t)
	for _, name := range requiredTunnelInventoryDispatch {
		if hostMCPBypassTools[name] {
			continue
		}
		if !dispatched[name] {
			t.Fatalf("required inventory tool %q is not registered in the dispatch registry", name)
		}
	}
}

func TestContainerStorageToolsHaveDispatchAndStandaloneCoverage(t *testing.T) {
	dispatched := loadDispatchToolNames(t)
	incus, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	catalog := make(map[string]bool, len(incus))
	for _, tool := range incus {
		catalog[tool.Name] = true
	}
	definitions := make(map[string]bool)
	for _, tool := range tools.StandaloneToolDefinitions() {
		definitions[tool.Name] = true
	}
	for _, name := range []string{"inspect_container_storage", "cleanup_container_storage"} {
		if !dispatched[name] {
			t.Fatalf("container storage tool %q is not registered in the dispatch registry", name)
		}
		if !catalog[name] {
			t.Fatalf("container storage tool %q missing from the Incus catalog", name)
		}
		if !tools.StandaloneToolNames[name] || !definitions[name] {
			t.Fatalf("container storage tool %q missing from standalone coverage", name)
		}
	}
	if tools.IsStandaloneMutation("inspect_container_storage") {
		t.Fatal("inspect_container_storage must remain read-only")
	}
	if !tools.IsStandaloneMutation("cleanup_container_storage") {
		t.Fatal("cleanup_container_storage must be a standalone mutation")
	}
}

func TestListVmNetworkDevicesOmittedFromCatalog(t *testing.T) {
	got, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range got {
		if tool.Name == "list_vm_network_devices" {
			t.Fatal("list_vm_network_devices must be omitted from tunnel catalog")
		}
	}
}

var hostMCPBypassTools = map[string]bool{
	"stream_vm_console":  true,
	"send_console_input": true,
	"resize_console":     true,
}

func loadDispatchToolNames(t *testing.T) map[string]bool {
	t.Helper()
	// Enumerate the dispatch registry. This used to read internal/tools/dispatch.go
	// as text and match `case "..."` labels; replacing the switch with a table
	// would have left that matching nothing (plan §2.4). The registry is the
	// same source of truth dispatch itself uses, so the two cannot diverge.
	registered := tools.RegisteredToolNames()
	if len(registered) == 0 {
		t.Fatal("dispatch registry is empty")
	}
	names := make(map[string]bool, len(registered))
	for _, name := range registered {
		names[name] = true
	}
	return names
}

// TestDispatchRegistryMatchesToolNameContract is the partition assertion (W2).
//
// The registry's key set must equal internal/contract/toolname exactly. A name
// in the contract with no handler is an unroutable capability; a handler under
// a name not in the contract is unreachable. Once the eight domain packages
// each register their own names, this is what catches a tool that lands in no
// domain -- and registry.register's duplicate panic catches one that lands in two.
func TestDispatchRegistryMatchesToolNameContract(t *testing.T) {
	registered := make(map[string]bool)
	for _, name := range tools.RegisteredToolNames() {
		registered[name] = true
	}
	declared := make(map[string]bool)
	for _, name := range toolname.All() {
		declared[name] = true
	}

	for name := range declared {
		if !registered[name] {
			t.Errorf("tool %q is declared in contract/toolname but has no dispatch handler", name)
		}
	}
	for name := range registered {
		if !declared[name] {
			t.Errorf("tool %q has a dispatch handler but is not declared in contract/toolname", name)
		}
	}
}
