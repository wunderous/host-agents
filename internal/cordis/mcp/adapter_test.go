package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

func TestConnectDiscoversToolsAndValidatesManifest(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "spoofable-name", Version: "0.0.1"}, nil)
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: providercontract.InstallManifest{
			Schema:     providercontract.InstallManifestVersion,
			Provider:   providercontract.ProviderRef{ID: "com.opute.example", Version: "1.0.0"},
			Provides:   []providercontract.CapabilityRef{{ID: "opute.capability.example.v1", Version: 1}},
			Recipes:    []providercontract.RecipeRef{{ID: "example", Source: providercontract.RecipeSource{URI: "https://example.invalid/recipe.yaml", Revision: "immutable", SHA256: "sha256:abc"}}},
			Validation: providercontract.ValidationRef{Capability: "opute.capability.example.v1", Operation: "validate"},
		}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	descriptor := providercontract.PluginDescriptor{
		Schema:   providercontract.PluginDescriptorVersion,
		PluginID: "com.opute.example", Version: "1.0.0",
		Capabilities: []providercontract.CapabilityRef{{ID: "opute.capability.example.v1", Version: 1}},
		Server:       providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: httpServer.URL},
	}
	adapter, err := Connect(context.Background(), descriptor, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	manifest, err := adapter.InstallManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Provider.ID != descriptor.PluginID || len(adapter.ToolNames()) != 1 {
		t.Fatalf("adapter state = %+v/%v", manifest, adapter.ToolNames())
	}
}

func TestConnectRejectsProviderWithoutManifestTool(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "not-trusted", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{Name: "other", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	descriptor := providercontract.PluginDescriptor{Schema: providercontract.PluginDescriptorVersion, PluginID: "com.opute.example", Version: "1.0.0", Capabilities: []providercontract.CapabilityRef{{ID: "opute.capability.example.v1", Version: 1}}, Server: providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: httpServer.URL}}
	if _, err := Connect(context.Background(), descriptor, Options{}); err == nil {
		t.Fatal("provider without manifest tool was accepted")
	}
}

func TestCallSynchronousOnlyRejectsProviderTaskResult(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "task-provider", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: providercontract.InstallManifest{
			Schema:     providercontract.InstallManifestVersion,
			Provider:   providercontract.ProviderRef{ID: "com.opute.example", Version: "1.0.0"},
			Provides:   []providercontract.CapabilityRef{{ID: "opute.capability.example.v1", Version: 1}},
			Recipes:    []providercontract.RecipeRef{{ID: "example", Source: providercontract.RecipeSource{URI: "https://example.invalid/recipe.yaml", Revision: "immutable", SHA256: "sha256:abc"}}},
			Validation: providercontract.ValidationRef{Capability: "opute.capability.example.v1", Operation: "validate"},
		}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "validate", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"resultType": "task", "taskId": "downstream-task"}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	adapter, err := Connect(context.Background(), providercontract.PluginDescriptor{
		Schema: providercontract.PluginDescriptorVersion, PluginID: "com.opute.example", Version: "1.0.0",
		Capabilities: []providercontract.CapabilityRef{{ID: "opute.capability.example.v1", Version: 1}},
		Server:       providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: httpServer.URL},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if _, err := adapter.CallSynchronousOnly(context.Background(), "validate", nil); err == nil {
		t.Fatal("provider task result was accepted by synchronous-only adapter")
	}
}
