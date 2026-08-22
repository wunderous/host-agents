package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/session"
)

func TestPlatformAssistantSendsExplicitSessionAndParsesTraceHistory(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, payload)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(strings.Join([]string{
			`data: {"type":"data-chat-execution-trace","data":{"event":{"kind":"tool-step","status":"completed","label":"Completed list VMs","data":{"toolInputs":[{"toolCallId":"call-1","toolName":"incus__list_vms","args":{}}],"toolOutputs":[{"toolCallId":"call-1","toolName":"incus__list_vms","output":{"vms":[]}}]}}}}`,
			`data: {"type":"text-delta","delta":"No VMs are available."}`,
			`data: [DONE]`,
			"",
		}, "\n\n")))
	}))
	defer server.Close()

	assistant := &PlatformAssistant{URL: server.URL, Token: "session-token", HTTP: server.Client()}
	request := session.Request{
		ContractVersions: []string{session.ContractVersion},
		SessionID:        "session-1",
		TurnID:           "turn-1",
		CatalogRevision:  "sha256:host-catalog",
		Input:            "List the available VMs.",
	}
	turn, err := assistant.Send(context.Background(), request.Input, request)
	if err != nil {
		t.Fatalf("first platform turn: %v", err)
	}
	if turn.Text != "No VMs are available." || len(turn.Trace) != 1 || len(turn.ToolHistory) != 1 {
		t.Fatalf("platform turn = %#v", turn)
	}
	if turn.ToolHistory[0].ToolName != "incus__list_vms" || turn.ToolHistory[0].TurnID != "turn-1" {
		t.Fatalf("platform history = %#v", turn.ToolHistory)
	}

	request.TurnID = "turn-2"
	request.Input = "Show those VMs again."
	if _, err := assistant.Send(context.Background(), request.Input, request); err != nil {
		t.Fatalf("second platform turn: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	secondMessages, ok := requests[1]["messages"].([]any)
	if !ok || len(secondMessages) != 3 {
		t.Fatalf("second messages = %#v", requests[1]["messages"])
	}
	secondSession, ok := requests[1]["hostAgentSession"].(map[string]any)
	if !ok || secondSession["catalogRevision"] != "sha256:host-catalog" {
		t.Fatalf("second host session = %#v", requests[1]["hostAgentSession"])
	}
}
