package hostagentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
)

func TestClientUsesPublicMCPBoundaryAndReconnects(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-host", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{Name: "get_capability_catalog", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: CatalogSnapshot{ProviderID: "incus", Revision: "sha256:test", Tools: []CapabilityDescriptor{{Name: "list_vms", OperationID: "list_vms"}}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "list_vms", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"entities": []any{map[string]any{"name": "demo"}}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "get_vm_info", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if request == nil || request.Params == nil {
			return nil, fmt.Errorf("vmName is required")
		}
		var arguments map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, fmt.Errorf("decode arguments: %w", err)
		}
		if _, ok := arguments["vmName"]; !ok {
			return nil, fmt.Errorf("vmName is required")
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"name": "demo", "status": "running"}}, nil
	})
	handler := mcphttp.WrapProviderHandler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}), map[string]any{"name": "fake-host", "version": "1.0.0"})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client, err := Connect(context.Background(), httpServer.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	snapshot, err := client.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != "sha256:test" {
		t.Fatalf("catalog = %+v", snapshot)
	}
	result, err := client.Call(context.Background(), "list_vms", map[string]any{})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("list_vms = %#v/%v", result, err)
	}
	result, err = client.Call(context.Background(), "get_vm_info", map[string]any{"vmName": "demo"})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("get_vm_info = %#v/%v", result, err)
	}
	if err := client.Reconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
}
