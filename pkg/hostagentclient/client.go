package hostagentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
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

func (s CatalogSnapshot) Find(name string) (CapabilityDescriptor, bool) {
	name = strings.TrimSpace(name)
	for _, tool := range s.Tools {
		if tool.Name == name || tool.OperationID == name {
			return tool, true
		}
	}
	return CapabilityDescriptor{}, false
}

type OperationSnapshot struct {
	ID            string         `json:"operationId"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"statusMessage,omitempty"`
	Result        map[string]any `json:"result,omitempty"`
	Error         string         `json:"error,omitempty"`
}

type Client struct {
	mu     sync.RWMutex
	client mcphttp.Client
}

func Connect(ctx context.Context, endpoint, token string) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "http://127.0.0.1:3014/mcp"
	}
	client := &Client{client: mcphttp.Client{
		Endpoint:   endpoint,
		Token:      token,
		Name:       "opute-host-agent-client",
		Version:    "0.1.0",
		HTTPClient: &http.Client{Transport: authTransport{base: http.DefaultTransport, token: token}},
	}}
	if _, err := client.client.Call(ctx, "server/discover", "", map[string]any{}); err != nil {
		return nil, fmt.Errorf("connect host agent: %w", err)
	}
	return client, nil
}

func (c *Client) Close() error { return nil }

func (c *Client) Reconnect(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("client is not reconnectable")
	}
	_, err := c.client.Call(ctx, "server/discover", "", map[string]any{})
	return err
}

func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	listed, err := c.client.Call(ctx, "tools/list", "", map[string]any{})
	if err != nil {
		return nil, err
	}
	raw, _ := listed["tools"].([]any)
	tools := make([]*mcp.Tool, 0, len(raw))
	for _, item := range raw {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		var tool mcp.Tool
		if err := json.Unmarshal(encoded, &tool); err != nil {
			return nil, err
		}
		tools = append(tools, &tool)
	}
	return tools, nil
}

func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	if c == nil {
		return nil, fmt.Errorf("host agent client is nil")
	}
	return c.client.CallTool(ctx, name, arguments)
}

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
