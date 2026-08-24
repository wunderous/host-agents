package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/resourceid"
)

func TestCloudflareManifestDeclaresDynamicCompatibilityOperations(t *testing.T) {
	manifest := cloudflareManifest()
	if err := providercontract.ValidateInstallManifest(manifest, manifest.Provider); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, operation := range manifest.Services[0].Operations {
		seen[operation.ID] = true
	}
	for _, name := range []string{"opute.capability.tunneling.ensure-host-tunnel", "ensure_cloudflared_tunnel", "install_cloudflared_connector", "delete_cloudflared_connector"} {
		if !seen[name] {
			t.Fatalf("manifest missing provider operation %q", name)
		}
	}
}

func TestCloudflareValidationPreservesDeclaredBindingsAndRejectsPlacement(t *testing.T) {
	bindings := []any{map[string]any{"id": "binding-a", "targetUri": "container:tenant-a:edge"}}
	result, err := dispatchCloudflareOperation(t.Context(), "opute.capability.tunneling.validate", map[string]any{"bindings": bindings, "placement": "container"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "container:tenant-a:edge") {
		t.Fatalf("provider did not preserve raw binding arguments: %s", encoded)
	}
	if _, err := dispatchCloudflareOperation(t.Context(), "opute.capability.tunneling.validate", map[string]any{"bindings": bindings, "placement": "vm-fallback"}); err == nil {
		t.Fatal("expected unsupported placement rejection")
	}
}

func TestCloudflareTargetAdmissionUsesTypedResourceURIs(t *testing.T) {
	target, err := typedTargetURI("container:tenant-a:edge", resourceid.TypeContainer)
	if err != nil || target.ResourceID != "edge" {
		t.Fatalf("valid typed target rejected: %#v %v", target, err)
	}
	if _, err := typedTargetURI("container:tenant-a:edge", resourceid.TypeCluster); err == nil {
		t.Fatal("wrong resource kind was accepted")
	}
	if _, err := typedTargetURI("cluster:other:edge", resourceid.TypeCluster); err != nil {
		t.Fatalf("tenant-scoped opaque cluster URI should parse at provider boundary: %v", err)
	}
	if _, err := typedTargetURI("not-a-resource", resourceid.TypeCluster); err == nil {
		t.Fatal("malformed resource URI was accepted")
	}
}

func TestCloudflareConnectorManifestDoesNotReturnToken(t *testing.T) {
	manifest := cloudflaredManifest("edge-system", "cloudflared", "cloudflare/cloudflared:test", 1, "secret-token", nil)
	if !strings.Contains(manifest, "secret-token") {
		t.Fatal("connector manifest must carry token to the host callback")
	}
	result, err := structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "placement": "kubernetes"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if strings.Contains(string(encoded), "secret-token") {
		t.Fatal("provider result leaked connector token")
	}
}

func TestCloudflareTunnelRejectsUnsafeLocalTargetBeforeCallback(t *testing.T) {
	_, err := ensureTunnel(t.Context(), map[string]any{
		"bindingId":   "binding-a",
		"localTarget": "file:///tmp/secret",
		"connector":   "host",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP(S)") {
		t.Fatalf("expected unsafe local target rejection, got %v", err)
	}
}

func TestCloudflareLegacyHostAliasDerivesHostConnector(t *testing.T) {
	t.Setenv("OPUTE_HOST_AGENT_ENDPOINT", "")
	args := map[string]any{
		"bindingId":   "binding-a",
		"localTarget": "http://127.0.0.1:9090",
	}
	_, err := ensureTunnel(t.Context(), args)
	if err == nil || !strings.Contains(err.Error(), "OPUTE_HOST_AGENT_ENDPOINT is required") {
		t.Fatalf("legacy host alias should derive connector before callback, got %v", err)
	}
}

func TestCloudflareCleanupReportsAllInvalidIdentifiers(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account")
	t.Setenv("CLOUDFLARE_ZONE_ID", "zone")
	t.Setenv("CLOUDFLARE_API_TOKEN", "token")
	err := cleanupExternalResources(t.Context(), map[string]any{
		"tunnelId":     "not valid",
		"dnsRecordIds": []any{"also not valid", "still not valid"},
	})
	if err == nil || !strings.Contains(err.Error(), "tunnel id") || !strings.Contains(err.Error(), "DNS record id") {
		t.Fatalf("expected aggregate identifier cleanup error, got %v", err)
	}
}

func TestCloudflareDeleteRejectsHTTP200WithAPIFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1022,"message":"active connections"}]}`))
	}))
	defer server.Close()

	err := cloudflareDelete(t.Context(), "token", server.URL)
	if err == nil || !strings.Contains(err.Error(), "1022") {
		t.Fatalf("expected Cloudflare API failure from HTTP 200 response, got %v", err)
	}
}
