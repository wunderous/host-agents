package hostmcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
)

type boundaryProvider struct {
	server         *mcp.Server
	httpServer     *httptest.Server
	generation     string
	cancelObserved chan struct{}
	cancelOnce     sync.Once
}

func newBoundaryProvider(t *testing.T, generation string) *boundaryProvider {
	t.Helper()
	provider := &boundaryProvider{
		generation:     generation,
		cancelObserved: make(chan struct{}),
	}
	manifest := boundaryManifest(generation)
	provider.server = mcp.NewServer(&mcp.Implementation{Name: "boundary-provider", Version: "1.0.0"}, nil)
	provider.server.AddTool(&mcp.Tool{
		Name:        "opute.provider.get_install_manifest",
		Description: "Read the boundary provider manifest",
		InputSchema: map[string]any{"type": "object"},
		OutputSchema: map[string]any{
			"type": "object",
		},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return boundaryStructured(manifest), nil
	})
	for _, operation := range manifest.Services[0].Operations {
		operation := operation
		provider.server.AddTool(&mcp.Tool{
			Name:         operation.ID,
			Description:  operation.Description,
			InputSchema:  operation.InputSchema,
			OutputSchema: operation.OutputSchema,
		}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			arguments := map[string]any{}
			if request != nil && request.Params != nil && len(request.Params.Arguments) > 0 {
				if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
					return nil, err
				}
			}
			switch operation.ID {
			case "opute.capability.boundary.probe":
				return boundaryStructured(map[string]any{"generation": provider.generation, "ok": true}), nil
			case "opute.capability.boundary.secret":
				return boundaryStructured(map[string]any{"accepted": arguments["secret"] != nil}), nil
			case "opute.capability.boundary.task":
				return boundaryStructured(map[string]any{"resultType": "task", "taskId": "provider-task"}), nil
			case "opute.capability.boundary.cancel":
				select {
				case <-ctx.Done():
					provider.cancelOnce.Do(func() { close(provider.cancelObserved) })
					return nil, ctx.Err()
				case <-time.After(5 * time.Second):
					return boundaryStructured(map[string]any{"cancelled": false}), nil
				}
			default:
				return nil, errors.New("unknown boundary operation")
			}
		})
	}
	provider.httpServer = httptest.NewServer(mcphttp.WrapProviderHandler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return provider.server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true}), map[string]any{"name": "boundary-provider", "version": "1"}))
	t.Cleanup(provider.httpServer.Close)
	return provider
}

func boundaryManifest(generation string) providercontract.InstallManifest {
	writeOnlySecret := map[string]any{
		"type":     "object",
		"required": []string{"secret"},
		"properties": map[string]any{
			"secret": map[string]any{"type": "string", "writeOnly": true},
		},
	}
	operation := func(id string, input map[string]any) providercontract.Operation {
		return providercontract.Operation{
			ID: id, Version: 1, InputSchema: input,
			OutputSchema: map[string]any{"type": "object"}, Effect: "read", Idempotent: true,
			TaskSupport: "sync_only",
		}
	}
	return providercontract.InstallManifest{
		Schema:   providercontract.InstallManifestVersion,
		Provider: providercontract.ProviderRef{ID: "com.opute.boundary", Version: generation},
		Provides: []providercontract.CapabilityRef{{ID: capabilitycontract.Kubernetes, Version: 1}},
		Services: []providercontract.ServiceDefinition{{
			ID: "opute.capability.boundary", Version: 1,
			Operations: []providercontract.Operation{
				operation("opute.capability.boundary.probe", map[string]any{"type": "object"}),
				operation("opute.capability.boundary.secret", writeOnlySecret),
				operation("opute.capability.boundary.task", map[string]any{"type": "object"}),
				operation("opute.capability.boundary.cancel", map[string]any{"type": "object"}),
			},
		}},
		Validation: providercontract.ValidationRef{Capability: capabilitycontract.Kubernetes, Operation: "opute.capability.boundary.probe"},
	}
}

func boundaryStructured(value any) *mcp.CallToolResult {
	return &mcp.CallToolResult{StructuredContent: value}
}

