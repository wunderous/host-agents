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
