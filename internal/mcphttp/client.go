package mcphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client is a modern-only Streamable HTTP MCP 2026-07-28 client.
// It never sends initialize or Mcp-Session-Id.
type Client struct {
	Endpoint   string
	Token      string
	HTTPClient *http.Client
	Name       string
	Version    string
}

func (c Client) Call(ctx context.Context, method, name string, params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	meta, err := ModernRequestEnvelope(c.version())
	if err != nil {
		return nil, err
	}
	if c.Name != "" {
		if info, ok := meta["io.modelcontextprotocol/clientInfo"].(map[string]any); ok {
			info["name"] = c.Name
		}
	}
	params["_meta"] = meta
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(c.Endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := ApplyStreamableHTTPRequestHeaders(req); err != nil {
		return nil, err
	}
	req.Header.Set("Mcp-Method", method)
	if method == "tools/call" {
		if err := ApplyToolsCallRequestHeaders(req, name); err != nil {
			return nil, err
		}
	} else if name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	if strings.TrimSpace(c.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("MCP HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(payload)))
	}
	var envelope struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode MCP response: %w", err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if envelope.Result == nil {
		return map[string]any{}, nil
	}
	return envelope.Result, nil
}

func (c Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	result, err := c.Call(ctx, "tools/call", name, map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var toolResult mcp.CallToolResult
	if err := json.Unmarshal(encoded, &toolResult); err != nil {
		return nil, err
	}
	if toolResult.StructuredContent == nil {
		if structured, ok := result["structuredContent"]; ok {
			toolResult.StructuredContent = structured
		}
	}
	return &toolResult, nil
}

func (c Client) version() string {
	if strings.TrimSpace(c.Version) != "" {
		return c.Version
	}
	return "1.0.0"
}
