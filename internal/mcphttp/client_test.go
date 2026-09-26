package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCallToolCancelsChildTaskWhenCallerContextEnds(t *testing.T) {
	getObserved := make(chan struct{}, 1)
	cancelObserved := make(chan map[string]any, 1)
	var callsMu sync.Mutex
	calls := make([]map[string]any, 0, 3)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		callsMu.Lock()
		calls = append(calls, request)
		callsMu.Unlock()
		method, _ := request["method"].(string)
		params, _ := request["params"].(map[string]any)
		var result map[string]any
		switch method {
		case "tools/call":
			result = map[string]any{"resultType": "task", "taskId": "child-task", "pollIntervalMs": 60_000}
		case "tasks/get":
			result = map[string]any{"taskId": "child-task", "status": "working"}
		case "tasks/cancel":
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("cancel authorization = %q, want original client token", got)
			}
			cancelObserved <- params
			result = map[string]any{"resultType": "complete"}
		default:
			t.Errorf("unexpected MCP method %q", method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result}); err != nil {
			t.Errorf("encode MCP response: %v", err)
			return
		}
		if method == "tasks/get" {
			getObserved <- struct{}{}
		}
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, Token: "test-token"}
	ctx, cancel := context.WithCancel(WithResourceDelegation(context.Background(), "signed-delegation"))
	result := make(chan error, 1)
	go func() {
		_, err := client.CallTool(ctx, "apply_manifest", map[string]any{"manifest": "redacted"})
		result <- err
	}()

	select {
	case <-getObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not poll the child task")
	}
	cancel()

	select {
	case params := <-cancelObserved:
		if params["taskId"] != "child-task" {
			t.Fatalf("cancel task id = %#v, want child-task", params["taskId"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not send tasks/cancel after caller cancellation")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CallTool error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool did not return after task cancellation")
	}

	callsMu.Lock()
	defer callsMu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("MCP method call count = %d, want tools/call, tasks/get, tasks/cancel", len(calls))
	}
	cancelParams, _ := calls[2]["params"].(map[string]any)
	meta, _ := cancelParams["_meta"].(map[string]any)
	if meta[ResourceDelegationMetaKey] != "signed-delegation" {
		t.Fatalf("cancellation request delegation = %#v, want preserved signed delegation", meta[ResourceDelegationMetaKey])
	}
	if calls[2]["method"] != "tasks/cancel" {
		t.Fatalf("last MCP method = %#v, want tasks/cancel", calls[2]["method"])
	}
}

func TestCallToolCancelsChildTaskAfterPollingError(t *testing.T) {
	cancelObserved := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		method, _ := request["method"].(string)
		switch method {
		case "tools/call":
			writeClientTestMCPResult(t, w, request, map[string]any{"resultType": "task", "taskId": "child-task", "pollIntervalMs": 1})
		case "tasks/get":
			http.Error(w, "temporary task status failure", http.StatusServiceUnavailable)
		case "tasks/cancel":
			cancelObserved <- struct{}{}
			writeClientTestMCPResult(t, w, request, map[string]any{"resultType": "complete"})
		default:
			t.Errorf("unexpected MCP method %q", method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, Token: "test-token"}
	_, err := client.CallTool(context.Background(), "apply_manifest", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "MCP HTTP 503") {
		t.Fatalf("CallTool error = %v, want the task polling HTTP 503", err)
	}
	select {
	case <-cancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not cancel the child task after a polling transport error")
	}
}

func TestCallToolCancelsChildTaskWhenItRequiresUnsupportedInput(t *testing.T) {
	cancelObserved := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		method, _ := request["method"].(string)
		switch method {
		case "tools/call":
			writeClientTestMCPResult(t, w, request, map[string]any{"resultType": "task", "taskId": "child-task", "pollIntervalMs": 1})
		case "tasks/get":
			writeClientTestMCPResult(t, w, request, map[string]any{"taskId": "child-task", "status": "input_required"})
		case "tasks/cancel":
			cancelObserved <- struct{}{}
			writeClientTestMCPResult(t, w, request, map[string]any{"resultType": "complete"})
		default:
			t.Errorf("unexpected MCP method %q", method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, Token: "test-token"}
	_, err := client.CallTool(context.Background(), "apply_manifest", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "input_required") {
		t.Fatalf("CallTool error = %v, want input_required", err)
	}
	select {
	case <-cancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not cancel the child task after unsupported input was requested")
	}
}

func writeClientTestMCPResult(t *testing.T, w http.ResponseWriter, request, result map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result}); err != nil {
		t.Errorf("encode MCP response: %v", err)
	}
}
