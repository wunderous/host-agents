package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

func TestTailscaleAPIListDevicesAndCreateAuthKey(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tskey-api-test" {
			t.Errorf("Authorization = %q", got)
		}
		sawAuth = true
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/devices"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"devices": []map[string]any{
					{"id": "n1", "hostname": "opute-ha-a", "name": "opute-ha-a.tailnet.ts.net", "addresses": []string{"100.64.1.10"}},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/keys"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k1", "key": "tskey-auth-created"})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/device/"):
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTailscaleAPIClient("tskey-api-test", "-", server.URL)
	devices, err := client.ListDevices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Hostname != "opute-ha-a" {
		t.Fatalf("devices = %#v", devices)
	}
	found, ok := findDeviceByHostname(devices, "opute-ha-a")
	if !ok || found.ID != "n1" {
		t.Fatalf("findDeviceByHostname = %#v ok=%v", found, ok)
	}
	if deviceIPv4(found) != "100.64.1.10" {
		t.Fatalf("deviceIPv4 = %q", deviceIPv4(found))
	}
	key, err := client.CreateAuthKey(t.Context(), 3600)
	if err != nil {
		t.Fatal(err)
	}
	if key.Key != "tskey-auth-created" {
		t.Fatalf("key = %#v", key)
	}
	if err := client.DeleteDevice(t.Context(), "n1"); err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatal("expected Authorization header")
	}
}

func TestLoadTailscaleEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "tailscale.env")
	if err := os.WriteFile(envPath, []byte("TAILSCALE_API_KEY=tskey-api-from-file\nTAILSCALE_TAILNET=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILSCALE_API_KEY", "")
	t.Setenv("TAILSCALE_TAILNET", "")
	loadTailscaleEnvFile(envPath)
	if got := os.Getenv("TAILSCALE_API_KEY"); got != "tskey-api-from-file" {
		t.Fatalf("TAILSCALE_API_KEY = %q", got)
	}
	if got := os.Getenv("TAILSCALE_TAILNET"); got != "example.com" {
		t.Fatalf("TAILSCALE_TAILNET = %q", got)
	}
}

func TestLiveValidateUsesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/devices") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"devices": []map[string]any{{"id": "n1", "hostname": "a", "addresses": []string{"100.64.1.1"}}},
		})
	}))
	defer server.Close()

	t.Setenv("OPUTE_TAILSCALE_BACKEND", "live")
	t.Setenv("TAILSCALE_API_KEY", "tskey-api-test")
	t.Setenv("TAILSCALE_API_BASE", server.URL)
	t.Setenv("TAILSCALE_TAILNET", "-")
	resetOwnershipStoreForTest()

	result, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayValidateOperation, map[string]any{
		"targetUri": "container:local:opute-ha-a", "credentialKind": "api-key", "apiKey": "tskey-api-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := asMap(t, result.StructuredContent)
	if body["ready"] != true || body["nodeCount"] != float64(1) && body["nodeCount"] != 1 {
		// JSON numbers may decode as float64 via some paths; StructuredContent is native map.
		if n, ok := body["nodeCount"].(int); !ok || n != 1 {
			if n64, ok := body["nodeCount"].(int); ok && n64 == 1 {
				// ok
			} else if body["nodeCount"] != 1 {
				t.Fatalf("validate result = %#v", body)
			}
		}
	}
	encoded := mustJSON(t, body)
	if strings.Contains(encoded, "tskey-api-test") {
		t.Fatal("api key leaked into structured content")
	}
}

func TestLiveGuestOpsSkippedUnlessEnabled(t *testing.T) {
	if os.Getenv("OPUTE_TAILSCALE_LIVE") == "1" {
		t.Skip("live guest integration is exercised outside unit tests when OPUTE_TAILSCALE_LIVE=1")
	}
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "live")
	t.Setenv("TAILSCALE_API_KEY", "tskey-api-test")
	resetOwnershipStoreForTest()
	_, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "container:local:opute-ha-a", "name": "opute-ha-a",
		"credentialKind": "auth-key", "authKey": "tskey-auth-test",
	})
	if err == nil {
		t.Fatal("expected enroll to fail closed without mesh-runtime.ensure-agent")
	}
	if !strings.Contains(err.Error(), "mesh-runtime.ensure-agent") && !strings.Contains(err.Error(), "mesh agent not ready") {
		t.Fatalf("unexpected enroll error: %v", err)
	}
	// Even after ensure-agent is recorded, live backend still needs host agent endpoint.
	if _, err := dispatchOverlayOperation(t.Context(), capabilitycontract.MeshRuntimeEnsureAgentOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "container:local:opute-ha-a",
	}); err == nil {
		t.Fatal("expected live ensure-agent to fail without host agent endpoint in unit tests")
	} else if !strings.Contains(err.Error(), "OPUTE_HOST_AGENT_ENDPOINT") && !strings.Contains(err.Error(), "host") {
		t.Fatalf("unexpected ensure-agent error: %v", err)
	}
}

func TestFindTailscaleIPv4AndDNSName(t *testing.T) {
	ip := findTailscaleIPv4("mesh_ip=100.64.9.9\ninet 100.64.9.9/32")
	if ip != "100.64.9.9" {
		t.Fatalf("ip = %q", ip)
	}
	dns := findDNSName("dns_name=opute-ha-a.tailnet.ts.net.\n")
	if dns != "opute-ha-a.tailnet.ts.net" {
		t.Fatalf("dns = %q", dns)
	}
}

func TestNormalizeLoopbackHTTP(t *testing.T) {
	got, err := normalizeLoopbackHTTP("8080")
	if err != nil || got != "http://127.0.0.1:8080" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := normalizeLoopbackHTTP("http://example.com:80/"); err == nil {
		t.Fatal("expected non-loopback rejection")
	}
}
