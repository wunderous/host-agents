package tui

import (
	"testing"

	"github.com/wunderous/host-agents/internal/tools"
)

func TestCompleteWithEntitiesReturnsTypedAuthorizedReferences(t *testing.T) {
	entities := testEntities()
	candidates := CompleteWithEntities("get_vm_info vmName=@worker", nil, entities)
	if len(candidates) != 2 || candidates[0] != "vmName=@vm:worker-01" || candidates[1] != "vmName=@vm:worker-02" {
		t.Fatalf("entity completions = %#v", candidates)
	}
}

func TestCompleteWithEntitiesFallsBackToCapabilityCompletion(t *testing.T) {
	catalog := NewCatalog(tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "catalog-1", Tools: []tools.CapabilityDescriptor{
		{Name: "get_host_info"},
		{Name: "list_vms"},
	}})
	candidates := CompleteWithEntities("get_", catalog, nil)
	if len(candidates) != 1 || candidates[0] != "get_host_info" {
		t.Fatalf("capability completions = %#v", candidates)
	}
}
