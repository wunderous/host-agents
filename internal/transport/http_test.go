package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateModernExtensionRequestRequiresMatchingMetadataAndHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	req.Header.Set("MCP-Protocol-Version", modernMCPVersion)
	raw := []byte(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}`)
	if err := validateModernExtensionRequest(req, "server/discover", raw); err != nil {
		t.Fatalf("valid modern request rejected: %v", err)
	}

	missingHeader := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	if err := validateModernExtensionRequest(missingHeader, "server/discover", raw); err == nil {
		t.Fatal("missing MCP-Protocol-Version header accepted")
	}

	unsupported := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	unsupported.Header.Set("MCP-Protocol-Version", "1900-01-01")
	unsupportedRaw := []byte(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01","io.modelcontextprotocol/clientInfo":{},"io.modelcontextprotocol/clientCapabilities":{}}}`)
	err := validateModernExtensionRequest(unsupported, "server/discover", unsupportedRaw)
	protocolErr, ok := err.(*protocolRequestError)
	if !ok || protocolErr.code != -32022 {
		t.Fatalf("unsupported version error = %#v", err)
	}
}

func TestNormalizeTaskCreationResponseLiftsFlatTaskEnvelope(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "queued"}},
			"structuredContent": map[string]any{
				"resultType":     "task",
				"taskId":         "task-1",
				"status":         "working",
				"createdAt":      "2026-08-23T00:00:00Z",
				"lastUpdatedAt":  "2026-08-23T00:00:00Z",
				"ttlMs":          60_000,
				"pollIntervalMs": 3_000,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(normalizeTaskCreationResponse(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	result := envelope["result"].(map[string]any)
	if result["resultType"] != "task" || result["taskId"] != "task-1" {
		t.Fatalf("normalized result = %#v", result)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("structuredContent leaked into normative task result: %#v", result)
	}
}
