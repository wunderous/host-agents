package hostmcp

import (
	"encoding/json"
	"testing"

	"github.com/wunderous/host-agents/internal/session"
)

func TestOpenAssistantSessionNegotiatesCompatibilityAndCatalogRevision(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	revision := server.CatalogSnapshot().Revision

	good, err := server.handleOpenAssistantSession(map[string]any{
		"sessionId":                 "session-1",
		"supportedContractVersions": []any{session.ContractVersion},
		"catalogRevision":           revision,
	})
	if err != nil || good == nil || good.IsError {
		t.Fatalf("compatible session = %#v err=%v", good, err)
	}
	if good.StructuredContent == nil {
		t.Fatal("compatible session omitted structured response")
	}

	stale, err := server.handleOpenAssistantSession(map[string]any{
		"sessionId":                 "session-stale",
		"supportedContractVersions": []string{session.ContractVersion},
		"catalogRevision":           "sha256:stale",
	})
	if err != nil || stale == nil || !stale.IsError {
		t.Fatalf("stale session = %#v err=%v", stale, err)
	}

	incompatible, err := server.handleOpenAssistantSession(map[string]any{
		"sessionId":                 "session-old",
		"supportedContractVersions": []string{"assistant-session.v0"},
	})
	if err != nil || incompatible == nil || !incompatible.IsError {
		t.Fatalf("incompatible session = %#v err=%v", incompatible, err)
	}

	// Keep the typed result JSON-shaped at the MCP boundary.
	var envelope map[string]any
	encoded, _ := json.Marshal(good.StructuredContent)
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["contractVersion"] != session.ContractVersion {
		t.Fatalf("contract version = %#v", envelope["contractVersion"])
	}
}

func TestSessionRequestAndEventRejectUnboundedOrUnknownPayloads(t *testing.T) {
	request := session.Request{
		ContractVersions: []string{session.ContractVersion}, SessionID: "s", TurnID: "t", CatalogRevision: "sha256:catalog", Input: "status",
		ToolCallHistory: make([]session.ToolCallHistory, session.MaxHistoryEntries+1),
	}
	if err := request.Validate(); err == nil {
		t.Fatal("oversized history was accepted")
	}
	event := session.NewEvent("s", "t", "sha256:catalog", 0, "unknown.event", nil)
	if err := event.Validate(); err == nil {
		t.Fatal("unknown event kind was accepted")
	}

}
