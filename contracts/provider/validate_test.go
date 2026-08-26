package provider

import (
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

func validDescriptor() PluginDescriptor {
	return PluginDescriptor{
		Schema: PluginDescriptorVersion, PluginID: "com.opute.example", Version: "1.0.0",
		Capabilities: []CapabilityRef{{ID: "opute.capability.example.v1", Version: 1}},
		Server:       ServerDescriptor{Transport: "streamable_http", Endpoint: "http://127.0.0.1:4318/mcp"},
	}
}

func TestValidateDescriptorRejectsProviderSpecificTransportAndCredentials(t *testing.T) {
	descriptor := validDescriptor()
	descriptor.Server.Transport = "stdio"
	if err := ValidateDescriptor(descriptor); err == nil {
		t.Fatal("stdio provider descriptor was accepted")
	}
	descriptor = validDescriptor()
	descriptor.Server.Endpoint = "http://user:secret@127.0.0.1:4318/mcp"
	if err := ValidateDescriptor(descriptor); err == nil {
		t.Fatal("credential-bearing provider endpoint was accepted")
	}
}

func TestValidateInstallManifestPinsRecipesAndIdentity(t *testing.T) {
	descriptor := validDescriptor()
	manifest := InstallManifest{
		Schema:     InstallManifestVersion,
		Provider:   ProviderRef{ID: descriptor.PluginID, Version: descriptor.Version},
		Provides:   descriptor.Capabilities,
		Recipes:    []RecipeRef{{ID: "example", Source: RecipeSource{URI: "https://example.invalid/recipe.yaml", Revision: "commit", SHA256: "sha256:abc"}}},
		Services:   []ServiceDefinition{{ID: "example", CapabilityID: descriptor.Capabilities[0].ID, Version: 1}},
		Validation: ValidationRef{Capability: descriptor.Capabilities[0].ID, Operation: "validate"},
	}
	if err := ValidateInstallManifest(manifest, manifest.Provider); err != nil {
		t.Fatal(err)
	}
	manifest.Provider.ID = "com.opute.other"
	if err := ValidateInstallManifest(manifest, ProviderRef{ID: descriptor.PluginID, Version: descriptor.Version}); err == nil {
		t.Fatal("manifest identity mismatch was accepted")
	}
	manifest.Provider = ProviderRef{ID: descriptor.PluginID, Version: descriptor.Version}
	manifest.Recipes[0].Source.SHA256 = ""
	if err := ValidateInstallManifest(manifest, manifest.Provider); err == nil {
		t.Fatal("unpinned recipe was accepted")
	}
}

func TestValidateOperationKeepsSelectorsDeclarativeAndOutputScoped(t *testing.T) {
	operation := Operation{
		ID: "inspect.vm", Version: 1,
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}},
		OutputType:   "vm.uri",
		ResultTypes:  []capabilitycontract.ResultType{{ID: "vm.uri", Version: 1, Selectors: []capabilitycontract.ResultSelector{{ID: "uri", SourcePath: "uri", Cardinality: capabilitycontract.CardinalityOne}}}},
		Effect:       "read",
		Produces:     []ResourceBinding{{ResourceType: "vm", SourcePath: "uri", SelectorID: "uri"}},
	}
	if err := validateOperation(operation, "test"); err != nil {
		t.Fatalf("valid selector operation rejected: %v", err)
	}
	operation.Requires = []ResourceBinding{{ResourceType: "vm", Argument: "uri", SelectorID: "uri"}}
	if err := validateOperation(operation, "test"); err == nil {
		t.Fatal("selector on input binding was accepted")
	}
	operation.Requires = nil
	operation.OutputType = "other.result"
	if err := validateOperation(operation, "test"); err == nil {
		t.Fatal("selector bound to a different output type was accepted")
	}
}

// serviceManifest returns a manifest whose only variable is the service under
// test, so a failure names the service rule rather than unrelated envelope
// validation.
func serviceManifest(service ServiceDefinition) InstallManifest {
	descriptor := validDescriptor()
	return InstallManifest{
		Schema:     InstallManifestVersion,
		Provider:   ProviderRef{ID: descriptor.PluginID, Version: descriptor.Version},
		Provides:   descriptor.Capabilities,
		Recipes:    []RecipeRef{{ID: "example", Source: RecipeSource{URI: "https://example.invalid/recipe.yaml", Revision: "commit", SHA256: "sha256:abc"}}},
		Services:   []ServiceDefinition{service},
		Validation: ValidationRef{Capability: descriptor.Capabilities[0].ID, Operation: "validate"},
	}
}

