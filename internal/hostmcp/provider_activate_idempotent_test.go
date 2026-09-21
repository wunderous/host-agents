package hostmcp

import (
	"context"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
)

// TestActivateProviderGenerationIdempotentForActiveGeneration covers the
// Tailscale seam-activate flake: completed activate plan + reconnect/reload
// with the same activationNonce must not Fail the live generation.
func TestActivateProviderGenerationIdempotentForActiveGeneration(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")

	firstID := activateWithRealManifestHash(t, server, provider)
	active, ok := server.providerLifecycle.Active("com.opute.boundary")
	if !ok || active.ID != firstID {
		t.Fatalf("expected active generation %q, got %#v ok=%v", firstID, active, ok)
	}

	// Simulate activateCompletedProviderCandidate then end-of-plan activate again.
	if err := server.activateProviderGeneration(map[string]any{
		"activate":             true,
		"providerGenerationId": firstID,
	}); err != nil {
		t.Fatalf("second activate must be idempotent: %v", err)
	}
	active, ok = server.providerLifecycle.Active("com.opute.boundary")
	if !ok || active.ID != firstID {
		t.Fatalf("generation must stay active after re-activate: %#v ok=%v", active, ok)
	}

	// activateCompletedProviderCandidate path (explicit).
	if err := server.activateCompletedProviderCandidate(map[string]any{
		"activate":             true,
		"providerGenerationId": firstID,
	}); err != nil {
		t.Fatalf("activateCompletedProviderCandidate: %v", err)
	}
	if err := server.activateProviderGeneration(map[string]any{
		"activate":             true,
		"providerGenerationId": firstID,
	}); err != nil {
		t.Fatalf("activate after completed-candidate path: %v", err)
	}
	active, ok = server.providerLifecycle.Active("com.opute.boundary")
	if !ok || active.ID != firstID {
		t.Fatalf("still expected active %q: %#v ok=%v", firstID, active, ok)
	}
}

// TestActivateCompletedThenPlanActivateDoesNotFailLiveGeneration builds a fresh
// candidate, activates via completed-reconnect helper, then invokes the plan
// completion activate path again — matching reload with ...-default nonce.
func TestActivateCompletedThenPlanActivateDoesNotFailLiveGeneration(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")

	descriptor := providercontract.PluginDescriptor{
		Schema:       providercontract.PluginDescriptorVersion,
		PluginID:     "com.opute.boundary",
		Version:      provider.generation,
		Capabilities: []providercontract.CapabilityRef{{ID: capabilitycontract.Kubernetes, Version: 1}},
		Server:       providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: provider.httpServer.URL},
	}
	adapter, err := provideradapter.Connect(context.Background(), descriptor, provideradapter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := adapter.InstallManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hashProviderValue(manifest)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := server.providerLifecycle.CreateCandidate(manifest.Provider, hash, provider.httpServer.URL, server.CatalogSnapshot().Revision)
	if err != nil {
		_ = adapter.Close()
		t.Fatal(err)
	}
	server.providerMu.Lock()
	server.providerCandidates[generation.ID] = adapter
	server.providerCandidateManifests[generation.ID] = manifest
	server.providerMu.Unlock()

	meta := map[string]any{
		"activate":             true,
		"providerGenerationId": generation.ID,
		"providerId":           manifest.Provider.ID,
	}
	if err := server.activateCompletedProviderCandidate(meta); err != nil {
		t.Fatalf("completed reconnect activate: %v", err)
	}
	// Plan completion always calls activateProviderGeneration; must not Fail.
	if err := server.activateProviderGeneration(meta); err != nil {
		t.Fatalf("plan-completion activate: %v", err)
	}
	// Simulate plan_run cleanup branch: only Fail on error. Success path completes.
	server.completeProviderCandidate(generation.ID)

	active, ok := server.providerLifecycle.Active(manifest.Provider.ID)
	if !ok || active.ID != generation.ID {
		t.Fatalf("expected active=%q, got %#v ok=%v", generation.ID, active, ok)
	}
	status, err := server.handleProviderStatus(map[string]any{"provider": manifest.Provider.ID})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := status.StructuredContent.(map[string]any)
	if body["active"] != true {
		t.Fatalf("provider status active=%v body=%#v", body["active"], body)
	}
}
