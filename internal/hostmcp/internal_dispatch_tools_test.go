package hostmcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/tools"
)

func newInternalDispatchTestServer(t *testing.T) *Server {
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
	server, err := NewServer(Options{
		ProviderID:     "incus",
		Ops:            svc,
		Standalone:     true,
		AllowMutations: true,
		StateDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func connectInternalDispatchSession(t *testing.T, server *Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "internal-dispatch-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// The catalog-excluded tools carry two obligations at once: they are hidden
// from tools/list and they remain callable through tools/call. Only the first
// was enforced, so the MCP server never registered the names and answered
// `unknown tool` — which is exactly how the platform's SQL hot path failed on
// ensure_sql_connector. Asserting the loader alone cannot catch that; the
// assertion has to run against a live session.
func TestCatalogExcludedToolsAreCallableButUnlisted(t *testing.T) {
	server := newInternalDispatchTestServer(t)
	session := connectInternalDispatchSession(t, server)

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listedNames := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		listedNames[tool.Name] = true
	}

	// Standalone mode deliberately republishes a few of the excluded names
	// (list_operations and friends) through StandaloneToolDefinitions, so the
	// invariant is about the tools this server hid, not about the whole
	// exclusion set: whatever registerInternalDispatchTools registered must
	// stay off tools/list.
	server.mu.Lock()
	hidden := make([]string, 0, len(server.internalToolNames))
	for name := range server.internalToolNames {
		hidden = append(hidden, name)
	}
	server.mu.Unlock()
	if len(hidden) == 0 {
		t.Fatal("no internal dispatch tools were registered")
	}
	for _, name := range hidden {
		if listedNames[name] {
			t.Fatalf("internal dispatch tool %q must not appear on tools/list", name)
		}
	}
	for _, name := range []string{"ensure_sql_connector", "get_sql_connector_status", "release_sql_connector", "install_sql_forward_sidecar"} {
		if listedNames[name] {
			t.Fatalf("catalog-excluded tool %q must not appear on tools/list", name)
		}
	}

	// A tool that is registered answers with its own validation or execution
	// failure. A tool that is not registered is rejected by the SDK before the
	// handler runs, and that rejection is the regression under test.
	for _, name := range []string{
		"ensure_sql_connector",
		"get_sql_connector_status",
		"release_sql_connector",
		"install_sql_forward_sidecar",
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      name,
			Arguments: map[string]any{},
		})
		if err != nil && strings.Contains(err.Error(), "unknown tool") {
			t.Fatalf("catalog-excluded tool %q is not registered for tools/call: %v", name, err)
		}
		// Registration on the MCP server is only half the path. Dispatch
		// resolves a capability by catalog name, and that lookup lives behind a
		// separate map; when it was empty the wire call still failed, just with
		// a different message. Both halves are asserted here.
		if err != nil && strings.Contains(err.Error(), "is not registered") {
			t.Fatalf("catalog-excluded tool %q has no dispatch capability: %v", name, err)
		}
		if result != nil {
			for _, content := range result.Content {
				text, ok := content.(*mcp.TextContent)
				if ok && strings.Contains(text.Text, "is not registered") {
					t.Fatalf("catalog-excluded tool %q has no dispatch capability: %s", name, text.Text)
				}
			}
		}
	}
}
