package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDetachedTUIExecutesTypedCommandAgainstCatalogWithoutLLM(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-host-agent", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{Name: "get_capability_catalog", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{
			"providerId": "incus", "catalogRevision": "sha256:tui-e2e",
			"tools": []any{
				map[string]any{"name": "list_vms", "operationId": "list_vms", "effect": "read", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "get_vm_info", "operationId": "get_vm_info", "effect": "read", "inputSchema": map[string]any{"type": "object", "required": []any{"vmName"}}},
			},
		}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "list_vms", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"entities": []any{map[string]any{"name": "demo", "status": "running"}}}}, nil
	})
	var calls int
	server.AddTool(&mcp.Tool{Name: "get_vm_info", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls++
		var arguments map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"name": arguments["vmName"], "status": "running"}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	serverHTTP := httptest.NewServer(handler)
	defer serverHTTP.Close()

	var output bytes.Buffer
	err := Run(context.Background(), Config{
		Endpoint: serverHTTP.URL,
		In:       strings.NewReader("/context\nget_vm_info vmName=@vm:demo fast=true\n/exit\n"),
		Out:      &output,
		NoPrompt: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("get_vm_info calls = %d, want exactly one", calls)
	}
	text := output.String()
	for _, expected := range []string{"opute-host-agent connected", "deterministic mode is ready", "catalog revision: sha256:tui-e2e", `"name":"demo"`, "bye"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output missing %q:\n%s", expected, text)
		}
	}
	t.Log("TUI_TYPED_EXECUTION_PASS")
}
