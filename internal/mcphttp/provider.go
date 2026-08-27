package mcphttp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// WrapProviderHandler serves a modern-only Streamable HTTP surface in front of
// a go-sdk MCP server so provider processes do not require initialize.
func WrapProviderHandler(inner http.Handler, serverInfo map[string]any) http.Handler {
	if serverInfo == nil {
		serverInfo = map[string]any{"name": "opute-provider", "version": "1.0.0"}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodDelete {
			if r.URL.Path == "/mcp" || r.URL.Path == "/" || r.URL.Path == "" {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
		}
		if r.Method != http.MethodPost {
			inner.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		var envelope struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &envelope) != nil {
			inner.ServeHTTP(w, r)
			return
		}
		if envelope.Method == "initialize" || envelope.Method == "notifications/initialized" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"error":   map[string]any{"code": -32601, "message": "Method not found: " + envelope.Method, "data": map[string]any{"supported": []string{"2026-07-28"}}},
			})
			return
		}
		if envelope.Method == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"result": map[string]any{
					"resultType":        "complete",
					"supportedVersions": []string{"2026-07-28"},
					"capabilities": map[string]any{
						"tools":      map[string]any{},
						"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}},
					},
					"_meta": map[string]any{"io.modelcontextprotocol/serverInfo": serverInfo},
				},
			})
			return
		}
		inner.ServeHTTP(w, r)
	})
}
