package tasks

import (
	"testing"
	"time"
)

func TestHostServingReconciliationUsesTaskContract(t *testing.T) {
	if !TaskAwareTools["ensure_cloudflared_tunnel"] {
		t.Fatal("ensure_cloudflared_tunnel must use the MCP task contract")
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
