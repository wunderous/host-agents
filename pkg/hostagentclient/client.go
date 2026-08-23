// Package hostagentclient is the public, provider-neutral client boundary for
// Host Agent consumers such as the TUI. It deliberately exposes MCP calls
// and projections without importing any Host Agent internal package.
package hostagentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CapabilityDescriptor struct {
	OperationID       string         `json:"operationId"`
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	InputSchema       map[string]any `json:"inputSchema"`
	OutputSchema      map[string]any `json:"outputSchema,omitempty"`
	Effect            string         `json:"effect"`
	Provider          string         `json:"provider"`
	Implementation    string         `json:"implementation"`
	ResourceKinds     []string       `json:"resourceKinds,omitempty"`
	Idempotent        bool           `json:"idempotent"`
	SupportsReadiness bool           `json:"supportsReadiness"`
}

type CatalogSnapshot struct {
	ProviderID string                 `json:"providerId"`
	Revision   string                 `json:"catalogRevision"`
	Tools      []CapabilityDescriptor `json:"tools"`
}

// Find returns the authoritative descriptor for an operation in this
// catalog. Consumers must use this descriptor before constructing a typed
// command; operation names are not a substitute for the current catalog.
func (s CatalogSnapshot) Find(name string) (CapabilityDescriptor, bool) {
	name = strings.TrimSpace(name)
	for _, tool := range s.Tools {
		if tool.Name == name || tool.OperationID == name {
			return tool, true
		}
	}
	return CapabilityDescriptor{}, false
}

// OperationSnapshot is the neutral durable-operation projection used by
// clients when reconnecting after a lost transport response.
type OperationSnapshot struct {
	ID            string         `json:"operationId"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"statusMessage,omitempty"`
	Result        map[string]any `json:"result,omitempty"`
	Error         string         `json:"error,omitempty"`
}

type Client struct {
	mu          sync.RWMutex
	endpoint    string
	token       string
	session     *mcp.ClientSession
	reconnectFn func(context.Context) (*mcp.ClientSession, error)
}

func Connect(ctx context.Context, endpoint, token string) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "http://127.0.0.1:3014/mcp"
	}
	connect := func(ctx context.Context) (*mcp.ClientSession, error) {
		transport := &mcp.StreamableClientTransport{
			Endpoint: endpoint,
			HTTPClient: &http.Client{Transport: authTransport{base: http.DefaultTransport,
				token: token}},
			MaxRetries:           2,
			DisableStandaloneSSE: true,
		}
		return mcp.NewClient(&mcp.Implementation{Name: "opute-host-agent-client", Version: "0.1.0"}, nil).Connect(ctx, transport, nil)
	}
	session, err := connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect host agent: %w", err)
	}
	return &Client{endpoint: endpoint, token: token, session: session, reconnectFn: connect}, nil
}

// ConnectWithTransport connects a client to an already-created MCP transport.
// It is used by in-process callers such as the CLI while preserving the same
// public, provider-neutral client boundary as the HTTP path.
func ConnectWithTransport(ctx context.Context, transport mcp.Transport) (*Client, error) {
	if transport == nil {
		return nil, fmt.Errorf("MCP transport is required")
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "opute-host-agent-client", Version: "0.1.0"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect host agent: %w", err)
	}
	return &Client{session: session}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	session := c.session
	c.session = nil
	c.mu.Unlock()
	if session != nil {
		return session.Close()
	}
	return nil
}

func (c *Client) Reconnect(ctx context.Context) error {
	if c == nil || c.reconnectFn == nil {
		return fmt.Errorf("client is not reconnectable")
	}
	session, err := c.reconnectFn(ctx)
	if err != nil {
		return fmt.Errorf("reconnect host agent: %w", err)
	}
	c.mu.Lock()
	old := c.session
	c.session = session
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	session, err := c.sessionForCall()
	if err != nil {
		return nil, err
	}
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	session, err := c.sessionForCall()
	if err != nil {
		return nil, err
	}
	return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
}

// OperationStatus reads durable state only. It is intentionally separate from
// Call so reconnect logic cannot accidentally replay the original mutation.
func (c *Client) OperationStatus(ctx context.Context, operationID string) (*mcp.CallToolResult, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil, fmt.Errorf("operation id is required")
	}
	return c.Call(ctx, "get_operation", map[string]any{"operationId": operationID})
}

func (c *Client) CancelOperation(ctx context.Context, operationID string) (*mcp.CallToolResult, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil, fmt.Errorf("operation id is required")
	}
	return c.Call(ctx, "cancel_operation", map[string]any{"operationId": operationID})
}

func (c *Client) Catalog(ctx context.Context) (CatalogSnapshot, error) {
	result, err := c.Call(ctx, "get_capability_catalog", map[string]any{})
	if err != nil {
		return CatalogSnapshot{}, err
	}
	if result == nil || result.IsError || result.StructuredContent == nil {
		return CatalogSnapshot{}, fmt.Errorf("get_capability_catalog failed")
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	var snapshot CatalogSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return CatalogSnapshot{}, err
	}
	if snapshot.Revision == "" {
		return CatalogSnapshot{}, fmt.Errorf("host agent returned an empty catalog revision")
	}
	return snapshot, nil
}

func (c *Client) sessionForCall() (*mcp.ClientSession, error) {
	if c == nil {
		return nil, fmt.Errorf("host agent client is nil")
	}
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("host agent client is disconnected")
	}
	return session, nil
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t authTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := request.Clone(request.Context())
	if strings.TrimSpace(t.token) != "" {
		clone.Header.Set("Authorization", "Bearer "+t.token)
	}
	return base.RoundTrip(clone)
}
