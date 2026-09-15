package hostmcp

import (
	"context"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
)

// boundaryInstallArgs is what the recipe layer sees for an inline descriptor:
// no file on disk, so the test never depends on a plugin directory existing.
func boundaryInstallArgs(provider *boundaryProvider, extra map[string]any) map[string]any {
	args := map[string]any{
		"descriptor": map[string]any{
			"schema":       providercontract.PluginDescriptorVersion,
			"pluginId":     "com.opute.boundary",
			"version":      provider.generation,
			"capabilities": []any{map[string]any{"id": string(capabilitycontract.Kubernetes), "version": 1}},
			"server":       map[string]any{"transport": "streamable_http", "endpoint": provider.httpServer.URL},
		},
	}
	for key, value := range extra {
		args[key] = value
	}
	return args
}

// activateWithRealManifestHash mirrors activateBoundaryProvider but records the
// hash install actually computes, so the short-circuit is compared against a
// generation a real install would have produced rather than a stand-in.
func activateWithRealManifestHash(t *testing.T, server *Server, provider *boundaryProvider) string {
	t.Helper()
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
	if err := server.activateProviderGeneration(map[string]any{
		"activate":             true,
		"providerGenerationId": generation.ID,
	}); err != nil {
		t.Fatal(err)
	}
	active, ok := server.providerLifecycle.Active(manifest.Provider.ID)
	if !ok {
		t.Fatal("expected an active generation after activation")
	}
	return active.ID
}

func installStructured(t *testing.T, server *Server, args map[string]any) map[string]any {
	t.Helper()
	result, err := server.handleProviderInstallContext(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured content, got %#v (isError=%v)", result.StructuredContent, result.IsError)
	}
	return structured
}

func installReused(t *testing.T, server *Server, args map[string]any) bool {
	t.Helper()
	result, err := server.handleProviderInstallContext(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	return ok && structured["reused"] == true
}

func TestProviderInstallReusesAnIdenticalActiveGeneration(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")
	activeID := activateWithRealManifestHash(t, server, provider)

	server.providerMu.RLock()
	candidatesBefore := len(server.providerCandidates)
	server.providerMu.RUnlock()

	structured := installStructured(t, server, boundaryInstallArgs(provider, nil))
	if structured["reused"] != true {
		t.Fatalf("expected the second install to reuse the active generation, got %#v", structured)
	}
	if structured["providerGenerationId"] != activeID {
		t.Fatalf("expected generation %q, got %#v", activeID, structured["providerGenerationId"])
	}
	if structured["status"] != "already-installed" {
		t.Fatalf("expected already-installed, got %#v", structured["status"])
	}
	current, ok := server.providerLifecycle.Active("com.opute.boundary")
	if !ok || current.ID != activeID {
		t.Fatalf("the short-circuit must not change the active generation: %#v", current)
	}
	server.providerMu.RLock()
	candidatesAfter := len(server.providerCandidates)
	server.providerMu.RUnlock()
	if candidatesAfter != candidatesBefore {
		t.Fatalf("expected no new candidate generation, candidates went from %d to %d", candidatesBefore, candidatesAfter)
	}
}

func TestProviderInstallStillRunsWhenForced(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")
	activateWithRealManifestHash(t, server, provider)

	// The boundary manifest declares no recipe, so a real install cannot
	// complete here -- which is exactly the signal wanted: getting past the
	// short-circuit at all proves `force` bypassed it.
	if installReused(t, server, boundaryInstallArgs(provider, map[string]any{"force": true})) {
		t.Fatal("force must not reuse the active generation")
	}
}

func TestProviderInstallDoesNotReuseAfterTheEndpointMoves(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")
	activateWithRealManifestHash(t, server, provider)

	moved := newBoundaryProvider(t, "1.0.0")
	if installReused(t, server, boundaryInstallArgs(moved, nil)) {
		t.Fatal("an install from a different endpoint must not be treated as already installed")
	}
}

func TestProviderInstallDoesNotReuseWhenTheManifestChanges(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")
	activateWithRealManifestHash(t, server, provider)

	republished := newBoundaryProvider(t, "1.1.0")
	if installReused(t, server, boundaryInstallArgs(republished, nil)) {
		t.Fatal("a changed manifest must not be treated as already installed")
	}
}

func TestProviderReloadNeverShortCircuits(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")
	activateWithRealManifestHash(t, server, provider)

	result, err := server.handleProviderReloadContext(context.Background(), boundaryInstallArgs(provider, nil))
	if err != nil {
		t.Fatal(err)
	}
	if structured, ok := result.StructuredContent.(map[string]any); ok && structured["reused"] == true {
		t.Fatal("reload must re-establish the provider rather than report it already installed")
	}
}
