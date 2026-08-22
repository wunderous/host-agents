package session

import "testing"

func TestRequestValidatesTypedHistoryAndProvenance(t *testing.T) {
	request := Request{
		ContractVersions: []string{ContractVersion},
		SessionID:        "session-1",
		TurnID:           "turn-1",
		CatalogRevision:  "sha256:catalog",
		Input:            "show status",
		References: []EntityReference{{
			OriginalToken:   "@worker",
			Kind:            "vm",
			CanonicalField:  "name",
			CanonicalValue:  "worker-1",
			Provider:        "incus",
			Source:          "inventory",
			Selection:       "exact_canonical",
			CatalogRevision: "sha256:catalog",
		}},
		Observations: []Observation{{
			Kind:           "vm",
			CanonicalField: "name",
			CanonicalValue: "worker-1",
			Source:         "inventory",
			Revision:       "inventory-1",
			Value:          map[string]any{"status": "running"},
		}},
		ToolCallHistory: []ToolCallHistory{{
			CallID:    "call-1",
			ToolName:  "get_host_info",
			TurnID:    "turn-1",
			Arguments: map[string]any{"fast": true},
			Status:    "success",
		}},
		Capabilities: []CapabilityIdentity{{OperationID: "get_host_info", Revision: "sha256:catalog"}},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestProposalRequiresPlanForHostPlanKind(t *testing.T) {
	proposal := Proposal{
		ProposalID:      "proposal-1",
		Kind:            ContractVersion,
		CatalogRevision: "sha256:catalog",
	}
	if err := proposal.Validate("sha256:catalog"); err == nil {
		t.Fatal("host-plan proposal without a plan was accepted")
	}
	proposal.Plan = map[string]any{"contractVersion": "host-plan.v1"}
	if err := proposal.Validate("sha256:catalog"); err != nil {
		t.Fatalf("valid host-plan proposal rejected: %v", err)
	}
}
