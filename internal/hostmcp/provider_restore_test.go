package hostmcp

import (
	"context"
	"encoding/json"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tools"
)

func TestRestoreProviderGenerationRefreshesLiveManifest(t *testing.T) {
	boundary := newBoundaryProvider(t, "1.0.0")
	stateDir := t.TempDir()
	store, err := state.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}

	descriptor := providercontract.PluginDescriptor{
		Schema:       providercontract.PluginDescriptorVersion,
		PluginID:     "com.opute.boundary",
		Version:      "1.0.0",
		Capabilities: []providercontract.CapabilityRef{{ID: capabilitycontract.Kubernetes, Version: 1}},
		Server:       providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: boundary.httpServer.URL},
	}
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	staleManifest := boundaryManifest("1.0.0")
	staleManifest.Services[0].CapabilityID = ""
	staleManifestJSON, err := json.Marshal(staleManifest)
	if err != nil {
		t.Fatal(err)
	}
	const generationID = "com.opute.boundary-1"
	if err := store.SaveProviderGeneration(state.ProviderGenerationRecord{
		GenerationID:    generationID,
		ProviderID:      descriptor.PluginID,
		ProviderVersion: descriptor.Version,
		ManifestHash:    "sha256:stale-manifest",
		Endpoint:        descriptor.Server.Endpoint,
		DescriptorJSON:  string(descriptorJSON),
		ManifestJSON:    string(staleManifestJSON),
		CatalogRevision: "sha256:stale-catalog",
		Status:          "active",
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	service := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	server, err := NewServer(Options{
		ProviderID:     string(hostruntime.IDIncus),
		Ops:            service,
		Standalone:     true,
		AllowMutations: true,
		StateDir:       stateDir,
	})
	if err != nil {
		t.Fatalf("restore server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	active, ok := server.providerLifecycle.Active(descriptor.PluginID)
	if !ok || active.ID != generationID {
		t.Fatalf("active generation = %#v, ok=%v", active, ok)
	}
	if server.providerGenerationAdapter(descriptor.PluginID, generationID) == nil {
		t.Fatal("restored generation did not reconnect to the live provider")
	}
	server.providerMu.RLock()
	manifest, ok := server.providerManifests[descriptor.PluginID]
	server.providerMu.RUnlock()
	if !ok || manifest.Services[0].CapabilityID != capabilitycontract.Kubernetes {
		t.Fatalf("restored manifest = %#v, want current live manifest", manifest)
	}

	refreshed, err := state.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer refreshed.Close()
	records, err := refreshed.ListProviderGenerations()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("provider generation records = %d, want 1", len(records))
	}
	currentHash, err := hashProviderValue(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if records[0].ManifestHash != currentHash || records[0].ManifestHash == "sha256:stale-manifest" {
		t.Fatalf("durable manifest hash = %q, want refreshed hash %q", records[0].ManifestHash, currentHash)
	}
	if records[0].CatalogRevision != server.CatalogSnapshot().Revision {
		t.Fatalf("durable catalog revision = %q, want %q", records[0].CatalogRevision, server.CatalogSnapshot().Revision)
	}
	if _, err := server.providerGenerationAdapter(descriptor.PluginID, generationID).CallSynchronousOnly(context.Background(), "opute.capability.boundary.probe", map[string]any{}); err != nil {
		t.Fatalf("reconnected provider call: %v", err)
	}
}
