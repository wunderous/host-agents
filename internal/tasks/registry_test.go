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

func TestRegistryReadAPIsReturnStableDeepSnapshots(t *testing.T) {
	registry := NewRegistry()
	inputIDs := []int{1, 2}
	inputGroups := map[string][]int{"blue": {3, 4}}
	inputCycle := map[string]any{"label": "cycle"}
	inputCycle["self"] = inputCycle
	type pointerKey struct{ Value *int }
	keyValue := 5
	inputKeys := map[pointerKey]string{{Value: &keyValue}: "stable"}
	created := registry.Create("inspect_host", map[string]any{
		"filter": map[string]any{"labels": []any{"production"}},
		"ids":    inputIDs,
		"groups": inputGroups,
		"cycle":  inputCycle,
		"keys":   inputKeys,
	}, time.Minute, "inspect", map[string]any{"source": "client"})
	inputIDs[0] = 99
	inputGroups["blue"][0] = 99
	inputCycle["label"] = "mutated-input"
	keyValue = 99
	created.ToolArgs["filter"].(map[string]any)["labels"].([]any)[0] = "mutated-create-result"
	created.ToolArgs["ids"].([]int)[0] = 98
	created.ToolArgs["groups"].(map[string][]int)["blue"][0] = 98
	created.ToolArgs["cycle"].(map[string]any)["label"] = "mutated-create-cycle"

	firstRead, ok := registry.Get(created.TaskID)
	if !ok {
		t.Fatal("created task was not found")
	}
	resultCodes := []int{7, 8}
	registry.Complete(created.TaskID, ToolResult{StructuredContent: map[string]any{
		"host":  map[string]any{"name": "node-a"},
		"codes": resultCodes,
	}})
	resultCodes[0] = 97
	if firstRead.Status != StatusWorking {
		t.Fatalf("previous snapshot changed after completion: status=%s", firstRead.Status)
	}
	firstRead.Metadata["source"] = "mutated-get-result"

	completed, ok := registry.Get(created.TaskID)
	if !ok || completed.Status != StatusCompleted {
		t.Fatalf("completed task snapshot = %#v, found=%v", completed, ok)
	}
	completed.ToolResult.StructuredContent.(map[string]any)["host"].(map[string]any)["name"] = "mutated-result"
	completed.ToolResult.StructuredContent.(map[string]any)["codes"].([]int)[0] = 96
	listed := registry.List()
	listed[0].Status = StatusFailed

	stored, ok := registry.Get(created.TaskID)
	if !ok || stored.Status != StatusCompleted {
		t.Fatalf("read snapshot mutation changed registry status: %#v", stored)
	}
	args := stored.ToolArgs["filter"].(map[string]any)["labels"].([]any)
	if args[0] != "production" {
		t.Fatalf("created/read snapshot aliased stored arguments: %#v", args)
	}
	if stored.ToolArgs["ids"].([]int)[0] != 1 || stored.ToolArgs["groups"].(map[string][]int)["blue"][0] != 3 {
		t.Fatalf("typed input composites aliased task state: %#v", stored.ToolArgs)
	}
	keys := stored.ToolArgs["keys"].(map[pointerKey]string)
	for key, value := range keys {
		if value != "stable" || key.Value == nil || *key.Value != 5 {
			t.Fatalf("map-key pointer aliased task state: key=%#v value=%q", key, value)
		}
		*key.Value = 42
	}
	storedAfterKeyMutation, ok := registry.Get(created.TaskID)
	if !ok {
		t.Fatal("task disappeared after map-key snapshot mutation")
	}
	for key := range storedAfterKeyMutation.ToolArgs["keys"].(map[pointerKey]string) {
		if key.Value == nil || *key.Value != 5 {
			t.Fatalf("map-key snapshot mutation changed registry state: %#v", key)
		}
	}
	if cycle := stored.ToolArgs["cycle"].(map[string]any); cycle["label"] != "cycle" || cycle["self"].(map[string]any)["label"] != "cycle" {
		t.Fatalf("cyclic input composite aliased task state: %#v", cycle)
	}
	if stored.Metadata["source"] != "client" {
		t.Fatalf("read snapshot aliased stored metadata: %#v", stored.Metadata)
	}
	name := stored.ToolResult.StructuredContent.(map[string]any)["host"].(map[string]any)["name"]
	if name != "node-a" {
		t.Fatalf("read snapshot aliased stored tool result: %#v", name)
	}
	if codes := stored.ToolResult.StructuredContent.(map[string]any)["codes"].([]int); codes[0] != 7 {
		t.Fatalf("typed result composite aliased task state: %#v", codes)
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
