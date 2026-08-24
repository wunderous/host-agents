package hostmcp

import (
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/tools"
)

func TestValidateProducedResourcesAcceptsTypedTenantLocalOutput(t *testing.T) {
	descriptor := tools.CapabilityDescriptor{
		OperationID: "list_vms",
		Produces:    []tools.ResourceBinding{{SourcePath: "items[].uri", ResourceType: "vm"}},
	}
	if err := validateProducedResources(descriptor, map[string]any{
		"items": []any{map[string]any{"uri": "vm:tenant-a:vm-1"}},
	}, "tenant-a"); err != nil {
		t.Fatalf("validate produced resources: %v", err)
	}
}

func TestValidateProducedResourcesRejectsMismatchedKindAndTenant(t *testing.T) {
	descriptor := tools.CapabilityDescriptor{
		OperationID: "list_vms",
		Produces:    []tools.ResourceBinding{{SourcePath: "uri", ResourceType: "vm"}},
	}
	for _, test := range []struct {
		name string
		uri  string
		want string
	}{
		{name: "kind", uri: "pod:tenant-a:pod-1", want: "resource kind"},
		{name: "tenant", uri: "vm:tenant-b:vm-1", want: "foreign-tenant"},
		{name: "malformed", uri: "not-a-uri", want: "invalid resource output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateProducedResources(descriptor, map[string]any{"uri": test.uri}, "tenant-a")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateProducedResourcesRejectsMissingDeclaredOutput(t *testing.T) {
	descriptor := tools.CapabilityDescriptor{
		OperationID: "list_vms",
		Produces:    []tools.ResourceBinding{{SourcePath: "items[].uri", ResourceType: "vm"}},
	}
	if err := validateProducedResources(descriptor, map[string]any{"items": []any{}}, "tenant-a"); err == nil {
		t.Fatal("expected missing output to fail closed")
	}
}
