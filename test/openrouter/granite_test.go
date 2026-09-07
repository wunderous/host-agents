//go:build openrouter

package openrouter_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/domain/llm"
)

const (
	openRouterEndpoint  = "https://openrouter.ai/api/v1"
	defaultGraniteModel = "ibm-granite/granite-4.2-8b"
)

func TestOpenRouterGraniteOpenAICompatibleProbe(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not configured")
	}
	modelRef := strings.TrimSpace(os.Getenv("OPENROUTER_GRANITE_MODEL"))
	if modelRef == "" {
		modelRef = defaultGraniteModel
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := (&llm.Service{}).ProbeOpenAICompatibleServer(ctx, llm.ProbeOpenAICompatibleArgs{
		Endpoint:    openRouterEndpoint,
		ModelRef:    modelRef,
		IncludeChat: true,
		BearerToken: apiKey,
	})
	if err != nil {
		t.Fatalf("probe OpenRouter: %v", err)
	}
	if !result.EndpointReady || !result.OpenAIModelsReady || !result.Ready {
		t.Fatalf("OpenRouter did not advertise configured Granite model %q: %+v", modelRef, result)
	}
	if result.ModelRef != modelRef {
		t.Fatalf("OpenRouter selected model %q, want %q", result.ModelRef, modelRef)
	}
	if !result.ChatReady || !result.StreamingChatReady {
		t.Fatalf("OpenRouter Granite model %q streaming chat is not ready: %+v", modelRef, result)
	}
}
