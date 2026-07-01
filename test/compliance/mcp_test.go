package compliance_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opute-io/host-agents/internal/hostmcp"
	"github.com/opute-io/host-agents/internal/ops"
	"github.com/opute-io/host-agents/internal/provider"
	"github.com/opute-io/host-agents/internal/tools"
	"github.com/opute-io/host-agents/internal/transport"
)

func newTestServer(t *testing.T) *hostmcp.Server {
	t.Helper()
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
	hs, err := hostmcp.NewServer(hostmcp.Options{ProviderID: "incus", Ops: svc})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return hs
}

func TestMCPInitializeAndGetHostInfo(t *testing.T) {
	hs := newTestServer(t)
	httpSrv := transport.NewHTTPServer(transport.HTTPOptions{
		HostServer: hs,
		BindHost:   "127.0.0.1",
		Port:       0,
	})
	ts := httptest.NewServer(httpSrv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "compliance-test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_host_info",
		Arguments: map[string]any{},
	})
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

func TestMCPTasksList(t *testing.T) {
	hs := newTestServer(t)
	result, err := hs.HandleExtensionMethod("tasks/list", nil)
	if err != nil {
		t.Fatalf("tasks/list: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	if _, ok := m["tasks"]; !ok {
		t.Fatalf("missing tasks key: %+v", m)
	}
}

func TestMCPResourcesList(t *testing.T) {
	hs := newTestServer(t)
	result, err := hs.HandleExtensionMethod("resources/list", nil)
	if err != nil {
		t.Fatalf("resources/list: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	if _, ok := m["resources"]; !ok {
		t.Fatalf("missing resources key: %+v", m)
	}
}
