package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbedTextsUsesConfiguredLocalEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer local-token" {
			t.Fatalf("missing local authorization")
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "local-embed" || len(body.Input) != 2 {
			t.Fatalf("request = %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"model": body.Model,
			"data": []map[string]any{
				{"index": 0, "embedding": []float64{1, 0}},
				{"index": 1, "embedding": []float64{0, 1}},
			},
		})
	}))
	defer server.Close()
	t.Setenv("OPUTE_EMBEDDING_BASE_URL", server.URL)
	t.Setenv("OPUTE_EMBEDDING_MODEL", "local-embed")
	t.Setenv("OPUTE_EMBEDDING_TOKEN", "local-token")

	result, err := (&HostOperationsService{}).EmbedTexts(context.Background(), EmbedTextsArgs{Texts: []string{"label", "resource-id"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Dimensions != 2 || len(result.Embeddings) != 2 || result.Model != "local-embed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestEmbedTextsRejectsNonLocalEndpoint(t *testing.T) {
	t.Setenv("OPUTE_EMBEDDING_BASE_URL", "https://embedding.example.test")
	t.Setenv("OPUTE_EMBEDDING_MODEL", "local-embed")
	_, err := (&HostOperationsService{}).EmbedTexts(context.Background(), EmbedTextsArgs{Texts: []string{"label"}})
	if err == nil {
		t.Fatal("expected non-local endpoint to be rejected")
	}
}
