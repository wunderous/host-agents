package transport

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/authz"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/tools"
)

func TestMCPRejectsInitializeAndLegacyVersions(t *testing.T) {
	hs := newTransportTestServer(t)
	authorizer := testAuthorizer(t, "host-bootstrap")
	httpSrv := NewHTTPServer(HTTPOptions{HostServer: hs, BindHost: "127.0.0.1", Port: 0, Authz: authorizer})
	ts := httptest.NewServer(httpSrv.Handler())
	defer ts.Close()

	get, err := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	get.Header.Set("Authorization", "Bearer host-bootstrap")
	res, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status = %d, want 405", res.StatusCode)
	}

	del, err := http.NewRequest(http.MethodDelete, ts.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	del.Header.Set("Authorization", "Bearer host-bootstrap")
	res, err = http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /mcp status = %d, want 405", res.StatusCode)
	}

	status, body := postMCP(t, ts.URL+"/mcp", "host-bootstrap", "https://evil.example", "initialize", "", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("bad Origin status = %d body=%s", status, body)
	}

	status, body = postMCP(t, ts.URL+"/mcp", "host-bootstrap", "", "initialize", "", map[string]any{
		"protocolVersion": "2025-11-25",
		"_meta":           mustMeta(),
	})
	if status != http.StatusNotFound {
		t.Fatalf("initialize status = %d body=%s, want 404", status, body)
	}
	if !strings.Contains(body, "-32601") {
		t.Fatalf("initialize body = %s, want method not found", body)
	}

	status, body = postMCP(t, ts.URL+"/mcp", "invalid-token", "", "server/discover", "", map[string]any{"_meta": mustMeta()})
	if status != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d body=%s", status, body)
	}

	status, body = postMCP(t, ts.URL+"/mcp", "host-bootstrap", "", "server/discover", "", map[string]any{"_meta": mustMeta()})
	if status != http.StatusOK {
		t.Fatalf("discover status = %d body=%s", status, body)
	}
	if strings.Contains(body, "listChanged") || strings.Contains(body, `"resources"`) {
		t.Fatalf("discover advertised unimplemented resources: %s", body)
	}
	if !strings.Contains(body, "2026-07-28") {
		t.Fatalf("discover missing modern version: %s", body)
	}

	prm, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer prm.Body.Close()
	if prm.StatusCode != http.StatusOK {
		t.Fatalf("PRM status = %d", prm.StatusCode)
	}
	var prmBody map[string]any
	if err := json.NewDecoder(prm.Body).Decode(&prmBody); err != nil {
		t.Fatal(err)
	}
	servers, _ := prmBody["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != ts.URL {
		t.Fatalf("PRM authorization_servers = %#v", prmBody["authorization_servers"])
	}

	meta, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Body.Close()
	var asBody map[string]any
	if err := json.NewDecoder(meta.Body).Decode(&asBody); err != nil {
		t.Fatal(err)
	}
	methods, _ := asBody["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Fatalf("AS metadata PKCE = %#v", asBody["code_challenge_methods_supported"])
	}
	if asBody["client_id_metadata_document_supported"] != true {
		t.Fatalf("CIMD not advertised: %#v", asBody)
	}
}

