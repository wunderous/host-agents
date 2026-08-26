package hostmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
)

func TestProviderTeardownFinalizationFailureLeavesGenerationRetryable(t *testing.T) {
	server, _ := newBindingTestServer(t)
	const providerID = "com.opute.teardown-test"
	const generationID = providerID + "-1"

	var finalizeCalls atomic.Int32
	provider := mcp.NewServer(&mcp.Implementation{Name: "teardown-test-provider", Version: "1.0.0"}, nil)
	provider.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: providercontract.InstallManifest{
			Schema:   providercontract.InstallManifestVersion,
			Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		}}, nil
	})
	provider.AddTool(&mcp.Tool{Name: providerTeardownOperation, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		if request != nil && request.Params != nil {
			_ = json.Unmarshal(request.Params.Arguments, &args)
		}
		if args["phase"] != "finalize" {
			return &mcp.CallToolResult{StructuredContent: map[string]any{
				"contractVersion": "host-plan.v1",
				"plan":            map[string]any{"contractVersion": "host-plan.v1"},
			}}, nil
		}
		inputs, _ := args["inputs"].(map[string]any)
		if inputs["phase"] != "finalize" {
			return &mcp.CallToolResult{IsError: true}, nil
		}
		if finalizeCalls.Add(1) == 1 {
			return &mcp.CallToolResult{IsError: true}, nil
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"completed": true}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return provider }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	descriptor := providercontract.PluginDescriptor{
		Schema:   providercontract.PluginDescriptorVersion,
		PluginID: providerID,
		Version:  "1.0.0",
		Capabilities: []providercontract.CapabilityRef{{
			ID: "opute.capability.teardown-test.v1", Version: 1,
		}},
		Server: providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: httpServer.URL},
	}
	adapter, err := provideradapter.Connect(context.Background(), descriptor, provideradapter.Options{})
	if err != nil {
		t.Fatalf("connect provider adapter: %v", err)
	}
	defer adapter.Close()

	manager := cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	server.providerLifecycle = manager
	generation, err := manager.CreateCandidate(providercontract.ProviderRef{ID: descriptor.PluginID, Version: descriptor.Version}, "manifest-hash", descriptor.Server.Endpoint, "catalog")
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	if generation.ID != generationID {
		t.Fatalf("generation ID = %q, want %q", generation.ID, generationID)
	}
	if err := manager.MarkReady(generation.ID); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if _, _, err := manager.Activate(generation.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	server.providerMu.Lock()
	server.providerAdapters[providerID] = adapter
	server.providerMu.Unlock()

	metadata := map[string]any{
		"providerId":             providerID,
		"providerGenerationId":   generation.ID,
		"providerTeardownInputs": map[string]any{"tunnelId": "disposable-tunnel"},
	}
	if err := server.completeProviderTeardown(metadata); err == nil {
		t.Fatal("first finalization unexpectedly succeeded")
	}
	if _, ok := manager.Active(providerID); !ok {
		t.Fatal("provider generation was retired after finalization failure")
	}
	server.providerMu.RLock()
	_, connected := server.providerAdapters[providerID]
	server.providerMu.RUnlock()
	if !connected {
		t.Fatal("provider adapter was removed after finalization failure")
	}

	if err := server.completeProviderTeardown(metadata); err != nil {
		t.Fatalf("retry finalization: %v", err)
	}
	if _, ok := manager.Active(providerID); ok {
		t.Fatal("provider generation remained active after successful retry")
	}
	stopped, ok := manager.Get(generation.ID)
	if !ok || stopped.State != cordis.GenerationStopped {
		t.Fatalf("generation after successful retry = %#v, found=%v", stopped, ok)
	}
	server.providerMu.RLock()
	_, connected = server.providerAdapters[providerID]
	server.providerMu.RUnlock()
	if connected {
		t.Fatal("provider adapter remained connected after successful retry")
	}
	if calls := finalizeCalls.Load(); calls != 2 {
		t.Fatalf("finalize calls = %d, want 2", calls)
	}
}
