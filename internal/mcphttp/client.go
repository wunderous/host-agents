package mcphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	if taskID, ok := taskHandle(result); ok {
		result, err = c.waitTask(ctx, taskID, taskPollInterval(result))
		if err != nil {
			return nil, err
		}
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

// taskHandle recognizes the flat MCP Tasks response returned by a
// task-capable Host Agent tool. Provider callbacks are ordinary MCP clients,
// so they must not treat the task handle as the completed tool result: doing
// so lets dependent callbacks race the reservation held by the task.
func taskHandle(result map[string]any) (string, bool) {
	if result == nil {
		return "", false
	}
	resultType, _ := result["resultType"].(string)
	taskID, _ := result["taskId"].(string)
	return strings.TrimSpace(taskID), resultType == "task" && strings.TrimSpace(taskID) != ""
}

func taskPollInterval(result map[string]any) time.Duration {
	if result != nil {
		switch value := result["pollIntervalMs"].(type) {
		case float64:
			if value >= 1 && value <= 60_000 {
				return time.Duration(value) * time.Millisecond
			}
		case int:
			if value >= 1 && value <= 60_000 {
				return time.Duration(value) * time.Millisecond
			}
		}
	}
	return 500 * time.Millisecond
}

func (c Client) waitTask(ctx context.Context, taskID string, pollInterval time.Duration) (map[string]any, error) {
	for {
		// The Host Agent's modern transport binds the task identifier to
		// Mcp-Name for tasks/get, just as tools/call binds the tool name.
		statusResult, err := c.Call(ctx, "tasks/get", taskID, map[string]any{"taskId": taskID})
		if err != nil {
			return nil, fmt.Errorf("get host task %s: %w", taskID, err)
		}
		status, _ := statusResult["status"].(string)
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "completed":
			result, ok := statusResult["result"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("host task %s completed without a tool result", taskID)
			}
			return result, nil
		case "failed", "cancelled", "input_required":
			return nil, fmt.Errorf("host task %s %s: %s", taskID, status, taskStatusMessage(statusResult))
		}
		if err := waitForTaskPoll(ctx, pollInterval); err != nil {
			return nil, fmt.Errorf("wait for host task %s: %w", taskID, err)
		}
	}
}

func waitForTaskPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func taskStatusMessage(result map[string]any) string {
	if message, ok := result["statusMessage"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if detail, ok := result["error"].(map[string]any); ok {
		if message, ok := detail["message"].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	}
	return "no diagnostic"
}

func (c Client) version() string {
	if strings.TrimSpace(c.Version) != "" {
		return c.Version
	}
	return "1.0.0"
}
