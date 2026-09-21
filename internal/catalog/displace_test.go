package catalog

import (
	"testing"

	"github.com/wunderous/host-agents/internal/tools"
)

func TestDisplaceCapabilityFamiliesRemovesOtherProviderSeam(t *testing.T) {
	base := tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: "base"}
	registry := NewRegistry(base, Options{
		ProviderID: "incus",
		AuthorizedProviders: map[string]bool{
			"com.opute.cloudflare": true,
			"com.opute.tailscale":  true,
		},
	})
	if err := registry.AuthorizeProvider("com.opute.cloudflare"); err != nil {
		t.Fatal(err)
	}
	if err := registry.AuthorizeProvider("com.opute.tailscale"); err != nil {
		t.Fatal(err)
	}

	cfOp := descriptor("opute.capability.public-ingress.ensure")
	cfOp.CapabilityID = "opute.capability.public-ingress.v1"
	cfOp.Provider = "com.opute.cloudflare"
	cfOp.Implementation = "provider:com.opute.cloudflare"
	cfOp.Effect = "mutation"
	if err := registry.RegisterRegistration(Registration{
		Descriptor: cfOp, ProviderID: cfOp.Provider, Implementation: cfOp.Implementation, Capability: executable(cfOp),
	}); err != nil {
		t.Fatal(err)
	}

	meshOp := descriptor("opute.capability.mesh-membership.enroll")
	meshOp.CapabilityID = "opute.capability.mesh-membership.v1"
	meshOp.Provider = "com.opute.cloudflare"
	meshOp.Implementation = "provider:com.opute.cloudflare"
	meshOp.Effect = "mutation"
	if err := registry.RegisterRegistration(Registration{
		Descriptor: meshOp, ProviderID: meshOp.Provider, Implementation: meshOp.Implementation, Capability: executable(meshOp),
	}); err != nil {
		t.Fatal(err)
	}

	removed := registry.DisplaceCapabilityFamilies("com.opute.tailscale", []string{"opute.capability.public-ingress.v1"})
	if len(removed) != 1 || removed[0] != "opute.capability.public-ingress.ensure" {
		t.Fatalf("removed = %#v", removed)
	}
	snapshot := registry.Snapshot()
	names := make([]string, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		names = append(names, tool.OperationID)
	}
	for _, name := range names {
		if name == "opute.capability.public-ingress.ensure" {
			t.Fatalf("public-ingress op still present: %#v", names)
		}
	}
	foundMesh := false
	for _, name := range names {
		if name == "opute.capability.mesh-membership.enroll" {
			foundMesh = true
		}
	}
	if !foundMesh {
		t.Fatalf("mesh-membership should remain untouched: %#v", names)
	}
}
