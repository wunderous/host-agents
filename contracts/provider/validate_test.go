package provider

import "testing"

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