func validService() ServiceDefinition {
	return ServiceDefinition{
		ID:           "opute.capability.example",
		CapabilityID: "opute.capability.example.v1",
		Version:      1,
		Operations: []Operation{{
			ID: "opute.capability.example.inspect", Version: 1, Description: "Inspect.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Effect:       "read",
		}},
	}
}

func TestValidateServicesRequiresNeutralCapabilityFamily(t *testing.T) {
	manifest := serviceManifest(validService())
	if err := ValidateInstallManifest(manifest, manifest.Provider); err != nil {
		t.Fatal(err)
	}
	// A service without a family cannot be keyed, mounted, or resolved to an
	// offering, so it must fail closed rather than register as an orphan.
	missing := serviceManifest(validService())
	missing.Services[0].CapabilityID = ""
	if err := ValidateInstallManifest(missing, missing.Provider); err == nil {
		t.Fatal("service without capabilityId was accepted")
	}
	malformed := serviceManifest(validService())
	malformed.Services[0].CapabilityID = "Opute Capability"
	if err := ValidateInstallManifest(malformed, malformed.Provider); err == nil {
		t.Fatal("service with a non-neutral capabilityId was accepted")
	}
}

func TestValidateServicesRejectsUnusableDependencyGraph(t *testing.T) {
	unversioned := serviceManifest(validService())
	unversioned.Services[0].Dependencies = []CapabilityRef{{ID: "opute.capability.other.v1"}}
	if err := ValidateInstallManifest(unversioned, unversioned.Provider); err == nil {
		t.Fatal("dependency without a version was accepted")
	}
	malformed := serviceManifest(validService())
	malformed.Services[0].Dependencies = []CapabilityRef{{ID: "Not A Capability", Version: 1}}
	if err := ValidateInstallManifest(malformed, malformed.Provider); err == nil {
		t.Fatal("malformed dependency id was accepted")
	}
	// A self-dependency can never be satisfied: the mount would wait on a
	// service key it is itself responsible for providing.
	cyclic := serviceManifest(validService())
	cyclic.Services[0].Dependencies = []CapabilityRef{{ID: cyclic.Services[0].CapabilityID, Version: 1}}
	if err := ValidateInstallManifest(cyclic, cyclic.Provider); err == nil {
		t.Fatal("self-dependency was accepted")
	}
	satisfiable := serviceManifest(validService())
	satisfiable.Services[0].Dependencies = []CapabilityRef{{ID: "opute.capability.other.v1", Version: 1}}
	if err := ValidateInstallManifest(satisfiable, satisfiable.Provider); err != nil {
		t.Fatalf("cross-provider dependency must validate at install: %v", err)
	}
}

func TestServiceKeyIsStableAcrossProviderAndService(t *testing.T) {
	key := ServiceKey("com.opute.example", "opute.capability.example")
	if key != "com.opute.example/opute.capability.example" {
		t.Fatalf("unexpected service key %q", key)
	}
	if ServiceKey(" com.opute.example ", " opute.capability.example ") != key {
		t.Fatal("service key must not vary with surrounding whitespace")
	}
	if ServiceKey("com.opute.other", "opute.capability.example") == key {
		t.Fatal("two providers offering one family must not share a service key")
	}
}

func TestValidateServicesRequiresAtLeastOneService(t *testing.T) {
	// A serviceless manifest would install and activate while owning nothing:
	// no plugin is mounted, so the adapter connected to the provider has no
	// fiber to dispose it and no service through which to reach it.
	if err := validateServices(nil); err == nil {
		t.Fatal("a manifest with no services was accepted")
	}
	if err := validateServices([]ServiceDefinition{}); err == nil {
		t.Fatal("a manifest with an empty service list was accepted")
	}
	if err := validateServices([]ServiceDefinition{validService()}); err != nil {
		t.Fatalf("a manifest with one valid service was rejected: %v", err)
	}
}
