package hostmcp

import (
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

func TestActivationPublishManifestFiltersByRecipeCapabilities(t *testing.T) {
	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: "com.opute.cloudflare", Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{
			{ID: "tunneling", CapabilityID: capabilitycontract.Tunneling},
			{ID: "mesh-membership", CapabilityID: capabilitycontract.MeshMembership},
			{ID: "public-ingress", CapabilityID: capabilitycontract.PublicIngress},
		},
	}
	meta := map[string]any{
		"runtime": map[string]any{
			"capabilities": []any{"tunneling"},
		},
	}
	publish := activationPublishManifest(manifest, meta)
	if len(publish.Services) != 1 || publish.Services[0].CapabilityID != capabilitycontract.Tunneling {
		t.Fatalf("expected only tunneling, got %#v", publish.Services)
	}
	full := activationPublishManifest(manifest, map[string]any{})
	if len(full.Services) != 3 {
		t.Fatalf("empty capabilities should publish full surface, got %d", len(full.Services))
	}
}

func TestNormalizeCapabilityFamilyID(t *testing.T) {
	if got := normalizeCapabilityFamilyID("mesh-runtime"); got != capabilitycontract.MeshRuntime {
		t.Fatalf("got %q", got)
	}
	if got := normalizeCapabilityFamilyID(capabilitycontract.Tunneling); got != capabilitycontract.Tunneling {
		t.Fatalf("got %q", got)
	}
}
