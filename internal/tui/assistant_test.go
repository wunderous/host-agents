package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wunderous/host-agents/internal/session"
	"github.com/wunderous/host-agents/internal/tools"
)

func assistantRequest(revision string) session.Request {
	return session.Request{
		ContractVersions: []string{session.ContractVersion},
		SessionID:        "assistant-session",
		TurnID:           "assistant-turn",
		CatalogRevision:  revision,
		Input:            "show host info",
	}
}

func TestAssistantProposeAcceptsBoundedTypedCommand(t *testing.T) {
	const revision = "sha256:catalog"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", request.Method)
		}
		var envelope map[string]any
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if envelope["responseFormat"] != "typed-command-or-host-plan-v1" {
			t.Fatalf("response format = %#v", envelope["responseFormat"])
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"proposal": session.Proposal{
				ProposalID:      "proposal-1",
				Kind:            "command",
				Capability:      "get_host_info",
				CatalogRevision: revision,
				Effect:          "read",
			},
		})
	}))
	defer server.Close()

	proposal, err := (&Assistant{URL: server.URL, HTTP: server.Client()}).Propose(
		context.Background(), "show host info", assistantRequest(revision),
		tools.CapabilityCatalogSnapshot{ProviderID: "incus", Revision: revision},
	)
	if err != nil {
		t.Fatalf("typed proposal: %v", err)
	}
	if proposal.Kind != "command" || proposal.Capability != "get_host_info" {
		t.Fatalf("proposal = %#v", proposal)
	}
}

func TestAssistantProposeRejectsStaleTypedProposal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"proposal": session.Proposal{
				ProposalID:      "proposal-stale",
				Kind:            "command",
				Capability:      "get_host_info",
				CatalogRevision: "sha256:old",
			},
		})
	}))
	defer server.Close()

	if _, err := (&Assistant{URL: server.URL, HTTP: server.Client()}).Propose(
		context.Background(), "show host info", assistantRequest("sha256:new"), nil,
	); err == nil {
		t.Fatal("stale assistant proposal was accepted")
	}
}
