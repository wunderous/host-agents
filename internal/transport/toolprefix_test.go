package transport

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/tools"
)

func newPrefixedTransportServer(t *testing.T, agentID string) *hostmcp.Server {
	t.Helper()
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	hs, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID:      "incus",
		Ops:             svc,
		Standalone:      true,
		AllowMutations:  false,
		StateDir:        t.TempDir(),
		AgentID:         agentID,
		PrefixToolNames: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hs.Close() })
	return hs
}

func TestHealthExposesMcpToolNamePrefixWhenEnabled(t *testing.T) {
	const agentID = "host-zephyrus-ef47fbbf"
	hs := newPrefixedTransportServer(t, agentID)
	httpSrv := NewHTTPServer(HTTPOptions{HostServer: hs, BindHost: "127.0.0.1", Port: 0, AgentID: agentID})
	ts := httptest.NewServer(httpSrv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	want := hostmcp.ToolNamePrefix(agentID)
	if body["mcpToolNamePrefix"] != want {
		t.Fatalf("health prefix = %#v, want %q body=%s", body["mcpToolNamePrefix"], want, payload)
	}
}

func TestHealthOmitsMcpToolNamePrefixByDefault(t *testing.T) {
	const agentID = "host-zephyrus-ef47fbbf"
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	hs, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID:     "incus",
		Ops:            svc,
		Standalone:     true,
		AllowMutations: false,
		StateDir:       t.TempDir(),
		AgentID:        agentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hs.Close() })
	httpSrv := NewHTTPServer(HTTPOptions{HostServer: hs, BindHost: "127.0.0.1", Port: 0, AgentID: agentID})
	ts := httptest.NewServer(httpSrv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["mcpToolNamePrefix"]; ok {
		t.Fatalf("default health must not advertise mcpToolNamePrefix: %s", payload)
	}
}

func TestPrefixedHTTPToolCallAcceptsCatalogAlias(t *testing.T) {
	const agentID = "host-zephyrus-ef47fbbf"
	hs := newPrefixedTransportServer(t, agentID)
	authorizer := testAuthorizer(t, "host-bootstrap")
	httpSrv := NewHTTPServer(HTTPOptions{HostServer: hs, BindHost: "127.0.0.1", Port: 0, Authz: authorizer, AgentID: agentID})
	ts := httptest.NewServer(httpSrv.Handler())
	defer ts.Close()

	prefix := hostmcp.ToolNamePrefix(agentID)
	status, body := postMCP(t, ts.URL+"/mcp", "host-bootstrap", "http://127.0.0.1", "tools/list", "", map[string]any{"_meta": mustMeta()})
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d body=%s", status, body)
	}
	var listEnvelope struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &listEnvelope); err != nil {
		t.Fatalf("tools/list json: %v body=%s", err, body)
	}
	marker := prefix + "_"
	sawCatalog := false
	for _, tool := range listEnvelope.Result.Tools {
		if !strings.HasPrefix(tool.Name, marker) {
			t.Fatalf("unprefixed wire name %q", tool.Name)
		}
		if tool.Name == marker+"get_capability_catalog" {
			sawCatalog = true
		}
	}
	if !sawCatalog {
		t.Fatal("prefixed get_capability_catalog missing from tools/list")
	}

	status, body = postMCP(t, ts.URL+"/mcp", "host-bootstrap", "http://127.0.0.1", "tools/call", "get_capability_catalog", map[string]any{
		"name":      "get_capability_catalog",
		"arguments": map[string]any{},
		"_meta":     mustMeta(),
	})
	if status != http.StatusOK {
		t.Fatalf("unprefixed tools/call status = %d body=%s", status, body)
	}
	if strings.Contains(body, `"error"`) && strings.Contains(strings.ToLower(body), "not found") {
		t.Fatalf("unprefixed alias should rewrite: %s", body)
	}

	status, body = postMCP(t, ts.URL+"/mcp", "host-bootstrap", "http://127.0.0.1", "tools/call", prefix+"_get_capability_catalog", map[string]any{
		"name":      prefix + "_get_capability_catalog",
		"arguments": map[string]any{},
		"_meta":     mustMeta(),
	})
	if status != http.StatusOK {
		t.Fatalf("prefixed tools/call status = %d body=%s", status, body)
	}
}
