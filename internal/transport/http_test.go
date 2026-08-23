package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateModernExtensionRequestRequiresMatchingMetadataAndHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	req.Header.Set("MCP-Protocol-Version", modernMCPVersion)
	raw := []byte(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}`)
	if err := validateModernExtensionRequest(req, raw); err != nil {
		t.Fatalf("valid modern request rejected: %v", err)
	}

	missingHeader := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	if err := validateModernExtensionRequest(missingHeader, raw); err == nil {
		t.Fatal("missing MCP-Protocol-Version header accepted")
	}

	unsupported := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	unsupported.Header.Set("MCP-Protocol-Version", "1900-01-01")
	unsupportedRaw := []byte(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01","io.modelcontextprotocol/clientInfo":{},"io.modelcontextprotocol/clientCapabilities":{}}}`)
	err := validateModernExtensionRequest(unsupported, unsupportedRaw)
	protocolErr, ok := err.(*protocolRequestError)
	if !ok || protocolErr.code != -32022 {
		t.Fatalf("unsupported version error = %#v", err)
	}
}
