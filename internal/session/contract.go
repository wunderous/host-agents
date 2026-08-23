package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const ContractVersion = "assistant-session.v1"

const (
	MaxHistoryEntries = 32
	MaxEvents         = 128
	MaxPayloadBytes   = 128 * 1024
)

// Request is the bounded, explicit context envelope used by local clients or
// the Platform-connected adapter. It is evidence and provenance, never hidden
// model memory or an authorization grant.
type Request struct {
	ContractVersions           []string             `json:"contractVersions"`
	SessionID                  string               `json:"sessionId"`
	TenantID                   string               `json:"tenantId,omitempty"`
	TurnID                     string               `json:"turnId"`
	CatalogRevision            string               `json:"catalogRevision"`
	TransportCatalogRevision   string               `json:"transportCatalogRevision,omitempty"`
	ContextRevision            string               `json:"contextRevision,omitempty"`
	SemanticVocabularyRevision string               `json:"semanticVocabularyRevision,omitempty"`
	EmbeddingProviderRevision  string               `json:"embeddingProviderRevision,omitempty"`
	ConversationCompactionRev  string               `json:"conversationCompactionRevision,omitempty"`
	SurfacePolicyRevision      string               `json:"surfacePolicyRevision,omitempty"`
	Input                      string               `json:"input"`
	References                 []EntityReference    `json:"references,omitempty"`
	Observations               []Observation        `json:"observations,omitempty"`
	ToolCallHistory            []ToolCallHistory    `json:"toolCallHistory,omitempty"`
	Capabilities               []CapabilityIdentity `json:"capabilities,omitempty"`
}

type CapabilityIdentity struct {
	OperationID string `json:"operationId"`
	Revision    string `json:"catalogRevision"`
}

type EntityReference struct {
	OriginalToken   string         `json:"originalToken"`
	Kind            string         `json:"kind"`
	CanonicalField  string         `json:"canonicalField"`
	CanonicalValue  string         `json:"canonicalValue"`
	URI             string         `json:"uri,omitempty"`
	DisplayName     string         `json:"displayName"`
	Provider        string         `json:"provider"`
	Source          string         `json:"source"`
	Selection       string         `json:"selectionMethod"`
	CatalogRevision string         `json:"catalogRevision"`
	ContextRevision string         `json:"contextRevision,omitempty"`
	Evidence        []EvidenceItem `json:"evidence,omitempty"`
}

type EvidenceItem struct {
	Source string `json:"source"`
	Field  string `json:"field"`
	Value  string `json:"value"`
}