func activateBoundaryProvider(t *testing.T, server *Server, provider *boundaryProvider) providercontract.InstallManifest {
	t.Helper()
	descriptor := providercontract.PluginDescriptor{
		Schema:       providercontract.PluginDescriptorVersion,
		PluginID:     "com.opute.boundary",
		Version:      provider.generation,
		Capabilities: []providercontract.CapabilityRef{{ID: capabilitycontract.Kubernetes, Version: 1}},
		Server:       providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: provider.httpServer.URL},
	}
	adapter, err := provideradapter.Connect(context.Background(), descriptor, provideradapter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manifest := boundaryManifest(provider.generation)
	generation, err := server.providerLifecycle.CreateCandidate(manifest.Provider, "sha256:"+provider.generation, provider.httpServer.URL, server.CatalogSnapshot().Revision)
	if err != nil {
		_ = adapter.Close()
		t.Fatal(err)
	}
	server.providerMu.Lock()
	server.providerCandidates[generation.ID] = adapter
	server.providerCandidateManifests[generation.ID] = manifest
	server.providerMu.Unlock()
	if err := server.activateProviderGeneration(map[string]any{
		"activate":             true,
		"providerGenerationId": generation.ID,
	}); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestProviderBoundaryOverStreamableHTTP(t *testing.T) {
	server, stateDir := newBindingTestServer(t)
	first := newBoundaryProvider(t, "1.0.0")
	activateBoundaryProvider(t, server, first)
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server.mcpServer
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true}))
	defer host.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "provider-boundary-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMCPTool(toolsResult.Tools, "opute.capability.boundary.probe") {
		t.Fatal("provider operation was not discovered over Streamable HTTP")
	}

	probe, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "opute.capability.boundary.probe", Arguments: map[string]any{}})
	if err != nil || probe.IsError {
		t.Fatalf("first provider call = %#v, err=%v", probe, err)
	}
	if structured, ok := probe.StructuredContent.(map[string]any); !ok || structured["generation"] != "1.0.0" {
		t.Fatalf("first provider result = %#v", probe.StructuredContent)
	}

	secret, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "opute.capability.boundary.secret", Arguments: map[string]any{"secret": "wire-secret"}})
	if err != nil || secret.IsError {
		t.Fatalf("secret provider call = %#v, err=%v", secret, err)
	}
	var argumentsJSON string
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT arguments_json FROM capability_invocations WHERE operation_id = ? ORDER BY created_at DESC LIMIT 1`, "opute.capability.boundary.secret").Scan(&argumentsJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(argumentsJSON, "wire-secret") || !strings.Contains(argumentsJSON, redactedEvidenceValue) {
		t.Fatalf("durable evidence leaked provider secret: %s", argumentsJSON)
	}

	taskResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "opute.capability.boundary.task", Arguments: map[string]any{}})
	if err != nil || !taskResult.IsError {
		t.Fatalf("provider task result was not rejected as synchronous-only: %#v, err=%v", taskResult, err)
	}

	cancelClient := mcp.NewClient(&mcp.Implementation{Name: "provider-boundary-cancel-test", Version: "1.0.0"}, nil)
	cancelSession, err := cancelClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callCtx, callCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	_, _ = cancelSession.CallTool(callCtx, &mcp.CallToolParams{Name: "opute.capability.boundary.cancel", Arguments: map[string]any{}})
	callCancel()
	_ = cancelSession.Close()
	select {
	case <-first.cancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not observe cancellation over Streamable HTTP")
	}

	second := newBoundaryProvider(t, "2.0.0")
	activateBoundaryProvider(t, server, second)
	replaced, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "opute.capability.boundary.probe", Arguments: map[string]any{}})
	if err != nil || replaced.IsError {
		t.Fatalf("replacement provider call = %#v, err=%v", replaced, err)
	}
	if structured, ok := replaced.StructuredContent.(map[string]any); !ok || structured["generation"] != "2.0.0" {
		t.Fatalf("replacement provider result = %#v", replaced.StructuredContent)
	}
}

func containsMCPTool(tools []*mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool != nil && tool.Name == name {
			return true
		}
	}
	return false
}
