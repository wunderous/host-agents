package mcphttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientForwardsResourceDelegationOnlyAsMetadata(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"accepted":true}}`))
	}))
	defer server.Close()

	const token = "opaque.signed.token"
	ctx := WithResourceDelegation(context.Background(), token)
	client := Client{Endpoint: server.URL}
	if _, err := client.Call(ctx, "tools/call", "get_host_info", map[string]any{
		"name": "get_host_info", "arguments": map[string]any{"uri": "host:local"},
	}); err != nil {
		t.Fatal(err)
	}

	params, ok := received["params"].(map[string]any)
	if !ok {
		t.Fatalf("request params = %#v", received["params"])
	}
	meta, ok := params["_meta"].(map[string]any)
	if !ok || meta[ResourceDelegationMetaKey] != token {
		t.Fatalf("request metadata = %#v", params["_meta"])
	}
	arguments, ok := params["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("request arguments = %#v", params["arguments"])
	}
	if _, found := arguments[ResourceDelegationMetaKey]; found {
		t.Fatal("delegation was copied into tool arguments")
	}
}
