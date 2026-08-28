package compliance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/authz"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/mcphttp"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/transport"
)

func newTestServer(t *testing.T) *hostmcp.Server {
	return newTestServerMode(t, false)
}

func newTestServerMode(t *testing.T, standalone bool) *hostmcp.Server {
	t.Helper()
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	hs, err := hostmcp.NewServer(hostmcp.Options{ProviderID: "incus", Ops: svc, Standalone: standalone, AllowMutations: standalone, StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return hs
}

func startAuthenticatedMCP(t *testing.T, hs *hostmcp.Server, token string) *httptest.Server {
	t.Helper()
	authorizer, err := authz.Open(authz.Options{StateDir: t.TempDir(), BootstrapToken: token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = authorizer.Close() })
	httpSrv := transport.NewHTTPServer(transport.HTTPOptions{
		HostServer: hs,
		BindHost:   "127.0.0.1",
		Port:       0,
		Authz:      authorizer,
	})
	ts := httptest.NewServer(httpSrv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestMCPInputRequiredTaskRoundTripOverHTTP(t *testing.T) {
	hs := newTestServerMode(t, true)
	const token = "host-bootstrap"
	ts := startAuthenticatedMCP(t, hs, token)
	meta, err := mcphttp.ModernRequestEnvelope("compliance")
	if err != nil {
		t.Fatal(err)
	}
	call := func(id int, method string, params map[string]any) map[string]any {
		t.Helper()
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if err := mcphttp.ApplyStreamableHTTPRequestHeaders(req); err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Mcp-Method", method)
		if method == "tools/call" {
			if err := mcphttp.ApplyToolsCallRequestHeaders(req, "request_task_input"); err != nil {
				t.Fatal(err)
			}
		} else if method == "tasks/get" || method == "tasks/update" {
			if taskID, ok := params["taskId"].(string); ok {
				req.Header.Set("Mcp-Name", taskID)
			}
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var envelope map[string]any
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if envelope["error"] != nil {
			t.Fatalf("%s error: %#v", method, envelope["error"])
		}
		result, ok := envelope["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s result = %#v", method, envelope)
		}
		return result
	}
	callParams := map[string]any{
		"name":      "request_task_input",
		"arguments": map[string]any{"prompt": "Continue?", "responseType": "boolean"},
		"_meta":     meta,
	}
	created := call(1, "tools/call", callParams)
	if created["resultType"] != "task" {
		t.Fatalf("task creation resultType = %#v, want task", created["resultType"])
	}
	taskID, ok := created["taskId"].(string)
	if !ok || taskID == "" {
		t.Fatalf("task creation result = %#v", created)
	}
	getParams := map[string]any{"taskId": taskID, "_meta": meta}
	get := call(2, "tasks/get", getParams)
	if get["status"] != "input_required" || get["inputRequests"] == nil {
		t.Fatalf("input-required task projection = %#v", get)
	}
	update := call(3, "tasks/update", map[string]any{"taskId": taskID, "inputResponses": map[string]any{"response": true}, "_meta": meta})
	if update["resultType"] != "complete" {
		t.Fatalf("tasks/update result = %#v, want resultType=complete", update)
	}
	completed := call(4, "tasks/get", getParams)
	if completed["status"] != "completed" || completed["resultType"] != "complete" {
		t.Fatalf("completed task projection = %#v", completed)
	}
	if _, ok := completed["result"].(map[string]any); !ok {
		t.Fatalf("completed task omitted inline result: %#v", completed)
	}
}

func TestMCPDiscoverAndGetHostInfo(t *testing.T) {
	hs := newTestServer(t)
	const token = "host-bootstrap"
	ts := startAuthenticatedMCP(t, hs, token)
	client := mcphttp.Client{Endpoint: ts.URL + "/mcp", Token: token, Name: "compliance-test", Version: "v1"}
	if _, err := client.Call(context.Background(), "server/discover", "", map[string]any{}); err != nil {
		t.Fatalf("discover: %v", err)
	}
	res, err := client.CallTool(context.Background(), "get_host_info", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_host_info failed: %+v", res)
	}
	if res.StructuredContent == nil {
		t.Fatal("expected structuredContent")
	}
}

func TestMCPAuthProtectsMCPButNotHealth(t *testing.T) {
	hs := newTestServer(t)
	const token = "test-token"
	ts := startAuthenticatedMCP(t, hs, token)

	health, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.StatusCode)
	}
	_ = health.Body.Close()

	discover, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
				"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "auth-test", "version": "1"},
			},
		},
	})
	request := func(token string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(discover)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "server/discover")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := request(""); got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP status = %d, want 401", got)
	}
	if got := request("wrong-token"); got != http.StatusUnauthorized {
		t.Fatalf("wrong-token MCP status = %d, want 401", got)
	}
	if got := request("test-token"); got != http.StatusOK {
		t.Fatalf("authenticated MCP status = %d, want 200", got)
	}
}

func TestMCPTasksListIsNotSupported(t *testing.T) {
	hs := newTestServer(t)
	if _, err := hs.HandleExtensionMethod("tasks/list", nil); err == nil {
		t.Fatal("tasks/list must not enumerate tasks in the Tasks extension")
	}
}

func TestMCPTasksGetServesInlineToolResult(t *testing.T) {
	hs := newTestServer(t)
	rec := hs.Tasks().Create("probe_tool", map[string]any{"vmName": "probe"}, 0, "Executing probe_tool...", nil)
	hs.Tasks().Complete(rec.TaskID, tasks.ToolResult{
		Content:           []map[string]any{{"type": "text", "text": "done"}},
		StructuredContent: map[string]any{"ready": true},
	})
	params, err := json.Marshal(map[string]string{"taskId": rec.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := hs.HandleExtensionMethod("tasks/get", params)
	if err != nil {
		t.Fatalf("tasks/get: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	inline, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing inline result: %+v", m)
	}
	if _, ok := inline["structuredContent"]; !ok {
		t.Fatalf("missing structuredContent in inline result: %+v", m)
	}
	if _, err := hs.HandleExtensionMethod("tasks/get", json.RawMessage(`{"taskId":"missing"}`)); err == nil {
		t.Fatal("expected task-not-found error for unknown task id")
	}
}

func TestMCPResourcesListIsNotAdvertised(t *testing.T) {
	hs := newTestServer(t)
	if _, err := hs.HandleExtensionMethod("resources/list", nil); err == nil {
		t.Fatal("resources/list must not be advertised on a modern-only server")
	}
}
