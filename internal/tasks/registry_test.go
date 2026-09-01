package tasks

import (
	"testing"
	"time"
)

// run_host_command's task-awareness moved onto its dispatch registration in
// W8; the assertion lives in test/contract, which can see the registry. What
// this package still owns is the residue table itself.
func TestResidualTaskTableIsNotEmpty(t *testing.T) {
	if !TaskAwareTools["request_task_input"] {
		t.Fatal("request_task_input is transport-owned and must stay task-aware here")
	}
}

func TestInputRequiredTaskAcceptsStandardUpdates(t *testing.T) {
	registry := NewRegistry()
	resumed := make(chan map[string]any, 1)
	rec := registry.CreateWithInput(
		"request_task_input", map[string]any{"prompt": "continue"}, time.Minute,
		"Waiting for operator input...", nil, nil,
		map[string]any{"response": map[string]any{"type": "string", "prompt": "continue"}},
		func(responses map[string]any) { resumed <- responses },
	)
	if rec.Status != StatusInputRequired {
		t.Fatalf("status = %s, want input_required", rec.Status)
	}
	view := registry.ToGetTaskResult(rec)
	if _, ok := view["inputRequests"]; !ok {
		t.Fatalf("inputRequests missing from tasks/get projection: %#v", view)
	}
	updated, ok := registry.Update(rec.TaskID, map[string]any{"unknown": "ignored", "response": "yes"})
	if !ok || updated.Status != StatusWorking {
		t.Fatalf("update did not resume task: ok=%v record=%#v", ok, updated)
	}
	select {
	case responses := <-resumed:
		if responses["response"] != "yes" {
			t.Fatalf("resume responses = %#v", responses)
		}
	case <-time.After(time.Second):
		t.Fatal("resume callback did not run")
	}
	registry.Complete(rec.TaskID, ToolResult{StructuredContent: map[string]any{"response": "yes"}})
	if got, _ := registry.Get(rec.TaskID); got.Status != StatusCompleted {
		t.Fatalf("status after completion = %s", got.Status)
	}
}

func TestRestoreSnapshotKeepsTerminalTaskFindableAfterRestart(t *testing.T) {
	registry := NewRegistry()
	restored, ok := registry.RestoreSnapshot(map[string]any{
		"taskId":         "durable-task-1",
		"toolName":       "run_host_command",
		"toolArgs":       map[string]any{"command": "true"},
		"status":         "completed",
		"createdAt":      "2026-08-24T00:00:00Z",
		"lastUpdatedAt":  "2026-08-24T00:01:00Z",
		"ttlMs":          float64(60_000),
		"pollIntervalMs": float64(3_000),
		"result": map[string]any{
			"content":           []map[string]any{{"type": "text", "text": "done"}},
			"structuredContent": map[string]any{"ok": true},
			"isError":           false,
		},
	})
	if !ok || restored == nil {
		t.Fatal("terminal task snapshot was not restored")
	}
	view, found := registry.Get("durable-task-1")
	if !found || view.Status != StatusCompleted || view.ToolResult == nil {
		t.Fatalf("restored task = %#v", view)
	}
	if result := registry.ToGetTaskResult(view); result["result"] == nil {
		t.Fatalf("restored task omitted final result: %#v", result)
	}

	interrupted, ok := registry.RestoreSnapshot(map[string]any{
		"taskId":   "durable-task-2",
		"toolName": "run_host_command",
		"status":   "working",
	})
	if !ok || interrupted.Status != StatusFailed {
		t.Fatalf("interrupted task status = %#v, want failed", interrupted)
	}
}

func TestRestoreSnapshotKeepsInputRequiredTaskResumable(t *testing.T) {
	registry := NewRegistry()
	restored, ok := registry.RestoreSnapshot(map[string]any{
		"taskId":        "waiting-task-1",
		"toolName":      "run_host_plan",
		"status":        "input_required",
		"statusMessage": "The task requires input before it can continue.",
		"inputRequests": map[string]any{
			"decision": map[string]any{"type": "string"},
		},
	})
	if !ok || restored == nil || restored.Status != StatusInputRequired {
		t.Fatalf("restored waiting task = %#v", restored)
	}
	resumed := make(chan map[string]any, 1)
	if _, ok := registry.SetResume(restored.TaskID, func(input map[string]any) { resumed <- input }); !ok {
		t.Fatal("could not attach waiting task continuation")
	}
	if _, ok := registry.Update(restored.TaskID, map[string]any{"decision": "approved"}); !ok {
		t.Fatal("restored waiting task did not accept input")
	}
	select {
	case input := <-resumed:
		if input["decision"] != "approved" {
			t.Fatalf("resumed input = %#v", input)
		}
	case <-time.After(time.Second):
		t.Fatal("restored waiting task continuation did not run")
	}
}
