package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/session"
)

type Assistant struct {
	URL   string
	Token string
	HTTP  *http.Client
}

func AssistantFromEnvironment() *Assistant {
	url := strings.TrimSpace(getenv("OPUTE_TUI_MODEL_URL"))
	if url == "" {
		return nil
	}
	return &Assistant{URL: url, Token: strings.TrimSpace(getenv("OPUTE_TUI_MODEL_TOKEN")), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Assistant) Propose(ctx context.Context, input string, request session.Request, catalog any) (session.Proposal, error) {
	if a == nil || strings.TrimSpace(a.URL) == "" {
		return session.Proposal{}, fmt.Errorf("no structured assistant provider is configured")
	}
	body, err := json.Marshal(map[string]any{"input": input, "session": request, "catalog": catalog, "responseFormat": "typed-command-or-host-plan-v1"})
	if err != nil {
		return session.Proposal{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(body))
	if err != nil {
		return session.Proposal{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return session.Proposal{}, fmt.Errorf("assistant provider unavailable: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 64*1024)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return session.Proposal{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return session.Proposal{}, fmt.Errorf("assistant provider returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Proposal session.Proposal `json:"proposal"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return session.Proposal{}, fmt.Errorf("assistant response is not JSON: %w", err)
	}
	proposal := envelope.Proposal
	if proposal.ProposalID == "" {
		if err := json.Unmarshal(responseBody, &proposal); err != nil {
			return session.Proposal{}, fmt.Errorf("assistant response has no typed proposal: %w", err)
		}
	}
	if err := proposal.Validate(request.CatalogRevision); err != nil {
		return session.Proposal{}, err
	}
	return proposal, nil
}

// Kept as a variable to make provider replay tests deterministic without
// changing the production environment boundary.
var getenv = func(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
