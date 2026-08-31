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
	openRouterEndpoint = "https://openrouter.ai/api/v1"
	granite41Model     = "ibm/granite4.1:3b"
)

func TestOpenRouterGranite41OpenAICompatibleProbe(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := (&llm.Service{}).ProbeOpenAICompatibleServer(ctx, llm.ProbeOpenAICompatibleArgs{
		Endpoint:    openRouterEndpoint,
		ModelRef:    granite41Model,
		IncludeChat: true,
		BearerToken: apiKey,
	})
	if err != nil {
		t.Fatalf("probe OpenRouter: %v", err)
	}
	if !result.EndpointReady || !result.OpenAIModelsReady || !result.Ready {
		t.Fatalf("OpenRouter did not advertise Granite 4.1: %+v", result)
	}
	if result.ModelRef != granite41Model {
		t.Fatalf("OpenRouter selected model %q, want %q", result.ModelRef, granite41Model)
	}
	if !result.ChatReady || !result.StreamingChatReady {
		t.Fatalf("OpenRouter Granite 4.1 streaming chat is not ready: %+v", result)
	}
}
