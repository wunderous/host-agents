package catalog

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/capability"
	"github.com/wunderous/host-agents/internal/tools"
)

func descriptor(name string) tools.CapabilityDescriptor {
	return tools.CapabilityDescriptor{Name: name, OperationID: name, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, Effect: "read", Provider: "incus", Implementation: "incus-v1"}
}

func executable(descriptor tools.CapabilityDescriptor) capability.Capability {
	return capability.NewLegacyAdapter(descriptor, func(context.Context, capability.RawArguments, capability.ExecutionSink) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{}}, nil
	})
}

func TestRegistryRejectsUnsafeOrConflictingRegistrationsAndBumpsRevision(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus", KnownResourceKinds: map[string]bool{"vm": true}})
	before := registry.Snapshot().Revision
	newDescriptor := descriptor("new_op")
	if err := registry.RegisterRegistration(Registration{Descriptor: newDescriptor, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(newDescriptor)}); err != nil {
		t.Fatal(err)
	}
	after := registry.Snapshot()
	if before == after.Revision || len(after.Tools) != 2 {
		t.Fatalf("revision/tools after registration = %q/%d", after.Revision, len(after.Tools))
	}
	existingDescriptor := descriptor("existing")
	if err := registry.RegisterRegistration(Registration{Descriptor: existingDescriptor, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(existingDescriptor)}); err == nil {
		t.Fatal("base conflict was accepted")
	}
	unsafe := descriptor("unsafe")
	unsafe.Effect = "shell"
	if err := registry.RegisterRegistration(Registration{Descriptor: unsafe, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(unsafe)}); err == nil {
		t.Fatal("unsupported effect was accepted")
	}
	unknown := descriptor("unknown")
	unknown.ResourceKinds = []string{"database"}
	if err := registry.RegisterRegistration(Registration{Descriptor: unknown, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(unknown)}); err == nil {
		t.Fatal("unknown resource kind was accepted")
	}
}

func TestAuthorizedExternalProviderCanUpsertDynamicOperation(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus", AuthorizedProviders: map[string]bool{"com.opute.example": true}})
	operation := descriptor("opute.capability.example.validate")
	operation.Provider = "com.opute.example"
	operation.Implementation = "provider:com.opute.example"
	registration := Registration{Descriptor: operation, ProviderID: operation.Provider, Implementation: operation.Implementation, Capability: executable(operation)}
	if err := registry.Upsert(registration); err != nil {
		t.Fatal(err)
	}
	first := registry.Snapshot().Revision
	operation.Description = "reloaded"
	if err := registry.Upsert(Registration{Descriptor: operation, ProviderID: operation.Provider, Implementation: operation.Implementation, Capability: executable(operation)}); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	if snapshot.Revision == first || len(snapshot.Tools) != 2 || snapshot.Tools[1].Description != "reloaded" {
		t.Fatalf("dynamic upsert snapshot = %+v", snapshot)
	}
}

func TestRegistryValidatesExplicitBindingsAndRevisionResolution(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus", KnownResourceKinds: map[string]bool{"vm": true}})
	bound := descriptor("bound")
	bound.InputSchema = map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}
	bound.OutputSchema = map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}
	bound.Requires = []tools.ResourceBinding{{Argument: "uri", ResourceType: "vm", Required: true}}
	bound.Produces = []tools.ResourceBinding{{SourcePath: "uri", ResourceType: "vm"}}
	value := executable(bound)
	if err := registry.Register(value); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	if _, err := registry.Resolve("bound", registry.Snapshot().Revision); err != nil {
		t.Fatalf("revisioned resolve failed: %v", err)
	}
	if _, err := registry.Resolve("bound", "stale"); err == nil {
		t.Fatal("stale catalog revision resolved")
	}
	invalid := descriptor("invalid_binding")
	invalid.Requires = []tools.ResourceBinding{{Argument: "missing", ResourceType: "vm"}}
	if err := registry.Register(executable(invalid)); err == nil {
		t.Fatal("binding to an undeclared input was accepted")
	}
}

func TestRegistrySnapshotsDoNotExposeMutableSchemaMaps(t *testing.T) {
	input := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}
	value := descriptor("immutable")
	value.InputSchema = input
	registry := NewRegistry(tools.CapabilityCatalogSnapshot{ProviderID: "incus", Tools: []tools.CapabilityDescriptor{value}}, Options{ProviderID: "incus"})
	snapshot := registry.Snapshot()
	snapshot.Tools[0].InputSchema["mutated"] = true
	if _, ok := registry.Snapshot().Tools[0].InputSchema["mutated"]; ok {
		t.Fatal("snapshot mutation changed registry state")
	}
}
