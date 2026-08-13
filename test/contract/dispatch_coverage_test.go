package contract_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

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
	"ensure_pgvector",
	"get_pgvector_status",
	"remove_postgresql_service",
	"reset_incus_stack",
}

var requiredPlatformTidbDispatch = []string{"reconcile_tidb_service", "get_tidb_service_status", "remove_tidb_service"}

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
			t.Fatalf("VM resource tool %q has no dispatch case in internal/tools/dispatch.go", name)
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
			t.Fatalf("platform/reset tool %q has no dispatch case in internal/tools/dispatch.go", name)
		}
		if !catalog[name] {
			t.Fatalf("platform/reset tool %q missing from the tunnel catalog", name)
		}
	}
}

func TestPlatformTidbToolsHaveDispatchAndCatalogCoverage(t *testing.T) {
	dispatched := loadDispatchToolNames(t)
	incus, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	catalog := make(map[string]bool, len(incus))
	for _, tool := range incus {
		catalog[tool.Name] = true
	}
	for _, name := range requiredPlatformTidbDispatch {
		if !dispatched[name] || !catalog[name] || !tools.StandaloneToolNames[name] {
			t.Fatalf("TiDB tool %q is missing dispatch, tunnel catalog, or standalone catalog coverage", name)
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
	for _, name := range []string{"reconcile_postgresql_service", "ensure_pgvector", "remove_postgresql_service", "reset_incus_stack"} {
		if !tools.IsStandaloneMutation(name) {
			t.Fatalf("mutation tool %q must be a standalone mutation", name)
		}
	}
	if tools.IsStandaloneMutation("get_postgresql_service_status") {
		t.Fatal("get_postgresql_service_status must remain read-only")
	}
	if tools.IsStandaloneMutation("get_pgvector_status") {
		t.Fatal("get_pgvector_status must remain read-only")
	}
}

func TestRequiredInventoryToolsHaveDispatchCase(t *testing.T) {
	dispatched := loadDispatchToolNames(t)
	for _, name := range requiredTunnelInventoryDispatch {
		if hostMCPBypassTools[name] {
			continue
		}
		if !dispatched[name] {
			t.Fatalf("required inventory tool %q has no dispatch case in internal/tools/dispatch.go", name)
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
			t.Fatalf("container storage tool %q has no dispatch case", name)
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
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dispatchPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "tools", "dispatch.go")
	raw, err := os.ReadFile(dispatchPath)
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`case\s+((?:"[^"]+"\s*,?\s*)+):`).FindAllStringSubmatch(string(raw), -1)
	names := make(map[string]bool)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		for _, nameMatch := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(match[1], -1) {
			if len(nameMatch) < 2 {
				continue
			}
			name := strings.TrimSpace(nameMatch[1])
			if name != "" {
				names[name] = true
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("no dispatch cases found in dispatch.go")
	}
	return names
}
