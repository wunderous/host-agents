package tui

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/catalog"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/transport"
)

func TestWaitOperationPollsDurableTaskToCompletion(t *testing.T) {
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	host, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID: "incus", Ops: svc, Standalone: true, StateDir: t.TempDir(), AllowMutations: false,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })

	operationName := "test_async_read"
	previous, hadPrevious := tasks.TaskAwareTools[operationName]
	tasks.TaskAwareTools[operationName] = true
	t.Cleanup(func() {
		if hadPrevious {
			tasks.TaskAwareTools[operationName] = previous
		} else {
			delete(tasks.TaskAwareTools, operationName)
		}
	})
	if err := host.RegisterCapability(catalog.Registration{
		Descriptor: tools.CapabilityDescriptor{
			OperationID: operationName, Name: operationName, Description: "test-only async read", Effect: "read", Provider: "incus", Implementation: "test", ResourceKinds: []string{"host"}, Idempotent: true,
			InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
		},
		ProviderID: "incus", Implementation: "test",
	}, func(context.Context, map[string]any) (*mcp.CallToolResult, error) {
		time.Sleep(30 * time.Millisecond)
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ready": true}}, nil
	}); err != nil {
		t.Fatalf("register async capability: %v", err)
	}

	httpServer := transport.NewHTTPServer(transport.HTTPOptions{HostServer: host, BindHost: "127.0.0.1", Port: 0})
	backend := httptest.NewServer(httpServer.Handler())
	t.Cleanup(backend.Close)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	reverseProxy := httputil.NewSingleHostReverseProxy(target)
	var droppedStatusRequest int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				http.Error(writer, readErr.Error(), http.StatusBadRequest)
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			if strings.Contains(string(body), `"name":"get_operation"`) && atomic.CompareAndSwapInt32(&droppedStatusRequest, 0, 1) {
				hijacker, ok := writer.(http.Hijacker)
				if !ok {
					http.Error(writer, "proxy cannot simulate transport drop", http.StatusInternalServerError)
					return
				}
				connection, _, hijackErr := hijacker.Hijack()
				if hijackErr == nil {
					_ = connection.Close()
				}
				return
			}
		}
		reverseProxy.ServeHTTP(writer, request)
	}))
	t.Cleanup(proxy.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Connect(ctx, proxy.URL+"/mcp", "")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	started, err := client.Call(ctx, operationName, map[string]any{})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	operationID, ok := resultIdentifier(started, "taskId")
	if !ok {
		t.Fatalf("started result has no task id: %#v", started)
	}
	var statuses []string
	final, err := client.WaitOperation(ctx, operationID, PollOptions{Timeout: 3 * time.Second, Interval: 5 * time.Millisecond, OnUpdate: func(snapshot PollSnapshot) {
		statuses = append(statuses, snapshot.Status)
	}})
	if err != nil {
		t.Fatalf("wait operation: %v", err)
	}
	value, err := structuredMap(final)
	if err != nil || value["ready"] != true {
		t.Fatalf("final operation result = %#v err=%v", value, err)
	}
	if len(statuses) < 2 || statuses[0] != "working" || statuses[len(statuses)-1] != "completed" {
		t.Fatalf("operation statuses = %#v", statuses)
	}
	if atomic.LoadInt32(&droppedStatusRequest) != 1 {
		t.Fatal("status poll did not exercise the transport reconnect path")
	}
}
