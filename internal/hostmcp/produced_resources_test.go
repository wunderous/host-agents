package hostmcp

import (
	"strings"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
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

func TestMaterializeBoundResourceOutputsAttachesCanonicalIdentity(t *testing.T) {
	descriptor := tools.CapabilityDescriptor{
		OperationID: "get_k8s_resource",
		Produces:    []tools.ResourceBinding{{SourcePath: "uri", ResourceType: "cluster"}},
	}
	structured := map[string]any{"yaml": "apiVersion: apps/v1"}
	got := materializeBoundResourceOutputs(descriptor, structured, tools.ExecutionBinding{
		Resources: []tools.BoundResource{{ResourceType: "cluster", URI: "cluster:local:opute-clean-k3s"}},
	})
	if got.(map[string]any)["uri"] != "cluster:local:opute-clean-k3s" {
		t.Fatalf("materialized uri = %#v", got.(map[string]any)["uri"])
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

func TestValidateProducedResourcesAcceptsEmptyManyResult(t *testing.T) {
	descriptor := tools.CapabilityDescriptor{
		OperationID: "list_vms",
		OutputType:  "vm.uri",
		ResultTypes: []capabilitycontract.ResultType{{
			ID:      "vm.uri",
			Version: 1,
			Selectors: []capabilitycontract.ResultSelector{{
				ID: "uri", SourcePath: "items[].uri", Cardinality: capabilitycontract.CardinalityMany,
			}},
		}},
		Produces: []tools.ResourceBinding{{SourcePath: "items[].uri", ResourceType: "vm", SelectorID: "uri"}},
	}
	if err := validateProducedResources(descriptor, map[string]any{"items": []any{}}, "tenant-a"); err != nil {
		t.Fatalf("empty many result rejected: %v", err)
	}
}

func TestValidateProducedResourcesRejectsMissingDeclaredOutput(t *testing.T) {
	descriptor := tools.CapabilityDescriptor{
		OperationID: "list_vms",
		OutputType:  "vm.uri",
		ResultTypes: []capabilitycontract.ResultType{{
			ID:      "vm.uri",
			Version: 1,
			Selectors: []capabilitycontract.ResultSelector{{
				ID: "uri", SourcePath: "items[].uri", Cardinality: capabilitycontract.CardinalityMany,
			}},
		}},
		Produces: []tools.ResourceBinding{{SourcePath: "items[].uri", ResourceType: "vm", SelectorID: "uri"}},
	}
	if err := validateProducedResources(descriptor, map[string]any{}, "tenant-a"); err == nil {
		t.Fatal("expected absent output to fail closed")
	}
}
