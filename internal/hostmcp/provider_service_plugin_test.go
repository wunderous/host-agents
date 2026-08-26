package hostmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
)

// mountTestAdapter connects an adapter to a provider that answers one probe
// operation, so a test can tell a live adapter from a closed one.
func mountTestAdapter(t *testing.T, providerID string) *provideradapter.Adapter {
	t.Helper()
	provider := mcp.NewServer(&mcp.Implementation{Name: providerID, Version: "1.0.0"}, nil)
	provider.AddTool(&mcp.Tool{Name: "probe", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	})
	provider.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: providercontract.InstallManifest{
			Schema:   providercontract.InstallManifestVersion,
			Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return provider }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	adapter, err := provideradapter.Connect(context.Background(), providercontract.PluginDescriptor{
		Schema:   providercontract.PluginDescriptorVersion,
		PluginID: providerID,
		Version:  "1.0.0",
		Capabilities: []providercontract.CapabilityRef{{
			ID: "opute.capability.mount-test.v1", Version: 1,
		}},
		Server: providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: httpServer.URL},
	}, provideradapter.Options{})
	if err != nil {
		t.Fatalf("connect provider adapter: %v", err)
	}
	return adapter
}

func service(id, capabilityID string, dependencies ...string) providercontract.ServiceDefinition {
	definition := providercontract.ServiceDefinition{ID: id, CapabilityID: capabilityID, Version: 1}
	for _, dependency := range dependencies {
		definition.Dependencies = append(definition.Dependencies, providercontract.CapabilityRef{ID: dependency, Version: 1})
	}
	return definition
}

func generationPluginIDs(server *Server, generationID string) []string {
	suffix := "@" + generationID
	ids := make([]string, 0)
	for _, id := range server.providerContext.PluginIDs() {
		if strings.HasSuffix(id, suffix) {
			ids = append(ids, id)
		}
	}
	return ids
}

func TestUnmountProviderGenerationLeavesNoServiceOrAdapterBehind(t *testing.T) {
	server, _ := newBindingTestServer(t)
	const providerID = "com.opute.mount-test"
	const generationID = providerID + "-1"
	adapter := mountTestAdapter(t, providerID)

	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{
			service("base", "opute.capability.mount-base.v1"),
			service("middle", "opute.capability.mount-middle.v1", "opute.capability.mount-base.v1"),
			service("leaf", "opute.capability.mount-leaf.v1", "opute.capability.mount-middle.v1"),
		},
	}
	if err := server.mountProviderGeneration(manifest, generationID, adapter); err != nil {
		t.Fatalf("mount provider generation: %v", err)
	}
	if got := len(generationPluginIDs(server, generationID)); got != 3 {
		t.Fatalf("mounted plugin count = %d, want 3", got)
	}
	if _, err := adapter.CallSynchronousOnly(context.Background(), "probe", map[string]any{}); err != nil {
		t.Fatalf("adapter unusable while mounted: %v", err)
	}

	if err := server.unmountProviderGeneration(providerID, generationID); err != nil {
		t.Fatalf("unmount provider generation: %v", err)
	}
	if ids := generationPluginIDs(server, generationID); len(ids) != 0 {
		t.Fatalf("plugins survived unmount: %v", ids)
	}
	for _, service := range manifest.Services {
		if _, ok := server.providerServiceValueFor(providerID, service.ID, generationID); ok {
			t.Fatalf("service %q is still resolvable after unmount", service.ID)
		}
	}
	if server.providerGenerationAdapter(providerID, generationID) != nil {
		t.Fatal("generation adapter is still reachable after unmount")
	}
	// The mount owned the adapter, so disposal must have closed the session
	// rather than leaving an orphaned connection to the provider process.
	if _, err := adapter.CallSynchronousOnly(context.Background(), "probe", map[string]any{}); err == nil {
		t.Fatal("adapter remained open after its owning fiber was disposed")
	}
}

func TestMountProviderGenerationOrdersServicesByDependency(t *testing.T) {
	server, _ := newBindingTestServer(t)
	const providerID = "com.opute.inject-test"
	const generationID = providerID + "-1"
	adapter := mountTestAdapter(t, providerID)

	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{
			service("base", "opute.capability.inject-base.v1"),
			service("dependent", "opute.capability.inject-dependent.v1", "opute.capability.inject-base.v1"),
		},
	}
	if err := server.mountProviderGeneration(manifest, generationID, adapter); err != nil {
		t.Fatalf("mount provider generation: %v", err)
	}
	ids := generationPluginIDs(server, generationID)
	if len(ids) != 2 || !strings.HasPrefix(ids[0], providerID+"/base@") {
		t.Fatalf("mount order = %v, want base before dependent", ids)
	}
	dependent, ok := server.providerServiceValueFor(providerID, "dependent", generationID)
	if !ok {
		t.Fatal("dependent service was not mounted")
	}
	if len(dependent.inject) != 1 || !strings.HasPrefix(string(dependent.inject[0]), providerID+"/base@") {
		t.Fatalf("dependent injects %v, want the base service key", dependent.inject)
	}
}

