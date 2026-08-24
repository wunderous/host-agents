package catalog

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/capability"
	"github.com/wunderous/host-agents/internal/tools"
)

func descriptor(name string) tools.CapabilityDescriptor {
	return tools.CapabilityDescriptor{Name: name, OperationID: name, Version: 1, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, Effect: "read", Provider: "incus", Implementation: "incus-v1"}
}

func executable(descriptor tools.CapabilityDescriptor) capability.Capability {
	return capability.NewLegacyAdapter(descriptor, func(context.Context, capability.RawArguments, tools.ExecutionBinding, capability.ExecutionSink) (*mcp.CallToolResult, error) {
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

func TestRegistryRejectsDuplicateOverlayAndMalformedDescriptors(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus", KnownResourceKinds: map[string]bool{"vm": true}})
	duplicate := descriptor("duplicate_op")
	first := Registration{Descriptor: duplicate, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(duplicate)}
	if err := registry.RegisterRegistration(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterRegistration(first); err == nil {
		t.Fatal("duplicate overlay registration accepted")
	}
	arraySchema := descriptor("array_schema")
	arraySchema.InputSchema = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	if err := registry.RegisterRegistration(Registration{Descriptor: arraySchema, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(arraySchema)}); err == nil {
		t.Fatal("non-object input schema accepted")
	}
	unversioned := descriptor("unversioned")
	unversioned.Version = 0
	if err := registry.RegisterCapability(executable(unversioned), "incus", "incus-v1"); err == nil {
		t.Fatal("capability without a declared version was registered")
	}
	malformed := descriptor("malformed_schema")
	malformed.InputSchema = map[string]any{"type": "object", "required": "uri"}
	if err := registry.RegisterRegistration(Registration{Descriptor: malformed, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(malformed)}); err == nil {
		t.Fatal("malformed JSON schema was accepted")
	}
}

func TestRegistryUnregisterRemovesOverlayAndPublishesNewRevision(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus"})
	removable := descriptor("removable_op")
	if err := registry.RegisterRegistration(Registration{Descriptor: removable, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(removable)}); err != nil {
		t.Fatal(err)
	}
	withOverlay := registry.Snapshot().Revision
	if err := registry.Unregister("removable_op"); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	if snapshot.Revision == withOverlay {
		t.Fatal("removal did not publish a new revision")
	}
	for _, tool := range snapshot.Tools {
		if tool.OperationID == "removable_op" {
			t.Fatal("removed operation still in snapshot")
		}
	}
	if err := registry.Unregister("removable_op"); err == nil {
		t.Fatal("double removal accepted")
	}
}

func TestRegistryRejectsUpsertDowngradeButAllowsSameVersionRefresh(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "com.opute.example", AuthorizedProviders: map[string]bool{"com.opute.example": true}})
	operation := descriptor("opute.capability.example.probe")
	operation.Version = 2
	operation.Provider = "com.opute.example"
	operation.Implementation = "provider:com.opute.example"
	registration := Registration{Descriptor: operation, ProviderID: operation.Provider, Implementation: operation.Implementation, Capability: executable(operation)}
	if err := registry.Upsert(registration); err != nil {
		t.Fatal(err)
	}
	downgraded := operation
	downgraded.Version = 1
	if err := registry.Upsert(Registration{Descriptor: downgraded, ProviderID: operation.Provider, Implementation: operation.Implementation, Capability: executable(downgraded)}); err == nil {
		t.Fatal("version downgrade accepted")
	}
	same := operation
	same.Description = "refreshed"
	if err := registry.Upsert(Registration{Descriptor: same, ProviderID: operation.Provider, Implementation: operation.Implementation, Capability: executable(same)}); err != nil {
		t.Fatalf("same-version refresh rejected: %v", err)
	}
}

func TestRegistryReplaceGenerationSwapsOverlayAtomicallyAndRetiresOld(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
	registry := NewRegistry(base, Options{ProviderID: "incus", AuthorizedProviders: map[string]bool{"com.opute.example": true}})
	first := descriptor("opute.capability.example.first")
	first.Provider = "com.opute.example"
	first.Implementation = "provider:com.opute.example"
	if err := registry.UpsertCapability(executable(first), "com.opute.example", "provider:com.opute.example"); err != nil {
		t.Fatal(err)
	}
	second := descriptor("opute.capability.example.second")
	second.Provider = "com.opute.example"
	second.Implementation = "provider:com.opute.example"
	second.GenerationID = "gen-2"
	third := descriptor("opute.capability.example.third")
	third.Provider = "com.opute.example"
	third.Implementation = "provider:com.opute.example"
	third.GenerationID = "gen-2"
	if err := registry.ReplaceGeneration("gen-2", []capability.Capability{executable(second), executable(third)}); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	seen := map[string]bool{}
	for _, tool := range snapshot.Tools {
		seen[tool.OperationID] = true
	}
	if seen["opute.capability.example.first"] {
		t.Fatal("prior provider overlay survived generation replacement")
	}
	if !seen["opute.capability.example.second"] || !seen["opute.capability.example.third"] {
		t.Fatalf("generation overlay incomplete: %#v", snapshot.Tools)
	}
	broken := descriptor("opute.capability.example.broken")
	broken.Provider = "com.opute.example"
	broken.Implementation = "provider:com.opute.example"
	broken.GenerationID = "gen-3"
	broken.Effect = "shell"
	if err := registry.ReplaceGeneration("gen-3", []capability.Capability{executable(broken)}); err == nil {
		t.Fatal("invalid generation accepted")
	}
	if _, ok := registry.ResolveCapability("opute.capability.example.second"); !ok {
		t.Fatal("failed replacement destroyed the active generation overlay")
	}
	if err := registry.ReplaceGeneration("gen-2", nil); err != nil {
		t.Fatalf("empty generation replacement rejected: %v", err)
	}
	if _, ok := registry.ResolveCapability("opute.capability.example.second"); ok {
		t.Fatal("empty generation replacement retained a retired capability")
	}
}

func TestRegistryReplaceProviderSwapsUngeneratedOverlayAtomically(t *testing.T) {
	registry := NewRegistry(tools.CapabilityCatalogSnapshot{ProviderID: "incus"}, Options{
		ProviderID:          "incus",
		AuthorizedProviders: map[string]bool{"provider-refresh": true},
	})
	makeCapability := func(name string) capability.Capability {
		descriptor := descriptor(name)
		descriptor.Provider = "provider-refresh"
		descriptor.Implementation = "provider:provider-refresh"
		return executable(descriptor)
	}
	first := makeCapability("first")
	second := makeCapability("second")
	if err := registry.ReplaceProvider("provider-refresh", []capability.Capability{first}); err != nil {
		t.Fatalf("initial provider replacement: %v", err)
	}
	if err := registry.ReplaceProvider("provider-refresh", []capability.Capability{second}); err != nil {
		t.Fatalf("provider replacement: %v", err)
	}
	if _, ok := registry.ResolveCapability("first"); ok {
		t.Fatal("prior provider capability survived replacement")
	}
	if _, ok := registry.ResolveCapability("second"); !ok {
		t.Fatal("replacement capability was not published")
	}
	brokenDescriptor := descriptor("broken")
	brokenDescriptor.Provider = "provider-refresh"
	brokenDescriptor.Implementation = "provider:provider-refresh"
	brokenDescriptor.Effect = "shell"
	broken := executable(brokenDescriptor)
	if err := registry.ReplaceProvider("provider-refresh", []capability.Capability{broken}); err == nil {
		t.Fatal("invalid provider replacement was accepted")
	}
	if _, ok := registry.ResolveCapability("second"); !ok {
		t.Fatal("failed replacement destroyed the active provider overlay")
	}
}

func TestRegistryRejectsMixedProviderGeneration(t *testing.T) {
	registry := NewRegistry(tools.CapabilityCatalogSnapshot{ProviderID: "incus"}, Options{
		ProviderID:          "incus",
		AuthorizedProviders: map[string]bool{"com.opute.example": true, "com.opute.other": true},
	})
	one := descriptor("opute.capability.example.one")
	one.Provider = "com.opute.example"
	one.Implementation = "provider:com.opute.example"
	one.GenerationID = "gen-mixed"
	two := descriptor("opute.capability.example.two")
	two.Provider = "com.opute.other"
	two.Implementation = "provider:com.opute.other"
	two.GenerationID = "gen-mixed"
	if err := registry.ReplaceGeneration("gen-mixed", []capability.Capability{executable(one), executable(two)}); err == nil {
		t.Fatal("mixed-provider generation was accepted")
	}
}

func TestRegistryFailedReplacementDoesNotDeleteActiveOverlayOnConflict(t *testing.T) {
	registry := NewRegistry(tools.CapabilityCatalogSnapshot{ProviderID: "incus"}, Options{ProviderID: "incus", AuthorizedProviders: map[string]bool{"com.opute.other": true}})
	active := descriptor("active.capability")
	active.GenerationID = "gen-active"
	if err := registry.ReplaceGeneration("gen-active", []capability.Capability{executable(active)}); err != nil {
		t.Fatal(err)
	}
	conflict := descriptor("active.capability")
	conflict.GenerationID = "gen-candidate"
	conflict.Provider = "com.opute.other"
	conflict.Implementation = "provider:com.opute.other"
	if err := registry.ReplaceGeneration("gen-candidate", []capability.Capability{executable(conflict)}); err == nil {
		t.Fatal("conflicting replacement accepted")
	}
	if _, ok := registry.ResolveCapability("active.capability"); !ok {
		t.Fatal("failed conflicting replacement removed the active capability")
	}
}

func TestRegistrySnapshotRevisionsAreDeterministic(t *testing.T) {
	build := func() *Registry {
		base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base", Tools: []tools.CapabilityDescriptor{descriptor("existing")}}
		registry := NewRegistry(base, Options{ProviderID: "incus"})
		one := descriptor("deterministic_a")
		if err := registry.RegisterRegistration(Registration{Descriptor: one, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(one)}); err != nil {
			t.Fatal(err)
		}
		two := descriptor("deterministic_b")
		if err := registry.RegisterRegistration(Registration{Descriptor: two, ProviderID: "incus", Implementation: "incus-v1", Capability: executable(two)}); err != nil {
			t.Fatal(err)
		}
		return registry
	}
	if build().Snapshot().Revision != build().Snapshot().Revision {
		t.Fatal("same registration order produced different revisions")
	}
}
