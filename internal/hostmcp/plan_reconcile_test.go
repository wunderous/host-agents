package hostmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A failed run is a report about the world as it was when that run stopped, so
// it must not be handed back to a caller asking for a fresh execution. It was:
// the harness recipe kept returning a node failure recorded before the defect
// behind it had been fixed and the agent rolled, so the repaired plan never ran
// and the failure looked permanent. Re-running is safe because every node
// revalidates before it acts.
func TestFailedHostPlanIsReconciledNotReplayed(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	// ensure_host_file refuses paths outside the invoking user's home
	// directory, so the managed file lives under a temporary directory there.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home directory: %v", err)
	}
	dir, err := os.MkdirTemp(home, "hostmcp-failed-reconcile-")
	if err != nil {
		t.Fatalf("create managed directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// The plan asserts a second file that only the environment can provide, so
	// the first run fails for a reason outside the plan - exactly the shape of
	// a defect that is later fixed underneath a stored failure.
	managed := filepath.Join(dir, "managed.txt")
	precondition := filepath.Join(dir, "precondition.txt")
	planDocument := map[string]any{
		"contractVersion": "host-plan.v1",
		"planId":          "hostmcp-failed-reconcile",
		"generation":      1,
		"idempotencyKey":  "hostmcp-failed-reconcile-1",
		"nodes": []any{map[string]any{
			"id":     "file",
			"action": map[string]any{"tool": "ensure_host_file", "args": map[string]any{"path": managed, "content": "managed\n"}},
			"validate": map[string]any{
				"tool":   "inspect_host_file",
				"args":   map[string]any{"path": precondition},
				"assert": []any{map[string]any{"path": "/exists", "op": "eq", "value": true}},
			},
		}},
	}

	runPlan := func(step string) string {
		started, runErr := server.handleRunHostPlan(map[string]any{"plan": planDocument})
		if runErr != nil || started == nil || started.IsError {
			t.Fatalf("%s run host plan = %#v err=%v", step, started, runErr)
		}
		var startedValue map[string]any
		encoded, _ := json.Marshal(started.StructuredContent)
		if decodeErr := json.Unmarshal(encoded, &startedValue); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		runID, _ := startedValue["runId"].(string)
		if runID == "" {
			t.Fatalf("%s run result has no runId: %#v", step, startedValue)
		}
		return runID
	}

	awaitStatus := func(step, runID, want string) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			record, found, getErr := server.state.GetPlan(runID)
			if getErr != nil || !found {
				t.Fatalf("%s: get durable plan: found=%v err=%v", step, found, getErr)
			}
			if record.Status == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: plan status %q never reached %q: %#v", step, record.Status, want, record)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	runID := runPlan("first")
	awaitStatus("first run", runID, "failed")

	// Repair the world the stored failure was recorded against.
	if writeErr := os.WriteFile(precondition, []byte("ready\n"), 0o600); writeErr != nil {
		t.Fatalf("write precondition: %v", writeErr)
	}
	if second := runPlan("second"); second != runID {
		t.Fatalf("expected the same durable run id for the same idempotency key, got %q and %q", runID, second)
	}
	awaitStatus("second run", runID, "completed")
	if _, statErr := os.Stat(managed); statErr != nil {
		t.Fatalf("expected the reconciled run to apply its action: %v", statErr)
	}
}
