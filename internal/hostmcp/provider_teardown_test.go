package hostmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/tools"
)

func TestProviderTeardownFinalizationFailureLeavesGenerationRetryable(t *testing.T) {
	server, _ := newBindingTestServer(t)
	const providerID = "com.opute.teardown-test"
	const generationID = providerID + "-1"

	var finalizeCalls atomic.Int32
	provider := mcp.NewServer(&mcp.Implementation{Name: "teardown-test-provider", Version: "1.0.0"}, nil)
	provider.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: providercontract.InstallManifest{
			Schema:   providercontract.InstallManifestVersion,
			Provider: providercontract.ProviderRef{ID: providerID, Version: "1.0.0"},
		}}, nil
	})
	provider.AddTool(&mcp.Tool{Name: providerTeardownOperation, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		if request != nil && request.Params != nil {
			_ = json.Unmarshal(request.Params.Arguments, &args)
		}
		if args["phase"] != "finalize" {
			return &mcp.CallToolResult{StructuredContent: map[string]any{
				"contractVersion": "host-plan.v1",
				"plan":            map[string]any{"contractVersion": "host-plan.v1"},
			}}, nil
		}
		inputs, _ := args["inputs"].(map[string]any)
		if inputs["phase"] != "finalize" {
			return &mcp.CallToolResult{IsError: true}, nil
		}
		if finalizeCalls.Add(1) == 1 {
			return &mcp.CallToolResult{IsError: true}, nil
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"completed": true}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return provider }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	descriptor := providercontract.PluginDescriptor{
		Schema:   providercontract.PluginDescriptorVersion,
		PluginID: providerID,
		Version:  "1.0.0",
		Capabilities: []providercontract.CapabilityRef{{
			ID: "opute.capability.teardown-test.v1", Version: 1,
		}},
		Server: providercontract.ServerDescriptor{Transport: "streamable_http", Endpoint: httpServer.URL},
	}
	adapter, err := provideradapter.Connect(context.Background(), descriptor, provideradapter.Options{})
	if err != nil {
		t.Fatalf("connect provider adapter: %v", err)
	}
	defer adapter.Close()

	manager := cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	server.providerLifecycle = manager
	generation, err := manager.CreateCandidate(providercontract.ProviderRef{ID: descriptor.PluginID, Version: descriptor.Version}, "manifest-hash", descriptor.Server.Endpoint, "catalog")
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	if generation.ID != generationID {
		t.Fatalf("generation ID = %q, want %q", generation.ID, generationID)
	}
	if err := manager.MarkReady(generation.ID); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if _, _, err := manager.Activate(generation.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	// Mount the generation the way the lifecycle does. Seeding an adapter map
	// directly would not exercise the fiber that teardown actually disposes.
	teardownManifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: providerID, Version: descriptor.Version},
		Services: []providercontract.ServiceDefinition{{
			ID: "opute.capability.teardown-test", CapabilityID: "opute.capability.teardown-test.v1", Version: 1,
		}},
	}
	if err := server.mountProviderGeneration(teardownManifest, generation.ID, adapter); err != nil {
		t.Fatalf("mount provider generation: %v", err)
	}

	metadata := map[string]any{
		"providerId":             providerID,
		"providerGenerationId":   generation.ID,
		"providerTeardownInputs": map[string]any{"tunnelId": "disposable-tunnel"},
	}
	if err := server.completeProviderTeardown(metadata); err == nil {
		t.Fatal("first finalization unexpectedly succeeded")
	}
	if _, ok := manager.Active(providerID); !ok {
		t.Fatal("provider generation was retired after finalization failure")
	}
	if server.providerGenerationAdapter(providerID, generation.ID) == nil {
		t.Fatal("provider adapter was removed after finalization failure")
	}

	if err := server.completeProviderTeardown(metadata); err != nil {
		t.Fatalf("retry finalization: %v", err)
	}
	if _, ok := manager.Active(providerID); ok {
		t.Fatal("provider generation remained active after successful retry")
	}
	stopped, ok := manager.Get(generation.ID)
	if !ok || stopped.State != cordis.GenerationStopped {
		t.Fatalf("generation after successful retry = %#v, found=%v", stopped, ok)
	}
	if server.providerGenerationAdapter(providerID, generation.ID) != nil {
		t.Fatal("provider adapter remained connected after successful retry")
	}
	if calls := finalizeCalls.Load(); calls != 2 {
		t.Fatalf("finalize calls = %d, want 2", calls)
	}
}

func TestCleanupProviderHostServiceRemovesOnlyRunOwnedUnitAfterFinalize(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unitPath := filepath.Join(home, ".config", "systemd", "user", "opute-provider-test.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var commands []string
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
		HostCommandRunnerFn: func(command []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
			commands = append(commands, strings.Join(command, " "))
			joined := strings.Join(command, " ")
			switch {
			case strings.Contains(joined, "is-active"):
				return hostexec.Result{ExitCode: 0, Stdout: "active\n"}, nil
			case strings.Contains(joined, "is-enabled"):
				return hostexec.Result{ExitCode: 0, Stdout: "enabled\n"}, nil
			default:
				return hostexec.Result{ExitCode: 0}, nil
			}
		},
	})
	server, err := NewServer(Options{ProviderID: "incus", Ops: svc, Standalone: true, AllowMutations: true, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.cleanupProviderHostService(map[string]any{
		"serviceName": "opute-provider-test.service",
		"serviceFile": unitPath,
		"scope":       "user",
	}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("run-owned service unit still exists: %v", err)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, " disable opute-provider-test.service") || !strings.Contains(joined, " stop opute-provider-test.service") {
		t.Fatalf("cleanup did not disable and stop the declared unit: %s", joined)
	}
	if strings.Index(joined, " disable opute-provider-test.service") > strings.Index(joined, " stop opute-provider-test.service") {
		t.Fatalf("service was stopped before it was disabled: %s", joined)
	}
}

func TestCleanupProviderHostServiceReportsReloadFailureWithoutStoppingProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unitPath := filepath.Join(home, ".config", "systemd", "user", "opute-provider-test.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var commands []string
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
		HostCommandRunnerFn: func(command []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
			commands = append(commands, strings.Join(command, " "))
			joined := strings.Join(command, " ")
			if strings.Contains(joined, "is-active") {
				return hostexec.Result{ExitCode: 0, Stdout: "active\n"}, nil
			}
			if strings.Contains(joined, "is-enabled") {
				return hostexec.Result{ExitCode: 1, Stderr: "disabled\n"}, nil
			}
			if strings.Contains(joined, "daemon-reload") {
				return hostexec.Result{ExitCode: 1, Stderr: "reload failed\n"}, nil
			}
			return hostexec.Result{ExitCode: 0}, nil
		},
	})
	server, err := NewServer(Options{ProviderID: "incus", Ops: svc, Standalone: true, AllowMutations: true, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	err = server.cleanupProviderHostService(map[string]any{
		"serviceName": "opute-provider-test.service",
		"serviceFile": unitPath,
		"scope":       "user",
	})
	if err == nil || !strings.Contains(err.Error(), "reload provider service manager") {
		t.Fatalf("cleanup error = %v, want visible reload failure", err)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("service unit was not removed before reload failure: %v", err)
	}
	for _, command := range commands {
		if strings.Contains(command, " stop ") {
			t.Fatalf("provider was stopped after cleanup failure: %s", strings.Join(commands, "\n"))
		}
	}
}
