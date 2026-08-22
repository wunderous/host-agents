package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/session"
)

// PlatformAssistant is deliberately a transport adapter, not an inference
// runtime. Platform owns intent, authorization, retrieval, and model state;
// this client sends explicit messages/session evidence and renders SSE.
type PlatformAssistant struct {
	URL      string
	Token    string
	HTTP     *http.Client
	messages []platformMessage
}

type platformMessage struct {
	ID    string             `json:"id"`
	Role  string             `json:"role"`
	Parts []platformTextPart `json:"parts"`
}

type platformTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type PlatformTraceEvent struct {
	Kind   string
	Status string
	Label  string
	Detail string
	Data   map[string]any
}

type PlatformChatTurn struct {
	Text        string
	Trace       []PlatformTraceEvent
	ToolHistory []session.ToolCallHistory
}

func PlatformAssistantFromEnvironment() *PlatformAssistant {
	url := strings.TrimSpace(getenv("OPUTE_TUI_PLATFORM_CHAT_URL"))
	if url == "" {
		base := strings.TrimRight(strings.TrimSpace(getenv("OPUTE_PLATFORM_WEB_URL")), "/")
		if base != "" {
			url = base + "/api/chat"
		}
	}
	if url == "" {
		return nil
	}
	token := strings.TrimSpace(getenv("OPUTE_TUI_PLATFORM_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(getenv("OPUTE_PLATFORM_SESSION_TOKEN"))
	}
	return &PlatformAssistant{URL: url, Token: token, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (p *PlatformAssistant) Send(ctx context.Context, input string, request session.Request) (PlatformChatTurn, error) {
	if p == nil || strings.TrimSpace(p.URL) == "" {
		return PlatformChatTurn{}, fmt.Errorf("platform chat adapter is not configured")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return PlatformChatTurn{}, fmt.Errorf("platform chat input is empty")
	}
	messages := append([]platformMessage(nil), p.messages...)
	messages = append(messages, platformMessage{
		ID:    request.TurnID + "-user",
		Role:  "user",
		Parts: []platformTextPart{{Type: "text", Text: input}},
	})
	body, err := json.Marshal(map[string]any{
		"messages":         messages,
		"hostAgentSession": request,
	})
	if err != nil {
		return PlatformChatTurn{}, fmt.Errorf("encode platform chat request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return PlatformChatTurn{}, fmt.Errorf("create platform chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if strings.TrimSpace(p.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	response, err := client.Do(req)
	if err != nil {
		return PlatformChatTurn{}, fmt.Errorf("platform chat unavailable: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return PlatformChatTurn{}, fmt.Errorf("read platform chat stream: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return PlatformChatTurn{}, fmt.Errorf("platform chat returned HTTP %d", response.StatusCode)
	}
	turn, err := parsePlatformSSE(raw)
	if err != nil {
		return PlatformChatTurn{}, err
	}
	for index := range turn.ToolHistory {
		turn.ToolHistory[index].TurnID = request.TurnID
	}
	if strings.TrimSpace(turn.Text) != "" {
		messages = append(messages, platformMessage{
			ID:    request.TurnID + "-assistant",
			Role:  "assistant",
			Parts: []platformTextPart{{Type: "text", Text: turn.Text}},
		})
		p.messages = messages
	}
	return turn, nil
}

func parsePlatformSSE(raw []byte) (PlatformChatTurn, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var turn PlatformChatTurn
	var assistantText strings.Builder
	history := make(map[string]session.ToolCallHistory)
	arguments := make(map[string]any)
	toolNames := make(map[string]string)
	for _, block := range strings.Split(text, "\n\n") {
		var dataLines []string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(dataLines) == 0 {
			continue
		}
		data := strings.Join(dataLines, "\n")
		if data == "[DONE]" || data == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}
		switch payload["type"] {
		case "text-delta":
			if delta, ok := payload["delta"].(string); ok {
				assistantText.WriteString(delta)
			}
		case "data-chat-execution-trace":
			trace, ok := nestedMap(payload, "data")
			if !ok {
				continue
			}
			event, ok := nestedMap(trace, "event")
			if !ok {
				continue
			}
			traceEvent := PlatformTraceEvent{
				Kind:   platformStringValue(event["kind"]),
				Status: platformStringValue(event["status"]),
				Label:  platformStringValue(event["label"]),
				Detail: platformStringValue(event["detail"]),
			}
			if eventData, ok := nestedMap(event, "data"); ok {
				traceEvent.Data = eventData
				collectPlatformToolHistory(eventData, history, arguments, toolNames)
			}
			turn.Trace = append(turn.Trace, traceEvent)
		case "error":
			message := platformStringValue(payload["errorText"])
			if message == "" {
				message = "platform chat stream reported an error"
			}
			return PlatformChatTurn{}, fmt.Errorf("%s", message)
		}
	}
	turn.Text = assistantText.String()
	callIDs := make([]string, 0, len(history))
	for callID := range history {
		callIDs = append(callIDs, callID)
	}
	sort.Strings(callIDs)
	for _, callID := range callIDs {
		entry := history[callID]
		turn.ToolHistory = append(turn.ToolHistory, entry)
	}
	if len(turn.Text) == 0 && len(turn.Trace) == 0 {
		return PlatformChatTurn{}, fmt.Errorf("platform chat stream contained no assistant text or execution trace")
	}
	return turn, nil
}

func collectPlatformToolHistory(data map[string]any, history map[string]session.ToolCallHistory, arguments map[string]any, toolNames map[string]string) {
	inputs, _ := data["toolInputs"].([]any)
	for _, raw := range inputs {
		input, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		callID := platformStringValue(input["toolCallId"])
		toolName := platformStringValue(input["toolName"])
		if callID == "" || toolName == "" {
			continue
		}
		toolNames[callID] = toolName
		arguments[callID] = input["args"]
	}
	outputs, _ := data["toolOutputs"].([]any)
	for _, raw := range outputs {
		output, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		callID := platformStringValue(output["toolCallId"])
		if callID == "" {
			continue
		}
		toolName := platformStringValue(output["toolName"])
		if toolName == "" {
			toolName = toolNames[callID]
		}
		if toolName == "" {
			continue
		}
		status := "success"
		if isError, _ := output["isError"].(bool); isError {
			status = "error"
		}
		history[callID] = session.ToolCallHistory{
			CallID:    callID,
			ToolName:  toolName,
			TurnID:    "platform",
			Arguments: arguments[callID],
			Status:    status,
		}
	}
}

func nestedMap(value map[string]any, key string) (map[string]any, bool) {
	nested, ok := value[key].(map[string]any)
	return nested, ok
}

func platformStringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
