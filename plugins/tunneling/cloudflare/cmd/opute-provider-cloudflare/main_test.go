package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	var publicHostRecipe *providercontract.RecipeRef
	for index := range manifest.Recipes {
		if manifest.Recipes[index].ID == "com.opute.cloudflare.tunneling.public-host" {
			publicHostRecipe = &manifest.Recipes[index]
			break
		}
	}
	if publicHostRecipe == nil {
		t.Fatal("manifest missing public-host recipe")
	}
	if publicHostRecipe.Source.URI != "recipes/tunneling-public-host.yaml" || publicHostRecipe.Mode != "public-host" || !strings.HasPrefix(publicHostRecipe.Source.SHA256, "sha256:") {
		t.Fatalf("manifest public-host recipe reference is incomplete: %#v", *publicHostRecipe)
	}
	seen := map[string]bool{}
	for _, operation := range manifest.Services[0].Operations {
		seen[operation.ID] = true
	}
	for _, name := range []string{"opute.capability.tunneling.ensure-host-tunnel", "opute.capability.tunneling.probe-host-tunnel", "opute.capability.tunneling.remove-host-tunnel", "ensure_cloudflared_tunnel", "install_cloudflared_connector", "delete_cloudflared_connector"} {
		if !seen[name] {
			t.Fatalf("manifest missing provider operation %q", name)
		}
	}
	for _, name := range []string{"create_cloudflare_tunnel", "delete_cloudflare_tunnel"} {
		if seen[name] {
			t.Fatalf("manifest must not publish retired catalog route %q", name)
		}
	}
}

func TestManagedRecipeRequiresAuthenticatedPublicMCPProbe(t *testing.T) {
	recipePath := filepath.Join("..", "..", "recipes", "tunneling-managed.yaml")
	recipe, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatalf("read managed tunnel recipe: %v", err)
	}
	text := string(recipe)
	for _, required := range []string{
		"recipeVersion: 1.3.0",
		"bindingId:",
		"servingContract: mcp-exposure.v1",
		"ensure_public_mcp_tunnel",
		"opute.capability.tunneling.probe-host-tunnel",
		"acceptAuthenticationChallenge: true",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("managed recipe missing authenticated public MCP contract %q", required)
		}
	}
}

func TestPublicHostRecipeDeclaresProviderOwnedPublicMCPFlow(t *testing.T) {
	recipePath := filepath.Join("..", "..", "recipes", "tunneling-public-host.yaml")
	recipe, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatalf("read public host recipe: %v", err)
	}
	text := string(recipe)
	for _, required := range []string{
		"recipeId: com.opute.cloudflare.tunneling.public-host",
		"servingContract: mcp-exposure.v1",
		"opute.capability.tunneling.ensure-host-tunnel",
		"manageHostConnector: true",
		"opute.capability.tunneling.probe-host-tunnel",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("public host recipe missing provider-owned MCP flow %q", required)
		}
	}
}

func TestCloudflareManifestDeclaresNetworkOverlayService(t *testing.T) {
	manifest := cloudflareManifest()
	var overlay *providercontract.ServiceDefinition
	for index := range manifest.Services {
		if manifest.Services[index].CapabilityID == "opute.capability.network-overlay.v1" {
			overlay = &manifest.Services[index]
			break
		}
	}
	if overlay == nil {
		t.Fatal("manifest missing network-overlay service")
	}
	seen := map[string]bool{}
	for _, operation := range overlay.Operations {
		seen[operation.ID] = true
		if operation.ID == "opute.capability.network-overlay.remove-ha-endpoint" {
			if len(operation.Requires) != 0 {
				t.Fatalf("opaque endpoint removal must not require targetUri: %#v", operation.Requires)
			}
			continue
		}
		if len(operation.Requires) != 2 || operation.Requires[0].Argument != "targetUri" || operation.Requires[0].ResourceType != "vm" || !operation.Requires[0].Required || operation.Requires[1].Argument != "targetUri" || operation.Requires[1].ResourceType != "container" || !operation.Requires[1].Required {
			t.Fatalf("overlay operation %q is missing typed target binding: %#v", operation.ID, operation.Requires)
		}
	}
	for _, operation := range []string{
		"opute.capability.network-overlay.validate",
		"opute.capability.network-overlay.prepare-membership",
		"opute.capability.network-overlay.attach-target",
		"opute.capability.network-overlay.probe-reachability",
		"opute.capability.network-overlay.remove-membership",
	} {
		if !seen[operation] {
			t.Fatalf("manifest missing network-overlay operation %q", operation)
		}
	}
}

