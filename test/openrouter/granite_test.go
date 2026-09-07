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
	granite42Model     = "ibm-granite/granite-4.2-8b"
)

func TestOpenRouterGranite42OpenAICompatibleProbe(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := (&llm.Service{}).ProbeOpenAICompatibleServer(ctx, llm.ProbeOpenAICompatibleArgs{
		Endpoint:    openRouterEndpoint,
		ModelRef:    granite42Model,
		IncludeChat: true,
		BearerToken: apiKey,
	})
	if err != nil {
		t.Fatalf("probe OpenRouter: %v", err)
	}
	if !result.EndpointReady || !result.OpenAIModelsReady || !result.Ready {
		t.Fatalf("OpenRouter did not advertise Granite 4.2 model %q: %+v", granite42Model, result)
	}
	if result.ModelRef != granite42Model {
		t.Fatalf("OpenRouter selected model %q, want %q", result.ModelRef, granite42Model)
	}
	if !result.ChatReady || !result.StreamingChatReady {
		t.Fatalf("OpenRouter Granite 4.2 model %q streaming chat is not ready: %+v", granite42Model, result)
	}
}
