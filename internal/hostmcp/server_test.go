package hostmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/tools"
)

func newStandaloneTestServer(t *testing.T, allowMutations bool) *Server {
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
	server, err := NewServer(Options{
		ProviderID:     "incus",
		Ops:            svc,
		Standalone:     true,
		AllowMutations: allowMutations,
		StateDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func TestStandaloneServerDoesNotExposePlatformTools(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	for _, def := range server.toolDefs {
		if tools.IsStandaloneMutation(def.Name) {
			continue
		}
		if def.Name == "register_host_agent" || def.Name == "host_agent_heartbeat" || def.Name == "dispatch_host_operation" {
			t.Fatalf("platform tool leaked into standalone catalog: %s", def.Name)
		}
	}
}

func TestStandaloneServerCloseIsIdempotent(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	if err := server.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestStandaloneMutationPolicyDeniesEveryMutatingTool(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	for name := range tools.StandaloneToolNames {
		if !tools.IsStandaloneMutation(name) {
			continue
		}
		result, err := server.handleToolCall(context.Background(), &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)},
		}, name)
		if err != nil {
			t.Fatalf("%s returned protocol error: %v", name, err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("%s was not denied while mutations were disabled: %+v", name, result)
		}
	}
}

func TestStandaloneContractIsValidated(t *testing.T) {
	if err := tools.ValidateStandaloneToolContract(); err != nil {
		t.Fatal(err)
	}
}