func TestMountProviderGenerationFailsClosedOnUnsatisfiableDependency(t *testing.T) {
	server, _ := newBindingTestServer(t)
	const providerID = "com.opute.unsatisfiable-test"
	const generationID = providerID + "-1"
	adapter := mountTestAdapter(t, providerID)
	defer adapter.Close()

	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{
			service("base", "opute.capability.unsatisfiable-base.v1"),
			service("dependent", "opute.capability.unsatisfiable-dependent.v1", "opute.capability.never-provided.v1"),
		},
	}
	if err := server.mountProviderGeneration(manifest, generationID, adapter); err == nil {
		t.Fatal("mount succeeded despite an unsatisfiable dependency")
	}
	// A partial mount is not a valid state: the services that did mount before
	// the failure must be gone, not left serving half a generation.
	if ids := generationPluginIDs(server, generationID); len(ids) != 0 {
		t.Fatalf("partial mount survived the failure: %v", ids)
	}
}

// activateGeneration drives the lifecycle manager through the real
// candidate → ready → active transitions and mounts the result, so the swap
// tests exercise the same state injectableServiceFamilies reads.
func activateGeneration(t *testing.T, server *Server, manifest providercontract.InstallManifest, endpoint string) (string, *provideradapter.Adapter) {
	t.Helper()
	providerID := manifest.Provider.ID
	candidate, err := server.providerLifecycle.CreateCandidate(manifest.Provider, "manifest-hash", endpoint, "catalog")
	if err != nil {
		t.Fatalf("create candidate for %q: %v", providerID, err)
	}
	if err := server.providerLifecycle.MarkReady(candidate.ID); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	previous, activated, err := server.providerLifecycle.Activate(candidate.ID)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	adapter := mountTestAdapter(t, providerID)
	// Mount the replacement before disposing the predecessor, exactly as
	// activateProviderGeneration does.
	if err := server.mountProviderGeneration(manifest, activated.ID, adapter); err != nil {
		t.Fatalf("mount generation %q: %v", activated.ID, err)
	}
	if previous != nil {
		if err := server.unmountProviderGeneration(previous.Provider.ID, previous.ID); err != nil {
			t.Fatalf("unmount superseded generation %q: %v", previous.ID, err)
		}
	}
	return activated.ID, adapter
}

func TestProviderSwapLeavesExactlyOneMountedGeneration(t *testing.T) {
	server, _ := newBindingTestServer(t)
	server.providerLifecycle = cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	const providerID = "com.opute.swap-test"
	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{
			service("primary", "opute.capability.swap.v1"),
			service("secondary", "opute.capability.swap-aux.v1"),
		},
	}

	// Three activations, so the test would catch an accumulation that a single
	// swap could hide.
	adapters := make([]*provideradapter.Adapter, 0, 3)
	generations := make([]string, 0, 3)
	for round := 0; round < 3; round++ {
		generationID, adapter := activateGeneration(t, server, manifest, "http://provider.invalid")
		generations = append(generations, generationID)
		adapters = append(adapters, adapter)

		mounted := generationPluginIDs(server, generationID)
		if len(mounted) != 2 {
			t.Fatalf("round %d: generation %q has %d mounted services, want 2", round, generationID, len(mounted))
		}
	}

	// Exactly one generation survives, and it is the newest.
	live := 0
	for _, id := range server.providerContext.PluginIDs() {
		if strings.HasPrefix(id, providerID+"/") {
			live++
			if !strings.HasSuffix(id, "@"+generations[len(generations)-1]) {
				t.Fatalf("a superseded generation is still mounted: %q", id)
			}
		}
	}
	if live != 2 {
		t.Fatalf("%d provider services mounted after three activations, want 2", live)
	}

	// Exactly one connection survives: every predecessor's adapter is closed.
	for index, adapter := range adapters {
		_, err := adapter.CallSynchronousOnly(context.Background(), "probe", map[string]any{})
		last := index == len(adapters)-1
		if last && err != nil {
			t.Fatalf("the active generation's adapter is closed: %v", err)
		}
		if !last && err == nil {
			t.Fatalf("generation %q leaked an open adapter after being superseded", generations[index])
		}
	}
}

