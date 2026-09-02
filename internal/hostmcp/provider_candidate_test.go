package hostmcp

import (
	"context"
	"testing"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
)

func TestProviderCandidateRecipeDispatchUsesPrivateCatalogProjection(t *testing.T) {
	server, _ := newBindingTestServer(t)
	provider := newBoundaryProvider(t, "1.0.0")
	descriptor := providercontract.PluginDescriptor{
		Schema:       providercontract.PluginDescriptorVersion,
		PluginID:     "com.opute.boundary",
		Version:      "1.0.0",
		Capabilities: []providercontract.CapabilityRef{{ID: "kubernetes.v1", Version: 1}},
		Server:       providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: provider.httpServer.URL},
	}
	adapter, err := provideradapter.Connect(context.Background(), descriptor, provideradapter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	manifest := boundaryManifest("1.0.0")
	candidate, err := server.providerLifecycle.CreateCandidate(manifest.Provider, "sha256:candidate", provider.httpServer.URL, server.CatalogSnapshot().Revision)
	if err != nil {
		t.Fatal(err)
	}
	server.providerMu.Lock()
	server.providerCandidates[candidate.ID] = adapter
	server.providerCandidateManifests[candidate.ID] = manifest
	server.providerMu.Unlock()

	for _, published := range server.CatalogSnapshot().Tools {
		if published.Name == "opute.capability.boundary.probe" {
			t.Fatal("candidate operation leaked into the public catalog")
		}
	}
	snapshot, ok := server.providerCandidateSnapshot(candidate.ID)
	if !ok {
		t.Fatal("candidate snapshot was not projected")
	}
	if snapshot.Revision == server.CatalogSnapshot().Revision {
		t.Fatal("candidate snapshot did not carry its private descriptor projection")
	}
	result, err, handled := server.dispatchCandidateTool(context.Background(), "opute.capability.boundary.probe", map[string]any{}, nil, candidate.ID, snapshot)
	if err != nil || !handled || result == nil || result.IsError {
		t.Fatalf("candidate dispatch = %#v, err=%v, handled=%v", result, err, handled)
	}
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || value["generation"] != "1.0.0" {
		t.Fatalf("candidate result = %#v", result.StructuredContent)
	}
}