type Observation struct {
	Kind           string `json:"kind"`
	CanonicalField string `json:"canonicalField"`
	CanonicalValue string `json:"canonicalValue"`
	DisplayName    string `json:"displayName,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Source         string `json:"source"`
	Revision       string `json:"revision"`
	Value          any    `json:"value,omitempty"`
}

type ToolCallHistory struct {
	CallID    string `json:"callId"`
	ToolName  string `json:"toolName"`
	TurnID    string `json:"turnId"`
	Arguments any    `json:"arguments"`
	Status    string `json:"status"`
}

type Proposal struct {
	ProposalID       string            `json:"proposalId"`
	Kind             string            `json:"kind"` // command or host-plan.v1
	Capability       string            `json:"capability,omitempty"`
	Arguments        map[string]any    `json:"arguments,omitempty"`
	Plan             any               `json:"plan,omitempty"`
	References       []EntityReference `json:"references,omitempty"`
	CatalogRevision  string            `json:"catalogRevision"`
	Effect           string            `json:"effect,omitempty"`
	RequiresApproval bool              `json:"requiresApproval"`
}

type Event struct {
	SessionID       string `json:"sessionId"`
	TurnID          string `json:"turnId"`
	Sequence        int    `json:"sequence"`
	Kind            string `json:"kind"`
	CatalogRevision string `json:"catalogRevision"`
	ContextRevision string `json:"contextRevision,omitempty"`
	CreatedAt       string `json:"createdAt"`
	Payload         any    `json:"payload,omitempty"`
}

var eventKinds = map[string]bool{
	"turn.started":           true,
	"command.proposed":       true,
	"approval.required":      true,
	"clarification.required": true,
	"tool.call":              true,
	"tool.result":            true,
	"setup.node.started":     true,
	"setup.node.completed":   true,
	"setup.node.failed":      true,
	"turn.completed":         true,
	"error":                  true,
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" || strings.TrimSpace(r.TurnID) == "" || strings.TrimSpace(r.CatalogRevision) == "" {
		return fmt.Errorf("sessionId, turnId, and catalogRevision are required")
	}
	if strings.TrimSpace(r.Input) == "" {
		return fmt.Errorf("input is required")
	}
	if len(r.ContractVersions) == 0 {
		return fmt.Errorf("at least one contract version is required")
	}
	if len(r.ContractVersions) > 8 || len(r.References) > 32 || len(r.Observations) > 128 || len(r.Capabilities) > 256 {
		return fmt.Errorf("session envelope exceeds a bounded collection limit")
	}
	compatible := false
	for _, version := range r.ContractVersions {
		if version == ContractVersion {
			compatible = true
		}
	}
	if !compatible {
		return fmt.Errorf("unsupported assistant session contract")
	}
	if len(r.ToolCallHistory) > MaxHistoryEntries {
		return fmt.Errorf("tool call history exceeds %d entries", MaxHistoryEntries)
	}
	for index, entry := range r.ToolCallHistory {
		if strings.TrimSpace(entry.CallID) == "" || strings.TrimSpace(entry.ToolName) == "" || strings.TrimSpace(entry.TurnID) == "" {
			return fmt.Errorf("tool call history entry %d is missing identity", index)
		}
		if entry.Status != "success" && entry.Status != "error" {
			return fmt.Errorf("tool call history entry %d has unsupported status %q", index, entry.Status)
		}
	}
	for index, reference := range r.References {
		if err := reference.validate(index); err != nil {
			return err
		}
		if reference.CatalogRevision != r.CatalogRevision {
			return fmt.Errorf("entity reference %d is stale for catalog revision %q", index, r.CatalogRevision)
		}
	}
	for index, observation := range r.Observations {
		if strings.TrimSpace(observation.Kind) == "" || strings.TrimSpace(observation.CanonicalField) == "" || strings.TrimSpace(observation.CanonicalValue) == "" || strings.TrimSpace(observation.Source) == "" || strings.TrimSpace(observation.Revision) == "" {
			return fmt.Errorf("observation %d is missing typed provenance", index)
		}
	}
	for index, capability := range r.Capabilities {
		if strings.TrimSpace(capability.OperationID) == "" || strings.TrimSpace(capability.Revision) == "" {
			return fmt.Errorf("capability identity %d is missing operationId or catalogRevision", index)
		}
		if capability.Revision != r.CatalogRevision {
			return fmt.Errorf("capability identity %d is stale for catalog revision %q", index, r.CatalogRevision)
		}
	}
	return boundedJSON(r)
}

func (p Proposal) Validate(catalogRevision string) error {
	if strings.TrimSpace(p.ProposalID) == "" || strings.TrimSpace(p.CatalogRevision) == "" {
		return fmt.Errorf("proposalId and catalogRevision are required")
	}
	if p.CatalogRevision != catalogRevision {
		return fmt.Errorf("proposal catalog revision %q is stale; current is %q", p.CatalogRevision, catalogRevision)
	}
	if p.Kind != "command" && p.Kind != ContractVersion {
		return fmt.Errorf("unsupported proposal kind %q", p.Kind)
	}
	if p.Kind == "command" && strings.TrimSpace(p.Capability) == "" {
		return fmt.Errorf("command proposal capability is required")
	}
	if p.Kind == ContractVersion && p.Plan == nil {
		return fmt.Errorf("host-plan proposal must include a plan")
	}
	if p.Effect != "" && p.Effect != "read" && p.Effect != "mutation" && p.Effect != "destructive" && p.Effect != "credential_bearing" {
		return fmt.Errorf("unsupported proposal effect %q", p.Effect)
	}
	if len(p.References) > 32 {
		return fmt.Errorf("proposal references exceed %d entries", 32)
	}
	for index, reference := range p.References {
		if err := reference.validate(index); err != nil {
			return err
		}
	}
	return boundedJSON(p)
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.SessionID) == "" || strings.TrimSpace(e.TurnID) == "" || strings.TrimSpace(e.CatalogRevision) == "" {
		return fmt.Errorf("event session, turn, and catalog revision are required")
	}
	if e.Sequence < 0 || e.Sequence >= MaxEvents {
		return fmt.Errorf("event sequence is outside the bounded range")
	}
	if !eventKinds[e.Kind] {
		return fmt.Errorf("unsupported session event %q", e.Kind)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.CreatedAt); err != nil {
		return fmt.Errorf("event createdAt is not RFC3339: %w", err)
	}
	return boundedJSON(e)
}

func (reference EntityReference) validate(index int) error {
	if strings.TrimSpace(reference.OriginalToken) == "" || strings.TrimSpace(reference.Kind) == "" || strings.TrimSpace(reference.CanonicalField) == "" || strings.TrimSpace(reference.CanonicalValue) == "" || strings.TrimSpace(reference.Provider) == "" || strings.TrimSpace(reference.Source) == "" || strings.TrimSpace(reference.Selection) == "" || strings.TrimSpace(reference.CatalogRevision) == "" {
		return fmt.Errorf("entity reference %d is missing typed provenance", index)
	}
	if reference.URI != "" {
		if reference.CanonicalField != "uri" || reference.CanonicalValue != reference.URI {
			return fmt.Errorf("entity reference %d canonical URI is not authoritative", index)
		}
	}
	switch reference.Selection {
	case "exact_canonical", "exact_alias", "prefix", "normalized", "semantic", "explicit":
	default:
		return fmt.Errorf("entity reference %d has unsupported selection method %q", index, reference.Selection)
	}
	if len(reference.Evidence) > 16 {
		return fmt.Errorf("entity reference %d exceeds evidence limit", index)
	}
	for evidenceIndex, evidence := range reference.Evidence {
		if strings.TrimSpace(evidence.Source) == "" || strings.TrimSpace(evidence.Field) == "" || strings.TrimSpace(evidence.Value) == "" {
			return fmt.Errorf("entity reference %d evidence %d is incomplete", index, evidenceIndex)
		}
	}
	return nil
}

func NewEvent(sessionID, turnID, catalogRevision string, sequence int, kind string, payload any) Event {
	return Event{SessionID: sessionID, TurnID: turnID, Sequence: sequence, Kind: kind, CatalogRevision: catalogRevision, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}

func boundedJSON(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode session contract: %w", err)
	}
	if len(encoded) > MaxPayloadBytes {
		return fmt.Errorf("session payload exceeds %d bytes", MaxPayloadBytes)
	}
	return nil
}
