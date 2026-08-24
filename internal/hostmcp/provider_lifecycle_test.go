package hostmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/tools"
)

// listPublishedTools connects an in-memory MCP client and returns the names
// the server actually publishes on tools/list.
func listPublishedTools(t *testing.T, server *Server) []string {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server transport: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "lifecycle-test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()
	listResult, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make([]string, 0, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func fakeProviderManifest(operationID string) providercontract.InstallManifest {
	return providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: "com.opute.fake", Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{{
			ID: "com.opute.fake.service", Version: 1,
			Operations: []providercontract.Operation{{
				ID: operationID,
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"uri": map[string]any{"type": "string"}},
				},
				OutputSchema: map[string]any{"type": "object"},
				Effect:       "read", Idempotent: true,
				Requires: []providercontract.ResourceBinding{{Argument: "uri", ResourceType: "vm"}},
			}},
		}},
	}
}

// TestRetiredProviderGenerationRejectsNewCallsAndUnpublishesTools proves a
// retired generation both fails closed on dispatch and disappears from the
// advertised tools/list instead of lingering as a dead advertisement.
func TestRetiredProviderGenerationRejectsNewCallsAndUnpublishesTools(t *testing.T) {
	server, _ := newBindingTestServer(t)
	manifest := fakeProviderManifest("opute.capability.fake.probe")
	if err := server.registerProviderServices(manifest); err != nil {
		t.Fatal(err)
	}
	publishedBefore := false
	for _, name := range listPublishedTools(t, server) {
		if name == "opute.capability.fake.probe" {
			publishedBefore = true
		}
	}
	if !publishedBefore {
		t.Fatal("provider operation was not published before retirement")
	}
	result, err := server.DispatchTool(context.Background(), "opute.capability.fake.probe", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("dispatch before retirement returned protocol error: %v", err)
	}
	// The fake provider has no connected adapter; dispatch must fail as a
	// typed tool error rather than succeed silently.
	if result == nil || !result.IsError {
		t.Fatalf("dispatch without provider adapter = %#v", result)
	}

	server.retireProviderCapabilities("com.opute.fake", "")
	server.catalogMu.RLock()
	_, stillRegistered := server.capabilities["opute.capability.fake.probe"]
	server.catalogMu.RUnlock()
	if stillRegistered {
		t.Fatal("retired capability still registered")
	}
	for _, descriptor := range server.CatalogSnapshot().Tools {
		if descriptor.OperationID == "opute.capability.fake.probe" {
			t.Fatal("retired operation still in catalog snapshot")
		}
	}
	for _, name := range listPublishedTools(t, server) {
		if name == "opute.capability.fake.probe" {
			t.Fatal("retired operation still advertised on tools/list")
		}
	}
	if _, err := server.DispatchTool(context.Background(), "opute.capability.fake.probe", map[string]any{}, nil); err == nil {
		t.Fatal("retired dispatch should fail closed")
	}
}

// TestProviderWireSchemaMatchesDescriptor proves the dynamically published
// MCP tool schema is structurally identical to the manifest operation schema
// the catalog descriptor was built from — one schema source, no drift.
func TestProviderWireSchemaMatchesDescriptor(t *testing.T) {
	server, _ := newBindingTestServer(t)
	manifest := fakeProviderManifest("opute.capability.fake.parity")
	if err := server.registerProviderServices(manifest); err != nil {
		t.Fatal(err)
	}
	operation := manifest.Services[0].Operations[0]
	var descriptor tools.CapabilityDescriptor
	for _, candidate := range server.CatalogSnapshot().Tools {
		if candidate.OperationID == operation.ID {
			descriptor = candidate
			break
		}
	}
	if descriptor.OperationID == "" {
		t.Fatal("provider operation was not published to the catalog")
	}
	descriptorJSON, _ := json.Marshal(descriptor.InputSchema)
	operationJSON, _ := json.Marshal(operation.InputSchema)
	if string(descriptorJSON) != string(operationJSON) {
		t.Fatalf("descriptor schema drifted from manifest schema:\n descriptor=%s\n manifest=%s", descriptorJSON, operationJSON)
	}
	if descriptor.Requires[0].ResourceType != operation.Requires[0].ResourceType {
		t.Fatalf("declared bindings drifted: %#v vs %#v", descriptor.Requires, operation.Requires)
	}
	// Wire parity: the published MCP tool schema must equal the descriptor.
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "parity-test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	listResult, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listResult.Tools {
		if tool.Name != operation.ID {
			continue
		}
		wireJSON, _ := json.Marshal(tool.InputSchema)
		if string(wireJSON) != string(operationJSON) {
			t.Fatalf("wire schema drifted from manifest schema:\n wire=%s\n manifest=%s", wireJSON, operationJSON)
		}
		return
	}
	t.Fatal("provider operation missing from tools/list")
}

func TestProviderRefreshUnpublishesOperationsRemovedFromManifest(t *testing.T) {
	server, _ := newBindingTestServer(t)
	first := fakeProviderManifest("opute.capability.fake.keep")
	removed := first.Services[0].Operations[0]
	removed.ID = "opute.capability.fake.remove"
	first.Services[0].Operations = append(first.Services[0].Operations, removed)
	if err := server.registerProviderServices(first); err != nil {
		t.Fatal(err)
	}
	second := fakeProviderManifest("opute.capability.fake.keep")
	if err := server.registerProviderServices(second); err != nil {
		t.Fatal(err)
	}
	server.catalogMu.RLock()
	_, stillRegistered := server.capabilities["opute.capability.fake.remove"]
	server.catalogMu.RUnlock()
	if stillRegistered {
		t.Fatal("operation removed from the provider manifest remained executable")
	}
	for _, name := range listPublishedTools(t, server) {
		if name == "opute.capability.fake.remove" {
			t.Fatal("operation removed from the provider manifest remained published")
		}
	}
}
