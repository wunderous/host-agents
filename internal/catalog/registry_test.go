package catalog

import (
	"testing"

	"github.com/wunderous/host-agents/internal/tools"
)

func descriptor(name string) tools.CapabilityDescriptor {
	return tools.CapabilityDescriptor{Name: name, OperationID: name, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, Effect: "read", Provider: "incus", Implementation: "incus-v1"}
}

func TestRegistryRejectsUnsafeOrConflictingRegistrationsAndBumpsRevision(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus", KnownResourceKinds: map[string]bool{"vm": true}})
	before := registry.Snapshot().Revision
	if err := registry.Register(Registration{Descriptor: descriptor("new_op"), ProviderID: "incus", Implementation: "incus-v1"}); err != nil {
		t.Fatal(err)
	}
	after := registry.Snapshot()
	if before == after.Revision || len(after.Tools) != 2 {
		t.Fatalf("revision/tools after registration = %q/%d", after.Revision, len(after.Tools))
	}
	if err := registry.Register(Registration{Descriptor: descriptor("existing"), ProviderID: "incus", Implementation: "incus-v1"}); err == nil {
		t.Fatal("base conflict was accepted")
	}
	unsafe := descriptor("unsafe")
	unsafe.Effect = "shell"
	if err := registry.Register(Registration{Descriptor: unsafe, ProviderID: "incus", Implementation: "incus-v1"}); err == nil {
		t.Fatal("unsupported effect was accepted")
	}
	unknown := descriptor("unknown")
	unknown.ResourceKinds = []string{"database"}
	if err := registry.Register(Registration{Descriptor: unknown, ProviderID: "incus", Implementation: "incus-v1"}); err == nil {
		t.Fatal("unknown resource kind was accepted")
	}
}

func TestAuthorizedExternalProviderCanUpsertDynamicOperation(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus", AuthorizedProviders: map[string]bool{"com.opute.example": true}})
	operation := descriptor("opute.capability.example.validate")
	operation.Provider = "com.opute.example"
	operation.Implementation = "provider:com.opute.example"
	registration := Registration{Descriptor: operation, ProviderID: operation.Provider, Implementation: operation.Implementation}
	if err := registry.Upsert(registration); err != nil {
		t.Fatal(err)
	}
	first := registry.Snapshot().Revision
	operation.Description = "reloaded"
	if err := registry.Upsert(Registration{Descriptor: operation, ProviderID: operation.Provider, Implementation: operation.Implementation}); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	if snapshot.Revision == first || len(snapshot.Tools) != 2 || snapshot.Tools[1].Description != "reloaded" {
		t.Fatalf("dynamic upsert snapshot = %+v", snapshot)
	}
}
