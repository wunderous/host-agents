package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	"github.com/wunderous/host-agents/internal/mcphttp"
)

func TestTailscaleMCPWireValidateAndEnroll(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()

	server := newProviderServer()
	handler := mcphttp.WrapProviderHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true}),
		map[string]any{"name": "opute-provider-tailscale", "version": "1.0.0"},
	)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	raw := mcphttp.Client{Endpoint: httpServer.URL, Name: "tailscale-wire-test", Version: "1.0.0"}
	if _, err := raw.Call(ctx, "server/discover", "", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	tools, err := raw.Call(ctx, "tools/list", "", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	encodedTools := mustJSON(t, tools)
	if !strings.Contains(encodedTools, capabilitycontract.NetworkOverlayValidateOperation) {
		t.Fatalf("tools/list missing validate: %s", encodedTools)
	}
	if !strings.Contains(encodedTools, capabilitycontract.NetworkOverlayEnrollOperation) {
		t.Fatalf("tools/list missing enroll: %s", encodedTools)
	}

	validateResult, err := raw.CallTool(ctx, capabilitycontract.NetworkOverlayValidateOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:wire-a", "credentialKind": "auth-key",
	})
	if err != nil || validateResult == nil || validateResult.IsError {
		t.Fatalf("validate wire call failed: %#v err=%v", validateResult, err)
	}
	validateBody := asMap(t, validateResult.StructuredContent)
	if validateBody["ready"] != true {
		t.Fatalf("validate not ready: %#v", validateBody)
	}

	enrollResult, err := raw.CallTool(ctx, capabilitycontract.NetworkOverlayEnrollOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:wire-a", "name": "wire-a",
		"credentialKind": "auth-key", "authKey": "tskey-auth-wire",
	})
	if err != nil || enrollResult == nil || enrollResult.IsError {
		t.Fatalf("enroll wire call failed: %#v err=%v", enrollResult, err)
	}
	enrollBody := asMap(t, enrollResult.StructuredContent)
	if enrollBody["overlayUri"] == nil || enrollBody["meshIp"] == nil || enrollBody["membershipRef"] == nil {
		t.Fatalf("enroll missing fields: %#v", enrollBody)
	}
	if strings.Contains(mustJSON(t, enrollBody), "tskey-auth-wire") {
		t.Fatal("wire enroll leaked auth key")
	}
}
