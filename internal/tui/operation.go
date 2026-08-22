package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PollOptions bounds status polling and keeps the renderer independent from
// the transport. The callback receives redacted server state and must not
// mutate the MCP client.
type PollOptions struct {
	Timeout  time.Duration
	Interval time.Duration
	OnUpdate func(PollSnapshot)
}

type PollSnapshot struct {
	ID      string
	Status  string
	Message string
	Raw     map[string]any
}

func defaultPollOptions(options PollOptions) PollOptions {
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Minute
	}
	if options.Interval <= 0 {
		options.Interval = 500 * time.Millisecond
	}
	if options.Interval > 10*time.Second {
		options.Interval = 10 * time.Second
	}
	return options
}

// CallForStatus retries only a status request after a transport failure. The
// original command is never replayed, so reconnect cannot duplicate a
// mutation whose response was lost.
func (c *Client) CallForStatus(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := c.Call(ctx, name, args)
	if err == nil || !isTransportError(err) {
		return result, err
	}
	if reconnectErr := c.Reconnect(ctx); reconnectErr != nil {
		return nil, fmt.Errorf("status request failed and reconnect failed: %w", reconnectErr)
	}
	return c.Call(ctx, name, args)
}

// WaitOperation follows the standalone get_operation contract. It is used
// only after the initial tool call returned an operation/task identity.
func (c *Client) WaitOperation(ctx context.Context, operationID string, options PollOptions) (*mcp.CallToolResult, error) {
	options = defaultPollOptions(options)
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil, fmt.Errorf("operationId is required")
	}
	deadline := time.Now().Add(options.Timeout)
	for {
		if err := contextErrorBefore(deadline, ctx); err != nil {
			return nil, err
		}
		result, err := c.CallForStatus(ctx, "get_operation", map[string]any{"operationId": operationID})
		if err != nil {
			return nil, err
		}
		value, err := structuredMap(result)
		if err != nil {
			return nil, fmt.Errorf("decode operation %s status: %w", operationID, err)
		}
		snapshot := PollSnapshot{
			ID:      operationID,
			Status:  stringValue(value["status"]),
			Message: firstString(value, "statusMessage", "description", "error"),
			Raw:     value,
		}
		if options.OnUpdate != nil {
			options.OnUpdate(snapshot)
		}
		switch snapshot.Status {
		case "completed", "succeeded":
			if nested, ok := value["result"].(map[string]any); ok {
				return callToolResultFromTaskResult(nested), nil
			}
			return result, nil
		case "failed", "cancelled", "unknown", "input_required":
			message := snapshot.Message
			if message == "" {
				message = "operation ended without a successful result"
			}
			return nil, fmt.Errorf("operation %s ended with status %s: %s", operationID, snapshot.Status, message)
		}
		if err := sleepUntil(ctx, deadline, options.Interval); err != nil {
			return nil, err
		}
	}
}

// WaitPlanRun follows the durable host-plan.v1 record. Unlike an ordinary
// task, a plan has its own run identity and status endpoint.
func (c *Client) WaitPlanRun(ctx context.Context, runID string, options PollOptions) (*mcp.CallToolResult, error) {
	options = defaultPollOptions(options)
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("runId is required")
	}
	deadline := time.Now().Add(options.Timeout)
	for {
		if err := contextErrorBefore(deadline, ctx); err != nil {
			return nil, err
		}
		result, err := c.CallForStatus(ctx, "get_host_plan_run", map[string]any{"runId": runID})
		if err != nil {
			return nil, err
		}
		value, err := structuredMap(result)
		if err != nil {
			return nil, fmt.Errorf("decode plan %s status: %w", runID, err)
		}
		snapshot := PollSnapshot{
			ID:      runID,
			Status:  stringValue(value["status"]),
			Message: firstString(value, "error", "statusMessage"),
			Raw:     value,
		}
		if options.OnUpdate != nil {
			options.OnUpdate(snapshot)
		}
		switch snapshot.Status {
		case "completed", "succeeded":
			return result, nil
		case "failed", "cancelled", "unknown":
			message := snapshot.Message
			if message == "" {
				message = "plan did not complete successfully"
			}
			return nil, fmt.Errorf("plan %s ended with status %s: %s", runID, snapshot.Status, message)
		}
		if err := sleepUntil(ctx, deadline, options.Interval); err != nil {
			return nil, err
		}
	}
}

func contextErrorBefore(deadline time.Time, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Now().After(deadline) {
		return fmt.Errorf("operation polling deadline exceeded")
	}
	return nil
}

func sleepUntil(ctx context.Context, deadline time.Time, interval time.Duration) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return fmt.Errorf("operation polling deadline exceeded")
	}
	if interval > remaining {
		interval = remaining
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func structuredMap(result *mcp.CallToolResult) (map[string]any, error) {
	if result == nil {
		return nil, fmt.Errorf("empty MCP result")
	}
	if result.IsError {
		return nil, fmt.Errorf("MCP call returned an error: %s", resultText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil || value == nil {
		if err == nil {
			err = fmt.Errorf("structured content is not an object")
		}
		return nil, err
	}
	return value, nil
}

func callToolResultFromTaskResult(value map[string]any) *mcp.CallToolResult {
	result := &mcp.CallToolResult{}
	if structured, ok := value["structuredContent"]; ok {
		result.StructuredContent = structured
	}
	if isError, ok := value["isError"].(bool); ok {
		result.IsError = isError
	}
	return result
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := stringValue(value[key]); text != "" {
			return text
		}
	}
	return ""
}

func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"eof", "closed", "connection reset", "connection refused", "broken pipe", "transport", "stream"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
