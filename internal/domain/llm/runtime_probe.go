package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProbeOpenAICompatibleArgs describes the runtime-neutral serving contract
// used by recipes and externally managed inference servers.
type ProbeOpenAICompatibleArgs struct {
	Endpoint    string
	ModelRef    string
	IncludeChat bool
	BearerToken string
}

// RuntimeObservation is deliberately independent of a vendor runtime. Native
// vendor diagnostics belong in a recipe-specific plan, not this common probe.
type RuntimeObservation struct {
	ServingContract    string         `json:"servingContract"`
	APIBaseURL         string         `json:"apiBaseUrl"`
	ModelRef           string         `json:"modelRef,omitempty"`
	Models             []RuntimeModel `json:"models"`
	Ready              bool           `json:"ready"`
	EndpointReady      bool           `json:"endpointReady,omitempty"`
	ChatReady          bool           `json:"chatReady,omitempty"`
	StreamingChatReady bool           `json:"streamingChatReady,omitempty"`
	OpenAIModelsReady  bool           `json:"openAiModelsReady,omitempty"`
	LoadError          string         `json:"loadError,omitempty"`
	RemediationHints   []string       `json:"remediationHints,omitempty"`
}

type RuntimeModel struct {
	Name string `json:"name"`
}

func (s *Service) ProbeOpenAICompatibleServer(ctx context.Context, args ProbeOpenAICompatibleArgs) (*RuntimeObservation, error) {
	base, err := normalizeOpenAIBaseURL(args.Endpoint)
	if err != nil {
		return nil, err
	}
	result := &RuntimeObservation{
		ServingContract: "openai-chat.v1",
		APIBaseURL:      base,
		ModelRef:        strings.TrimSpace(args.ModelRef),
		Models:          []RuntimeModel{},
	}
	// Model discovery is usually immediate, but a first streaming generation
	// on a local reasoning-capable model can legitimately take longer. Keep a
	// bounded deadline without turning normal cold-start latency into a false
	// activation failure.
	client := &http.Client{Timeout: 60 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	setBearer(request, args.BearerToken)
	response, err := client.Do(request)
	if err != nil {
		result.LoadError = err.Error()
		return result, nil
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	response.Body.Close()
	if readErr != nil {
		result.LoadError = readErr.Error()
		return result, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.LoadError = fmt.Sprintf("model discovery returned HTTP %d", response.StatusCode)
		return result, nil
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &models); err != nil {
		result.LoadError = fmt.Sprintf("decode model discovery response: %v", err)
		return result, nil
	}
	for _, model := range models.Data {
		if strings.TrimSpace(model.ID) != "" {
			result.Models = append(result.Models, RuntimeModel{Name: model.ID})
		}
	}
	result.EndpointReady = true
	result.OpenAIModelsReady = true
	result.Ready = len(result.Models) > 0
	if result.ModelRef == "" && len(result.Models) > 0 {
		result.ModelRef = result.Models[0].Name
	}
	if result.ModelRef != "" {
		matched := false
		for _, model := range result.Models {
			if runtimeModelMatches(model.Name, result.ModelRef) {
				matched = true
				result.ModelRef = model.Name
				break
			}
		}
		if !matched {
			result.Ready = false
			result.RemediationHints = append(result.RemediationHints, "make the requested model available at the configured endpoint")
		}
	}
	if args.IncludeChat && result.Ready {
		result.StreamingChatReady = probeStreamingChat(ctx, client, base, result.ModelRef, args.BearerToken)
		result.ChatReady = result.StreamingChatReady
		if !result.ChatReady {
			result.RemediationHints = append(result.RemediationHints, "verify the endpoint supports streaming /v1/chat/completions")
		}
	}
	return result, nil
}

func normalizeOpenAIBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("endpoint scheme must be http or https")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/v1") {
		parsed.Path += "/v1"
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func runtimeModelMatches(left, right string) bool {
	left = strings.TrimSpace(strings.TrimSuffix(left, ":latest"))
	right = strings.TrimSpace(strings.TrimSuffix(right, ":latest"))
	return left != "" && right != "" && (left == right || strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left))
}

func setBearer(request *http.Request, token string) {
	if token = strings.TrimSpace(token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

func probeStreamingChat(ctx context.Context, client *http.Client, base, model, token string) bool {
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Reply with the single word READY.",
		}},
		"stream": true,
		// Keep enough budget for reasoning-capable models to reach their
		// assistant answer. A probe that only observes reasoning tokens is not
		// evidence that the serving contract can produce chat output.
		"max_tokens":  256,
		"temperature": 0,
		// OpenAI-compatible reasoning-capable runtimes may otherwise spend the
		// entire bounded probe budget on hidden reasoning and never emit an
		// assistant message. "none" is a neutral request hint; runtimes that
		// do not support reasoning simply ignore the field.
		"reasoning_effort": "none",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	setBearer(request, token)
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 2*1024*1024))
	scanner.Buffer(make([]byte, 4096), 256*1024)
	gotContent := false
	completed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			completed = true
			continue
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) == nil {
			if _, ok := chunk["error"]; ok {
				return false
			}
			choices, _ := chunk["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			if finishReason, _ := choice["finish_reason"].(string); finishReason != "" {
				completed = true
			}
			delta, _ := choice["delta"].(map[string]any)
			if content, _ := delta["content"].(string); strings.TrimSpace(content) != "" {
				gotContent = true
			}
		}
	}
	return gotContent && completed && scanner.Err() == nil
}
