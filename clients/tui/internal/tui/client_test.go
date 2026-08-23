package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

func TestTypedEntityFlowUsesCanonicalBindingAndCurrentCatalog(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-host", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{Name: "get_capability_catalog", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"providerId": "incus", "catalogRevision": "sha256:typed-flow", "tools": []any{}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "list_vms", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"entities": []any{map[string]any{"name": "demo", "status": "running"}}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "get_vm_info", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"name": args["vmName"], "status": "running"}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	client, err := hostagentclient.Connect(context.Background(), httpServer.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	executor := NewExecutor(client)
	if err := executor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	entities, err := executor.ListVMs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 || entities[0].Name != "demo" {
		t.Fatalf("entities = %+v", entities)
	}
	info, err := executor.GetVMInfo(context.Background(), EntityBinding{EntityKind: "vm", EntityName: entities[0].Name, CatalogRevision: executor.Catalog.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if info["name"] != "demo" {
		t.Fatalf("info = %+v", info)
	}
}

func TestTypedEntityFlowRejectsStaleBinding(t *testing.T) {
	executor := &Executor{Catalog: hostagentclient.CatalogSnapshot{Revision: "sha256:current"}}
	if _, err := executor.GetVMInfo(context.Background(), EntityBinding{EntityKind: "vm", EntityName: "demo", CatalogRevision: "sha256:stale"}); err == nil {
		t.Fatal("stale binding was accepted")
	}
}

func TestTypedDraftUsesCatalogSchemaAndPreservesProvenance(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-host", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{Name: "get_capability_catalog", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{
			"providerId": "incus", "catalogRevision": "sha256:typed-draft",
			"tools": []any{map[string]any{
				"operationId": "get_vm_info", "name": "get_vm_info", "effect": "read",
				"inputSchema": map[string]any{"type": "object", "required": []any{"vmName"}},
			}},
		}}, nil
	})
	var calls int
	server.AddTool(&mcp.Tool{Name: "get_vm_info", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls++
		var args map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"name": args["vmName"]}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	client, err := hostagentclient.Connect(context.Background(), httpServer.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	executor := NewExecutor(client)
	if err := executor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	receipt, err := executor.ExecuteDraft(context.Background(), CommandDraft{
		Operation: "get_vm_info", CatalogRevision: "sha256:typed-draft",
		Arguments: map[string]any{"vmName": "demo"},
		Bindings:  []EntityBinding{{Operation: "get_vm_info", Argument: "vmName", EntityKind: "vm", EntityName: "demo", CatalogRevision: "sha256:typed-draft"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || receipt.CatalogRevision != "sha256:typed-draft" || len(receipt.Bindings) != 1 {
		t.Fatalf("calls=%d receipt=%+v", calls, receipt)
	}
	if receipt.Arguments["vmName"] != "demo" {
		t.Fatalf("arguments=%+v", receipt.Arguments)
	}
	if _, err := executor.ExecuteDraft(context.Background(), CommandDraft{Operation: "get_vm_info", CatalogRevision: "sha256:typed-draft", Arguments: map[string]any{}}); err == nil {
		t.Fatal("draft without required vmName was accepted")
	}
	t.Log("TUI_TYPED_EXECUTION_PASS")
}

func TestParserDoesNotInferProseAndSupportsQuotedValues(t *testing.T) {
	command, err := ParseCommand(`get_vm_info vmName="demo one" fast=true`)
	if err != nil {
		t.Fatal(err)
	}
	if command.Operation != "get_vm_info" || command.Arguments["vmName"].Typed != "demo one" || command.Arguments["fast"].Typed != true {
		t.Fatalf("command=%+v", command)
	}
	if _, err := ParseCommand("tell me about demo"); err == nil {
		t.Fatal("prose was accepted as a typed command")
	}
}
