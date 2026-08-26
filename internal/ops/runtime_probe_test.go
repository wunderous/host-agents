package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"http://127.0.0.1:11434", "http://127.0.0.1:11434/v1"},
		{"http://127.0.0.1:11434/v1/", "http://127.0.0.1:11434/v1"},
	} {
		got, err := normalizeOpenAIBaseURL(test.input)
		if err != nil || got != test.want {
			t.Fatalf("normalizeOpenAIBaseURL(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	if _, err := normalizeOpenAIBaseURL("file:///tmp/model"); err == nil {
		t.Fatal("file URL should be rejected")
	}
}

func TestProbeOpenAICompatibleServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M"}]}`))
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"READY\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	service := &HostOperationsService{}
	result, err := service.ProbeOpenAICompatibleServer(context.Background(), ProbeOpenAICompatibleArgs{
		Endpoint:    server.URL + "/v1",
		ModelRef:    "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M",
		IncludeChat: true,
		BearerToken: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || !result.ChatReady || !result.StreamingChatReady || result.ModelRef != "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M" {
		t.Fatalf("unexpected runtime observation: %+v", result)
	}
}

func TestProbeOpenAICompatibleServerDoesNotReturnBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model"}]}`))
	}))
	defer server.Close()
	result, err := (&HostOperationsService{}).ProbeOpenAICompatibleServer(context.Background(), ProbeOpenAICompatibleArgs{Endpoint: server.URL, BearerToken: strings.Repeat("s", 32)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), strings.Repeat("s", 32)) {
		t.Fatal("runtime observation leaked bearer token")
	}
}

func TestProbeOpenAICompatibleServerRejectsReasoningOnlyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"model"}]}`))
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"thinking\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	result, err := (&HostOperationsService{}).ProbeOpenAICompatibleServer(context.Background(), ProbeOpenAICompatibleArgs{
		Endpoint:    server.URL,
		ModelRef:    "model",
		IncludeChat: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StreamingChatReady || result.ChatReady {
		t.Fatalf("reasoning-only stream was reported ready: %+v", result)
	}
}