func TestCloudflareMutationsDeclareResourceCost(t *testing.T) {
	manifest := cloudflareManifest()
	check := func(operation providercontract.Operation) {
		t.Helper()
		if operation.Effect == "read" {
			return
		}
		if operation.ResourceCost == nil || strings.TrimSpace(operation.ResourceCost.Class) == "" {
			t.Fatalf("mutating operation %q must declare resourceCost.class", operation.ID)
		}
	}
	for _, service := range manifest.Services {
		for _, operation := range service.Operations {
			check(operation)
		}
	}
	if manifest.Teardown == nil {
		t.Fatal("manifest missing teardown")
	}
	check(*manifest.Teardown)
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

func TestCloudflareHostServiceScopeUsesTypedURIAndDefaults(t *testing.T) {
	if got := hostServiceURIForScope("system", "opute-cloudflared-host.service"); got != "host-service:local:system/opute-cloudflared-host.service" {
		t.Fatalf("unexpected system service URI: %q", got)
	}
	if got := defaultHostServiceFile("system", "opute-cloudflared-host.service"); got != "/etc/systemd/system/opute-cloudflared-host.service" {
		t.Fatalf("unexpected system service file: %q", got)
	}
	if got := defaultHostServiceFile("user", "opute-cloudflared-host.service"); got != "~/.config/systemd/user/opute-cloudflared-host.service" {
		t.Fatalf("unexpected user service file: %q", got)
	}
	if scope, err := hostServiceScope(map[string]any{"scope": "system"}); err != nil || scope != "system" {
		t.Fatalf("system scope rejected: %q %v", scope, err)
	}
	if _, err := hostServiceScope(map[string]any{"scope": "container"}); err == nil {
		t.Fatal("unsupported host service scope was accepted")
	}
}

func TestCloudflareSystemTeardownRemovesOnlyDeclaredUnitWithHostCommand(t *testing.T) {
	serviceFile := "/etc/systemd/system/opute-provider-cloudflare-p15-test.service"
	plan := teardownPlan(
		"com.opute.cloudflare.teardown",
		"opute-provider-cloudflare-p15-test.service",
		serviceFile,
		"host-service:local:system/opute-provider-cloudflare-p15-test.service",
		"system",
		"cleanup",
	)
	nodes, ok := plan["nodes"].([]any)
	if !ok || len(nodes) != 3 {
		t.Fatalf("unexpected teardown nodes: %#v", plan["nodes"])
	}
	remove := nodes[2].(map[string]any)
	action := remove["action"].(map[string]any)
	if action["tool"] != "run_host_command" {
		t.Fatalf("system teardown used %q, want run_host_command", action["tool"])
	}
	args := action["args"].(map[string]any)
	if args["command"] != "rm -f -- '/etc/systemd/system/opute-provider-cloudflare-p15-test.service' && systemctl daemon-reload" {
		t.Fatalf("system teardown command = %#v", args["command"])
	}
	validation := remove["validate"].(map[string]any)
	if validation["tool"] != "run_host_command" {
		t.Fatalf("system teardown validation used %q, want run_host_command", validation["tool"])
	}
}

func TestCloudflareSystemTeardownRejectsUnownedServicePath(t *testing.T) {
	if _, err := validateCloudflareServiceFile("system", "/etc/systemd/system/../passwd.service"); err == nil {
		t.Fatal("invalid system service path was accepted")
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

func TestCloudflareMeshTargetAdmissionAcceptsVMsAndContainers(t *testing.T) {
	for _, raw := range []string{"vm:tenant-a:edge-vm", "container:tenant-a:edge-container"} {
		parsed, err := parseTargetURI(map[string]any{"targetUri": raw})
		if err != nil || parsed.String() != raw {
			t.Fatalf("mesh target %q rejected: %#v %v", raw, parsed, err)
		}
	}
	if _, err := parseTargetURI(map[string]any{"targetUri": "cluster:tenant-a:edge"}); err == nil {
		t.Fatal("mesh target accepted a cluster URI")
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

func TestDedicatedPublicMcpEnsureManagesHostConnectorByDefault(t *testing.T) {
	fake := newFakeCloudflare()
	withFakeCloudflare(t, fake)
	t.Setenv("OPUTE_HOST_AGENT_ENDPOINT", "")
	_, err := ensureTunnel(t.Context(), map[string]any{
		"bindingId":   "public-host-agent",
		"hostname":    "host.example.com",
		"endpoint":    "https://host.example.com/mcp",
		"localTarget": "http://127.0.0.1:3004/mcp",
		"tunnelName":  "opute-public-host-agent",
	})
	if err == nil || !strings.Contains(err.Error(), "OPUTE_HOST_AGENT_ENDPOINT is required") {
		t.Fatalf("public MCP endpoint should invoke the typed Host Agent connector by default, got %v", err)
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
