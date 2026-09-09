package hostmcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeAuthenticatedMCPEndpointUsesAudienceBoundClientCredentials(t *testing.T) {
	var observedResource string
	var observedBearer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			observedResource = r.Form.Get("resource")
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "opute-mcp-host" {
				t.Fatalf("unexpected token form: %#v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"oha_probe"}`))
		case "/mcp":
			observedBearer = r.Header.Get("Authorization")
			if r.Header.Get("MCP-Protocol-Version") != mcpExposureProtocolVersion || r.Header.Get("Mcp-Method") != "tools/list" {
				t.Fatalf("missing modern MCP headers: %#v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"host-agent-mcp-probe","result":{"tools":[{"name":"get_host_info"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	endpoint := server.URL + "/mcp"
	observation, err := probeAuthenticatedMCPEndpoint(t.Context(), endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if !observation["ready"].(bool) || !observation["authenticated"].(bool) || observation["toolCount"] != 1 {
		t.Fatalf("unexpected observation: %#v", observation)
	}
	if observation["authorizationMode"] != "client-credentials" {
		t.Fatalf("authorization mode = %#v", observation["authorizationMode"])
	}
	if observedResource != endpoint || observedBearer != "Bearer oha_probe" {
		t.Fatalf("audience or bearer mismatch: resource=%q bearer=%q", observedResource, observedBearer)
	}
}

func TestProbeAuthenticatedMCPEndpointAcceptsExplicitBearerWithoutMinting(t *testing.T) {
	tokenRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			tokenRequests++
			http.Error(w, "unexpected token request", http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer oha_explicit" {
			t.Fatalf("unexpected MCP request: path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer server.Close()

	observation, err := probeAuthenticatedMCPEndpoint(t.Context(), server.URL+"/mcp", "oha_explicit")
	if err != nil {
		t.Fatal(err)
	}
	if observation["authorizationMode"] != "provided-bearer" || tokenRequests != 0 {
		t.Fatalf("explicit bearer was not preserved: observation=%#v tokenRequests=%d", observation, tokenRequests)
	}
}

func TestMcpProbeRejectsNonMcpEndpointAndDoesNotLeakCredential(t *testing.T) {
	_, err := probeAuthenticatedMCPEndpoint(t.Context(), "https://example.invalid/health", "oha_secret")
	if err == nil || !strings.Contains(err.Error(), "end with /mcp") || strings.Contains(err.Error(), "oha_secret") {
		t.Fatalf("unexpected endpoint validation error: %v", err)
	}

	encoded, marshalErr := json.Marshal(map[string]any{"error": err.Error()})
	if marshalErr != nil || strings.Contains(string(encoded), "oha_secret") {
		t.Fatalf("credential appeared in probe evidence: %s", encoded)
	}
}
