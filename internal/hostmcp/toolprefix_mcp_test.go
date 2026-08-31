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

func newPrefixedTestServer(t *testing.T, agentID string) *Server {
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
		ProviderID:      "incus",
		Ops:             svc,
		Standalone:      true,
		AllowMutations:  false,
		StateDir:        t.TempDir(),
		AgentID:         agentID,
		PrefixToolNames: true,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func TestPrefixToolNamesRequiresAgentID(t *testing.T) {
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
	_, err := NewServer(Options{
		ProviderID:      "incus",
		Ops:             svc,
		Standalone:      true,
		StateDir:        t.TempDir(),
		PrefixToolNames: true,
	})
	if err == nil {
		t.Fatal("expected PrefixToolNames without AgentID to fail closed")
	}
}

func TestPrefixedToolsListAndCall(t *testing.T) {
	const agentID = "host-zephyrus-ef47fbbf"
	server := newPrefixedTestServer(t, agentID)
	prefix := ToolNamePrefix(agentID)
	if server.ToolNamePrefix() != prefix {
		t.Fatalf("server prefix = %q, want %q", server.ToolNamePrefix(), prefix)
	}
	if server.ImplementationName() != "host-agent-"+prefix {
		t.Fatalf("implementation = %q", server.ImplementationName())
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "prefix-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	marker := prefix + "_"
	sawCatalog := false
	for _, tool := range listed.Tools {
		if !strings.HasPrefix(tool.Name, marker) {
			t.Fatalf("unprefixed wire name %q", tool.Name)
		}
		if strings.HasSuffix(tool.Name, "_get_capability_catalog") || tool.Name == marker+"get_capability_catalog" {
			sawCatalog = true
		}
		if tool.Name == "get_capability_catalog" || tool.Name == "list_vms" {
			t.Fatalf("catalog name leaked onto tools/list: %q", tool.Name)
		}
	}
	if !sawCatalog {
		t.Fatal("prefixed get_capability_catalog missing from tools/list")
	}

	wire := WireToolName(prefix, "get_capability_catalog")
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: wire, Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("prefixed call failed: %#v err=%v", result, err)
	}
	unprefixed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_capability_catalog", Arguments: map[string]any{}})
	if err == nil && unprefixed != nil && !unprefixed.IsError {
		t.Fatal("in-memory tools/call must not register unprefixed catalog names")
	}
}

func TestPrefixedServersAdvertiseDisjointNames(t *testing.T) {
	left := newPrefixedTestServer(t, "host-zephyrus-ef47fbbf")
	right := newPrefixedTestServer(t, "host-workstation-e5059700")
	if left.ToolNamePrefix() == right.ToolNamePrefix() {
		t.Fatal("co-resident agents must not share a tool prefix")
	}
	leftNames := map[string]bool{}
	for _, name := range listPublishedTools(t, left) {
		leftNames[name] = true
	}
	for _, name := range listPublishedTools(t, right) {
		if leftNames[name] {
			t.Fatalf("tool name %q collided across agents", name)
		}
	}
}

func TestResolveIncomingToolCallNameRewritesCatalogNames(t *testing.T) {
	server := newPrefixedTestServer(t, "host-zephyrus-ef47fbbf")
	prefix := server.ToolNamePrefix()
	if got := server.ResolveIncomingToolCallName("list_vms"); got != prefix+"_list_vms" {
		t.Fatalf("resolve list_vms = %q", got)
	}
	if got := server.ResolveIncomingToolCallName(prefix + "_list_vms"); got != prefix+"_list_vms" {
		t.Fatalf("resolve already-prefixed = %q", got)
	}
	if got := server.ResolveIncomingToolCallName("not_a_real_tool"); got != "not_a_real_tool" {
		t.Fatalf("unknown names must not be rewritten: %q", got)
	}
}
