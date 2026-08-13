package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	maxEmbeddingBatch = 32
	maxEmbeddingText  = 8192
)

// EmbedTextsArgs is intentionally limited to text. The host owns the
// endpoint, credentials, and model selection; callers cannot redirect the
// agent to an arbitrary embedding provider.
type EmbedTextsArgs struct {
	Texts []string `json:"texts"`
}

type EmbedTextsResult struct {
	Model       string      `json:"model"`
	Dimensions  int         `json:"dimensions"`
	Embeddings  [][]float64 `json:"embeddings"`
	GeneratedAt string      `json:"generatedAt"`
}

type openAIEmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error any `json:"error,omitempty"`
}

func configuredEmbeddingEndpoint() (string, string, string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("OPUTE_EMBEDDING_BASE_URL")), "/")
	model := strings.TrimSpace(os.Getenv("OPUTE_EMBEDDING_MODEL"))
	if base == "" || model == "" {
		return "", "", "", errors.New("host-local embedding endpoint is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" {
		return "", "", "", errors.New("OPUTE_EMBEDDING_BASE_URL must be a valid URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", "", "", errors.New("OPUTE_EMBEDDING_BASE_URL must resolve to the local host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", "", errors.New("OPUTE_EMBEDDING_BASE_URL must use http or https")
	}
	endpoint := base + "/v1/embeddings"
	if strings.HasSuffix(base, "/v1") {
		endpoint = base + "/embeddings"
	}
	return endpoint, model, strings.TrimSpace(os.Getenv("OPUTE_EMBEDDING_TOKEN")), nil
}

// EmbedTexts generates vectors on the host that owns this agent. It is a
// read-only operation and returns no endpoint, token, or provider metadata.
func (s *HostOperationsService) EmbedTexts(ctx context.Context, args EmbedTextsArgs) (*EmbedTextsResult, error) {
	if len(args.Texts) == 0 || len(args.Texts) > maxEmbeddingBatch {
		return nil, fmt.Errorf("texts must contain between 1 and %d items", maxEmbeddingBatch)
	}
	for index, text := range args.Texts {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("texts[%d] must be non-empty", index)
		}
		if len([]rune(text)) > maxEmbeddingText {
			return nil, fmt.Errorf("texts[%d] exceeds %d characters", index, maxEmbeddingText)
		}
	}
	endpoint, model, token, err := configuredEmbeddingEndpoint()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(openAIEmbeddingRequest{Model: model, Input: args.Texts})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding host unavailable: %w", err)
	}
	defer response.Body.Close()
	var decoded openAIEmbeddingResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding host returned HTTP %d", response.StatusCode)
	}
	if len(decoded.Data) != len(args.Texts) {
		return nil, fmt.Errorf("embedding host returned %d vectors for %d texts", len(decoded.Data), len(args.Texts))
	}
	embeddings := make([][]float64, len(decoded.Data))
	dimensions := 0
	for index, item := range decoded.Data {
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("embedding host returned an empty vector at index %d", index)
		}
		if dimensions == 0 {
			dimensions = len(item.Embedding)
		}
		if len(item.Embedding) != dimensions {
			return nil, errors.New("embedding host returned vectors with inconsistent dimensions")
		}
		embeddings[index] = item.Embedding
	}
	return &EmbedTextsResult{
		Model:       firstNonEmptyEmbedding(decoded.Model, model),
		Dimensions:  dimensions,
		Embeddings:  embeddings,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func firstNonEmptyEmbedding(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