func TestAudienceBoundTokenRejectedOnSecondResource(t *testing.T) {
	hs := newTransportTestServer(t)
	authorizer := testAuthorizer(t, "host-bootstrap")
	httpSrv := NewHTTPServer(HTTPOptions{HostServer: hs, BindHost: "127.0.0.1", Port: 0, Authz: authorizer})
	loopback := httptest.NewServer(httpSrv.Handler())
	defer loopback.Close()
	public := httptest.NewServer(httpSrv.Handler())
	defer public.Close()

	form := strings.NewReader("grant_type=client_credentials&client_id=opute-mcp-host&resource=" + loopback.URL + "/mcp")
	req, err := http.NewRequest(http.MethodPost, loopback.URL+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var tokenBody map[string]any
	if err := json.NewDecoder(res.Body).Decode(&tokenBody); err != nil {
		t.Fatal(err)
	}
	token, _ := tokenBody["access_token"].(string)
	if !strings.HasPrefix(token, "oha_") {
		t.Fatalf("issued token = %#v", tokenBody)
	}
	status, _ := postMCP(t, loopback.URL+"/mcp", token, "", "server/discover", "", map[string]any{"_meta": mustMeta()})
	if status != http.StatusOK {
		t.Fatalf("loopback audience status = %d", status)
	}
	status, _ = postMCP(t, public.URL+"/mcp", token, "", "server/discover", "", map[string]any{"_meta": mustMeta()})
	if status != http.StatusForbidden {
		t.Fatalf("mismatched audience status = %d, want 403", status)
	}
}

func newTransportTestServer(t *testing.T) *hostmcp.Server {
	t.Helper()
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	hs, err := hostmcp.NewServer(hostmcp.Options{ProviderID: "incus", Ops: svc, Standalone: true, AllowMutations: false, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hs.Close() })
	return hs
}

func testAuthorizer(t *testing.T, token string) *authz.Service {
	t.Helper()
	svc, err := authz.Open(authz.Options{StateDir: t.TempDir(), BootstrapToken: token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func mustMeta() map[string]any {
	meta, err := mcphttp.ModernRequestEnvelope("test")
	if err != nil {
		panic(err)
	}
	return meta
}

func TestMCPAllowsLegacyHandshakeWhenOptedIn(t *testing.T) {
	hs := newTransportTestServer(t)
	authorizer := testAuthorizer(t, "host-bootstrap")
	httpSrv := NewHTTPServer(HTTPOptions{
		HostServer:           hs,
		BindHost:             "127.0.0.1",
		Port:                 0,
		Authz:                authorizer,
		AllowLegacyHandshake: true,
	})
	ts := httptest.NewServer(httpSrv.Handler())
	defer ts.Close()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "cursor", "version": "1.0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer host-bootstrap")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200, body=%s", res.StatusCode, string(payload))
	}
	if !strings.Contains(string(payload), "serverInfo") {
		t.Fatalf("initialize response = %s, want serverInfo", string(payload))
	}

	listBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	listReq, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(listBody)))
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("Content-Type", "application/json")
	listReq.Header.Set("Accept", "application/json, text/event-stream")
	listReq.Header.Set("Authorization", "Bearer host-bootstrap")
	listRes, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	defer listRes.Body.Close()
	listPayload, _ := io.ReadAll(listRes.Body)
	if listRes.StatusCode != http.StatusOK {
		t.Fatalf("tools/list status = %d, want 200, body=%s", listRes.StatusCode, string(listPayload))
	}
	if !strings.Contains(string(listPayload), "tools") {
		t.Fatalf("tools/list response = %s, want tools", string(listPayload))
	}
}

func postMCP(t *testing.T, url, token, origin, method, name string, params map[string]any) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if err := mcphttp.ApplyStreamableHTTPRequestHeaders(req); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Mcp-Method", method)
	if name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(payload)
}

// TestLegacyHandshakeDoesNotBypassModernSurface pins the narrowing recorded in
// ADR 0011.
//
// `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE` used to skip modern-MCP validation for any
// request whose params omitted `_meta["io.modelcontextprotocol/protocolVersion"]`,
// on every method. A client could therefore reach `tasks/*` -- which has its own
// capability negotiation -- and `server/discover` with no `Mcp-Method` header and
// no protocol version, simply by not asking to be validated. The flag is named
// for the handshake; its effect was to disable the transport contract.
//
// The compatibility surface is now enumerated. These two methods are 2026-07-28
// additions that no pre-2026 client can need, so they stay validated even when
// the legacy flag is on. TestMCPAllowsLegacyHandshakeWhenOptedIn covers the
// other half: initialize and tools/list still work for Codex/Cursor.
func TestLegacyHandshakeDoesNotBypassModernSurface(t *testing.T) {
	hs := newTransportTestServer(t)
	authorizer := testAuthorizer(t, "host-bootstrap")
	httpSrv := NewHTTPServer(HTTPOptions{
		HostServer:           hs,
		BindHost:             "127.0.0.1",
		Port:                 0,
		Authz:                authorizer,
		AllowLegacyHandshake: true,
	})
	ts := httptest.NewServer(httpSrv.Handler())
	defer ts.Close()

	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{method: "server/discover", params: map[string]any{}},
		{method: "tasks/get", params: map[string]any{"taskId": "task-1"}},
		// A progressToken in _meta is the shape the old comment called out as
		// "arbitrary _meta"; it must not read as a modern negotiation either.
		{method: "tasks/list", params: map[string]any{"_meta": map[string]any{"progressToken": 1}}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  tc.method,
				"params":  tc.params,
			})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(body)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Authorization", "Bearer host-bootstrap")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			payload, _ := io.ReadAll(res.Body)
			// -32020 HeaderMismatch is what validateModernMCPRequest returns for a
			// request carrying neither the headers nor the _meta protocol version.
			if !strings.Contains(string(payload), "-32020") {
				t.Fatalf("%s in legacy mode = %s, want -32020 HeaderMismatch: the legacy bypass must not cover the 2026-07-28 surface", tc.method, string(payload))
			}
		})
	}
}
