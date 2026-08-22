package tui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/session"
	"github.com/wunderous/host-agents/internal/tools"
)

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
		return connectSession(ctx, endpoint, token)
	}
	session, err := connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect host agent: %w", err)
	}
	return &Client{endpoint: endpoint, token: token, session: session, reconnectFn: connect}, nil
}

// ConnectWithTransport connects the TUI to an already-created MCP transport.
// The in-memory SDK transport is used by the single-process standalone mode;
// it preserves the MCP initialize, tools/list, and tools/call boundary without
// opening a loopback listener.
func ConnectWithTransport(ctx context.Context, transport mcp.Transport) (*Client, error) {
	if transport == nil {
		return nil, fmt.Errorf("MCP transport is required")
	}
	session, err := connectMCPClient(ctx, transport)
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
	if session == nil {
		return nil
	}
	return session.Close()
}

func (c *Client) ListTools(ctx context.Context) (*mcp.ListToolsResult, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	return session.ListTools(ctx, nil)
}

func (c *Client) Call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

func connectSession(ctx context.Context, endpoint, token string) (*mcp.ClientSession, error) {
	transport := &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{Transport: authRoundTripper{
			token: token,
			base:  http.DefaultTransport,
		}},
		MaxRetries:           2,
		DisableStandaloneSSE: true,
	}
	return connectMCPClient(ctx, transport)
}

func connectMCPClient(ctx context.Context, transport mcp.Transport) (*mcp.ClientSession, error) {
	return mcp.NewClient(&mcp.Implementation{Name: "opute-host-agent", Version: "0.1.0"}, nil).Connect(ctx, transport, nil)
}

func (c *Client) currentSession() (*mcp.ClientSession, error) {
	if c == nil {
		return nil, fmt.Errorf("MCP client is not connected")
	}
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("MCP client is not connected")
	}
	return session, nil
}

// Reconnect replaces only the MCP transport session. The caller's catalog,
// plan identity, and trace remain client-owned and are therefore available to
// resume a read-only status poll after a transient transport failure.
func (c *Client) Reconnect(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("MCP client is not connected")
	}
	c.mu.RLock()
	connect := c.reconnectFn
	old := c.session
	c.mu.RUnlock()
	if connect == nil {
		return fmt.Errorf("in-process MCP client cannot reconnect its consumed transport")
	}
	newSession, err := connect(ctx)
	if err != nil {
		return fmt.Errorf("reconnect host agent: %w", err)
	}
	c.mu.Lock()
	c.session = newSession
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (c *Client) CapabilityCatalog(ctx context.Context) (tools.CapabilityCatalogSnapshot, error) {
	result, err := c.Call(ctx, "get_capability_catalog", map[string]any{})
	if err != nil {
		return tools.CapabilityCatalogSnapshot{}, err
	}
	if result == nil || result.IsError {
		return tools.CapabilityCatalogSnapshot{}, fmt.Errorf("get_capability_catalog failed: %s", resultText(result))
	}
	return decodeSnapshot(result.StructuredContent)
}

func (c *Client) OpenSession(ctx context.Context, sessionID, catalogRevision string) error {
	result, err := c.Call(ctx, "open_assistant_session", map[string]any{
		"sessionId":                 sessionID,
		"supportedContractVersions": []string{session.ContractVersion},
		"catalogRevision":           catalogRevision,
	})
	if err != nil {
		return err
	}
	if result == nil || result.IsError {
		return fmt.Errorf("open_assistant_session failed: %s", resultText(result))
	}
	return nil
}

func resultText(result *mcp.CallToolResult) string {
	if result == nil {
		return "no result"
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			return text.Text
		}
	}
	return "operation failed"
}

type authRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (r authRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if r.base == nil {
		r.base = http.DefaultTransport
	}
	clone := request.Clone(request.Context())
	if strings.TrimSpace(r.token) != "" {
		clone.Header.Set("Authorization", "Bearer "+r.token)
	}
	return r.base.RoundTrip(clone)
}
