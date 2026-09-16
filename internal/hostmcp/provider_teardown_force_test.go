package hostmcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/tools"
)

// newOrphanedProviderTestServer builds a host agent whose active provider
// generation is registered but not mounted. That is exactly the state a
// provider whose process died leaves behind: the lifecycle still calls the
// generation active, and every adapter lookup for it returns nil.
func newOrphanedProviderTestServer(t *testing.T, providerID string) (*Server, *cordis.ProviderLifecycleManager, cordis.ProviderGeneration, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	unitPath := filepath.Join(home, ".config", "systemd", "user", "opute-provider-orphan.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("orphaned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(provider string) []string {
			names, err := tools.HostToolNamesForProvider(provider)
			if err != nil {
				return nil
			}
			return names
		},
		HostCommandRunnerFn: func(command []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
			joined := strings.Join(command, " ")
			switch {
			// The provider process is gone, so its unit is inactive but its
			// unit file is still on disk and still enabled.
			case strings.Contains(joined, "is-active"):
				return hostexec.Result{ExitCode: 3, Stdout: "inactive\n"}, nil
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
	t.Cleanup(func() { _ = server.Close() })

	manager := cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	server.providerLifecycle = manager
	generation, err := manager.CreateCandidate(providercontract.ProviderRef{ID: providerID, Version: "1.0.0"}, "manifest-hash", "http://127.0.0.1:1/mcp", "catalog")
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	if err := manager.MarkReady(generation.ID); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if _, _, err := manager.Activate(generation.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if server.providerGenerationAdapter(providerID, generation.ID) != nil {
		t.Fatal("orphaned generation unexpectedly has a mounted adapter")
	}
	return server, manager, generation, unitPath
}

func resultText(result *mcp.CallToolResult) string {
	parts := []string{}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, " | ")
}

func TestProviderTeardownWithoutForceCannotReclaimDeadProviderProcess(t *testing.T) {
	const providerID = "com.opute.orphan-test"
	server, manager, generation, _ := newOrphanedProviderTestServer(t, providerID)

	result, err := server.handleProviderTeardown(map[string]any{
		"provider": providerID,
		"confirm":  true,
		"inputs":   map[string]any{"serviceName": "opute-provider-orphan.service", "scope": "user"},
	})
	if err != nil {
		t.Fatalf("handleProviderTeardown: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("teardown of a dead provider should fail without force: %#v", result)
	}
	// The defect this pins: the generation the caller asked to tear down is
	// still active afterwards, and status still derives `connected` from the
	// same adapter lookup that just failed.
	if _, ok := manager.Active(providerID); !ok {
		t.Fatal("generation was retired without force")
	}
	if stored, ok := manager.Get(generation.ID); !ok || stored.State != cordis.GenerationActive {
		t.Fatalf("generation state = %#v, found=%v; want active", stored, ok)
	}
}

func TestForcedProviderTeardownReclaimsGenerationWhoseProcessIsGone(t *testing.T) {
	const providerID = "com.opute.orphan-test"
	server, manager, generation, unitPath := newOrphanedProviderTestServer(t, providerID)

	result, err := server.handleProviderTeardown(map[string]any{
		"provider": providerID,
		"confirm":  true,
		"force":    true,
		"inputs":   map[string]any{"serviceName": "opute-provider-orphan.service", "serviceFile": unitPath, "scope": "user"},
	})
	if err != nil {
		t.Fatalf("forced teardown: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("forced teardown returned an error result: %s", resultText(result))
	}
	object, ok := structuredObject(result.StructuredContent)
	if !ok {
		t.Fatalf("forced teardown returned no structured run: %#v", result.StructuredContent)
	}
	// A forced reclaim is a durable run like any other teardown: same runId,
	// status and catalogRevision evidence, not a synthesized success.
	for _, field := range []string{"runId", "status", "catalogRevision"} {
		if value, _ := object[field].(string); strings.TrimSpace(value) == "" {
			t.Fatalf("forced teardown run is missing %s: %#v", field, object)
		}
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		stored, found := manager.Get(generation.ID)
		if found && stored.State == cordis.GenerationStopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("generation was not retired by the forced reclaim: %#v found=%v", stored, found)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := manager.Active(providerID); ok {
		t.Fatal("generation is still active after a forced reclaim")
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("orphaned provider unit still exists after reclaim: %v", err)
	}
}

func TestForcedProviderTeardownCompletionSkipsTheUnreachableFinalizeCallback(t *testing.T) {
	const providerID = "com.opute.orphan-test"
	server, manager, generation, unitPath := newOrphanedProviderTestServer(t, providerID)

	metadata := map[string]any{
		"providerId":                   providerID,
		"providerGenerationId":         generation.ID,
		"providerTeardownForced":       true,
		"providerTeardownForcedReason": `provider "com.opute.orphan-test" is not connected`,
		"providerTeardownInputs":       map[string]any{"serviceName": "opute-provider-orphan.service", "serviceFile": unitPath, "scope": "user"},
	}
	if err := server.completeProviderTeardown(metadata); err != nil {
		t.Fatalf("forced completion: %v", err)
	}
	if _, ok := manager.Active(providerID); ok {
		t.Fatal("generation is still active after forced completion")
	}

	// Without the forced marker the same metadata must still fail: an
	// unreachable provider has not consented to anything, and only an explicit
	// force may skip its finalize callback.
	server2, manager2, generation2, unitPath2 := newOrphanedProviderTestServer(t, providerID)
	err := server2.completeProviderTeardown(map[string]any{
		"providerId":             providerID,
		"providerGenerationId":   generation2.ID,
		"providerTeardownInputs": map[string]any{"serviceName": "opute-provider-orphan.service", "serviceFile": unitPath2, "scope": "user"},
	})
	if err == nil || !strings.Contains(err.Error(), "not connected for teardown finalization") {
		t.Fatalf("unforced completion error = %v, want an unreachable-provider failure", err)
	}
	if _, ok := manager2.Active(providerID); !ok {
		t.Fatal("generation was retired by an unforced completion against a dead provider")
	}
}