func TestProviderSwapKeepsDependenciesResolvable(t *testing.T) {
	server, _ := newBindingTestServer(t)
	server.providerLifecycle = cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	const baseID = "com.opute.swap-base"
	const dependentID = "com.opute.swap-dependent"
	baseManifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: baseID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{service("svc", "opute.capability.swap-base.v1")},
	}

	first, _ := activateGeneration(t, server, baseManifest, "http://base.invalid")
	second, _ := activateGeneration(t, server, baseManifest, "http://base.invalid")
	if first == second {
		t.Fatal("the two activations produced the same generation")
	}

	// This is the case that failed before the swap disposal landed: with two
	// generations of one family mounted, the family was ambiguous and every
	// dependent mount failed closed.
	dependentManifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: dependentID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{
			service("svc", "opute.capability.swap-dependent.v1", "opute.capability.swap-base.v1"),
		},
	}
	dependentGeneration, _ := activateGeneration(t, server, dependentManifest, "http://dependent.invalid")

	value, ok := server.providerServiceValueFor(dependentID, "svc", dependentGeneration)
	if !ok {
		t.Fatal("dependent service was not mounted")
	}
	if len(value.inject) != 1 {
		t.Fatalf("dependent injects %v, want exactly one service key", value.inject)
	}
	// It must bind to the surviving generation, never to the superseded one.
	if !strings.HasSuffix(string(value.inject[0]), "@"+second) {
		t.Fatalf("dependent injected %q, want the active generation %q", value.inject[0], second)
	}
}

func TestInjectableFamiliesExcludeSupersededGenerations(t *testing.T) {
	server, _ := newBindingTestServer(t)
	server.providerLifecycle = cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	const providerID = "com.opute.scope-test"
	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{service("svc", "opute.capability.scope.v1")},
	}

	// Mount two generations and, unlike the swap path, leave both mounted —
	// this is the overlap window a replacement is brought up in.
	candidate, err := server.providerLifecycle.CreateCandidate(manifest.Provider, "hash", "http://scope.invalid", "catalog")
	if err != nil {
		t.Fatalf("create first candidate: %v", err)
	}
	if err := server.providerLifecycle.MarkReady(candidate.ID); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if _, _, err := server.providerLifecycle.Activate(candidate.ID); err != nil {
		t.Fatalf("activate first: %v", err)
	}
	if err := server.mountProviderGeneration(manifest, candidate.ID, mountTestAdapter(t, providerID)); err != nil {
		t.Fatalf("mount first: %v", err)
	}
	replacement, err := server.providerLifecycle.CreateCandidate(manifest.Provider, "hash", "http://scope.invalid", "catalog")
	if err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	if err := server.providerLifecycle.MarkReady(replacement.ID); err != nil {
		t.Fatalf("mark ready replacement: %v", err)
	}
	if _, _, err := server.providerLifecycle.Activate(replacement.ID); err != nil {
		t.Fatalf("activate replacement: %v", err)
	}
	if err := server.mountProviderGeneration(manifest, replacement.ID, mountTestAdapter(t, providerID)); err != nil {
		t.Fatalf("mount replacement: %v", err)
	}
	if got := len(generationPluginIDs(server, candidate.ID)); got != 1 {
		t.Fatalf("superseded generation is not mounted; the overlap window is not being tested")
	}

	// A third party planning a mount now must see the family exactly once.
	families := server.injectableServiceFamilies("com.opute.unrelated-1")
	matches := 0
	for key, family := range families {
		if family == "opute.capability.scope.v1" {
			matches++
			if !strings.HasSuffix(string(key), "@"+replacement.ID) {
				t.Fatalf("injectable family resolved to %q, want the active generation", key)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("family is injectable from %d generations during the overlap window, want 1", matches)
	}
}

func TestRestoredGenerationIsInjectable(t *testing.T) {
	server, _ := newBindingTestServer(t)
	server.providerLifecycle = cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	const providerID = "com.opute.restore-test"
	const generationID = providerID + "-1"

	// Injection scope is decided against lifecycle-active state, so restoring a
	// generation must make it active before it is mounted. This is the coupling
	// between ProviderLifecycleManager.Restore and the mount pass in
	// restoreProviderGenerations; without it, a restart would leave every
	// restored provider mounted but uninjectable.
	if err := server.providerLifecycle.Restore(cordis.ProviderGeneration{
		ID:           generationID,
		Provider:     providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		ManifestHash: "hash",
		Endpoint:     "http://restore.invalid",
		State:        cordis.GenerationActive,
	}); err != nil {
		t.Fatalf("restore generation: %v", err)
	}
	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{service("svc", "opute.capability.restore.v1")},
	}
	if err := server.mountProviderGeneration(manifest, generationID, mountTestAdapter(t, providerID)); err != nil {
		t.Fatalf("mount restored generation: %v", err)
	}

	families := server.injectableServiceFamilies("com.opute.unrelated-1")
	found := false
	for _, family := range families {
		if family == "opute.capability.restore.v1" {
			found = true
		}
	}
	if !found {
		t.Fatal("a restored active generation is mounted but not injectable")
	}
}
