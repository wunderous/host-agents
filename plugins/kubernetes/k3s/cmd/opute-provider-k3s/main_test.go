package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

func TestK3sManifestDeclaresNeutralCapabilityAndOperations(t *testing.T) {
	manifest := k3sManifest()
	if err := providercontract.ValidateInstallManifest(manifest, providercontract.ProviderRef{ID: "com.opute.k3s", Version: "1.0.1"}); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Provides) != 1 || manifest.Provides[0].ID != capabilitycontract.Kubernetes {
		t.Fatalf("unexpected capabilities: %#v", manifest.Provides)
	}
	seen := map[string]bool{}
	for _, operation := range operations() {
		seen[operation.ID] = true
	}
	for _, operation := range []string{
		capabilitycontract.KubernetesProvisionOperation,
		capabilitycontract.KubernetesStatusOperation,
		capabilitycontract.KubernetesConfigureRegistryOperation,
		capabilitycontract.KubernetesRestartOperation,
		capabilitycontract.KubernetesRemoveOperation,
		capabilitycontract.KubernetesApplyManifestOperation,
		capabilitycontract.KubernetesPutSecretOperation,
		capabilitycontract.KubernetesGetResourceOperation,
		capabilitycontract.KubernetesDeleteResourceOperation,
		capabilitycontract.KubernetesGetResourceStatusOperation,
		capabilitycontract.KubernetesListEventsOperation,
		capabilitycontract.KubernetesListClustersOperation,
		capabilitycontract.KubernetesGetClusterInfoOperation,
		capabilitycontract.KubernetesInspectMembershipOperation,
		capabilitycontract.KubernetesPrepareHAOperation,
		capabilitycontract.KubernetesPrepareJoinOperation,
		capabilitycontract.KubernetesGetJoinReceiverKeyOperation,
		capabilitycontract.KubernetesRedeemJoinOperation,
		capabilitycontract.KubernetesJoinNodeOperation,
		capabilitycontract.KubernetesEnsureHAEndpointOperation,
		capabilitycontract.KubernetesRemoveNodeOperation,
		capabilitycontract.KubernetesExecCommandOperation,
	} {
		if !seen[operation] {
			t.Fatalf("missing provider operation %q", operation)
		}
	}
}

func TestK3sHTTPHandlerAdvertisesModernProviderProtocol(t *testing.T) {
	server := newTestServer()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "http://provider.test/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")

	newHTTPHandler(server).ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("server/discover status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Result struct {
			SupportedVersions []string `json:"supportedVersions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode server/discover response: %v", err)
	}
	if len(response.Result.SupportedVersions) != 1 || response.Result.SupportedVersions[0] != "2026-07-28" {
		t.Fatalf("supported versions = %#v", response.Result.SupportedVersions)
	}
}

func TestProbeHAEndpointUsesHostTLSAndAcceptsKubernetesAuthChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/version" {
			t.Fatalf("request path = %q, want /version", request.URL.Path)
		}
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	statusCode, err := probeHAEndpointWithClient(context.Background(), server.URL, server.Client())
	if err != nil {
		t.Fatalf("probeHAEndpointWithClient() error = %v", err)
	}
	if statusCode != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", statusCode, http.StatusUnauthorized)
	}
}

func TestProbeHAEndpointRejectsMissingPublicEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	statusCode, err := probeHAEndpointWithClient(context.Background(), server.URL, server.Client())
	if err == nil {
		t.Fatal("probeHAEndpointWithClient() accepted a missing endpoint")
	}
	if statusCode != http.StatusNotFound {
		t.Fatalf("status code = %d, want %d", statusCode, http.StatusNotFound)
	}
}

func newTestServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-k3s", Version: "1.0.0"}, nil)
	addManifestTool(server, k3sManifest())
	addOperations(server)
	return server
}

func TestK3sTargetOperationsDeclareCanonicalClusterBinding(t *testing.T) {
	for _, operation := range operations() {
		if operation.ID == capabilitycontract.KubernetesValidateOperation || operation.ID == capabilitycontract.KubernetesListClustersOperation {
			if len(operation.Requires) != 0 {
				t.Fatalf("unbound inventory operation %q unexpectedly requires a target: %#v", operation.ID, operation.Requires)
			}
			continue
		}
		wantType := "cluster"
		if operation.ID == capabilitycontract.KubernetesProvisionOperation {
			wantType = "vm"
		}
		if len(operation.Requires) != 1 || operation.Requires[0].Argument != "targetUri" || operation.Requires[0].ResourceType != wantType || !operation.Requires[0].Required {
			t.Fatalf("operation %q is missing required canonical %s binding: %#v", operation.ID, wantType, operation.Requires)
		}
	}
}

func TestK3sProvisionClusterInitIsExplicit(t *testing.T) {
	for _, operation := range operations() {
		if operation.ID != capabilitycontract.KubernetesProvisionOperation {
			continue
		}
		properties, ok := operation.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("provision schema properties = %#v", operation.InputSchema)
		}
		clusterInit, ok := properties["clusterInit"].(map[string]any)
		if !ok || clusterInit["const"] != true {
			t.Fatalf("provision clusterInit must be an explicit true-only input: %#v", properties["clusterInit"])
		}
		return
	}
	t.Fatal("provision operation not found")
}

func TestK3sInstallExecBindsNativeTrafficToDeclaredOverlay(t *testing.T) {
	install, err := k3sServerInstallExec(map[string]any{
		"nodeIp":           "100.96.1.10",
		"advertiseAddress": "100.96.1.10",
		"flannelIface":     "CloudflareWARP",
		"tlsSan":           "100.96.1.10",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"server", "--cluster-init", "--node-ip 100.96.1.10", "--advertise-address 100.96.1.10", "--flannel-iface CloudflareWARP", "--tls-san 100.96.1.10"} {
		if !strings.Contains(install, want) {
			t.Fatalf("install exec %q does not contain %q", install, want)
		}
	}
	if _, err := k3sServerInstallExec(map[string]any{"nodeIp": "not-an-ip"}, false); err == nil {
		t.Fatal("invalid overlay address was accepted")
	}
}

func TestParseK3sVersionReturnsInstallerReleaseToken(t *testing.T) {
	got := parseK3sVersion("k3s version v1.31.8+k3s1 (33429f76) go version go1.25.5")
	if got != "v1.31.8+k3s1" {
		t.Fatalf("parseK3sVersion() = %q, want installer release token", got)
	}
}

func TestJoinInvitationIsOneTimeAndRequiresRedemption(t *testing.T) {
	ref := "join-test-one-time"
	joinInvitations.Lock()
	joinInvitations.items[ref] = joinInvitation{
		joinToken: "transient-token",
		expiresAt: time.Now().Add(time.Minute),
	}
	joinInvitations.Unlock()
	t.Cleanup(func() {
		joinInvitations.Lock()
		delete(joinInvitations.items, ref)
		joinInvitations.Unlock()
	})
	if _, _, _, err := joinTokenForNode(map[string]any{"redemptionRef": ref}); err == nil {
		t.Fatal("unredeemed invitation was accepted")
	}
	joinInvitations.Lock()
	invitation := joinInvitations.items[ref]
	invitation.redeemed = true
	joinInvitations.items[ref] = invitation
	joinInvitations.Unlock()
	token, consumedRef, version, err := joinTokenForNode(map[string]any{"redemptionRef": ref})
	if err != nil || token != "transient-token" || consumedRef != ref || version != "" {
		t.Fatalf("redeemed invitation = %q, %q, %q, %v", token, consumedRef, version, err)
	}
	if _, _, _, err := joinTokenForNode(map[string]any{"redemptionRef": ref}); err == nil {
		t.Fatal("consumed invitation was accepted twice")
	}
}

func TestSealedJoinMaterialRoundTripsOnlyToReceiverKey(t *testing.T) {
	key, err := currentJoinReceiverKey()
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	publicKey := base64.RawURLEncoding.EncodeToString(der)
	sealed, err := sealJoinMaterial(publicKey, sealedJoinMaterial{
		RedemptionRef:     "join-sealed-test",
		SourceTargetURI:   "cluster:local:source",
		DestinationHostID: "host-destination",
		ClusterIdentity:   "sha256:cluster",
		K3sVersion:        "v1.31.8+k3s1",
		JoinToken:         "transient-token",
		ExpiresAt:         time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "" || strings.Contains(sealed, "transient-token") {
		t.Fatalf("sealed material leaked plaintext: %q", sealed)
	}
	decoded, err := unsealJoinMaterial(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RedemptionRef != "join-sealed-test" || decoded.JoinToken != "transient-token" {
		t.Fatalf("unexpected sealed material: %#v", decoded)
	}

}

func TestK3sExecCommandValidationPreservesTypedArguments(t *testing.T) {
	args, err := stringSliceArgument(map[string]any{"kubectlArgs": []any{"get", "nodes", "-o", "json"}}, "kubectlArgs")
	if err != nil || len(args) != 4 || args[0] != "get" || args[3] != "json" {
		t.Fatalf("unexpected typed command arguments: %#v %v", args, err)
	}
	if _, err := stringSliceArgument(map[string]any{"kubectlArgs": []any{"get", "bad\narg"}}, "kubectlArgs"); err == nil {
		t.Fatal("unsafe command argument was accepted")
	}
}

func TestK3sTargetValidationRejectsFallbackShapes(t *testing.T) {
	cases := []map[string]any{
		{"targetUri": "vm:local:k3s", "providerInstanceName": "k3s"},
		{"targetUri": "cluster:local:k3s"},
		{"targetUri": "cluster:local:k3s", "providerInstanceName": "k3s", "instanceType": "display-name"},
	}
	for _, args := range cases {
		if err := requireTarget(args); err == nil {
			t.Fatalf("target unexpectedly accepted: %#v", args)
		}
	}
}

func TestK3sProvisionTargetValidationRequiresCanonicalVM(t *testing.T) {
	if err := requireProvisionTarget(map[string]any{"targetUri": "vm:local:k3s", "providerInstanceName": "k3s"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"targetUri": "cluster:local:k3s", "providerInstanceName": "k3s"},
		{"targetUri": "vm:local:k3s"},
		{"targetUri": "vm:local:k3s", "providerInstanceName": "k3s", "instanceType": "display-name"},
	} {
		if err := requireProvisionTarget(args); err == nil {
			t.Fatalf("provision target unexpectedly accepted: %#v", args)
		}
	}
}

func TestK3sResourceValidationRequiresTypedFields(t *testing.T) {
	if _, _, _, err := resourceArguments(map[string]any{"kind": "Deployment"}); err == nil {
		t.Fatal("resource without name was accepted")
	}
	if _, _, _, err := resourceArguments(map[string]any{"resourceName": "cloudflared"}); err == nil {
		t.Fatal("resource without kind was accepted")
	}
}

func TestNativeMembershipParsingUsesReadyAndRoleLabels(t *testing.T) {
	nodes, err := parseNativeMembership([]byte(`{
      "items": [
        {"metadata":{"name":"server-a","labels":{"node-role.kubernetes.io/control-plane":"true","node-role.kubernetes.io/etcd":"true"}},"status":{"conditions":[{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.31.8+k3s1"}}},
        {"metadata":{"name":"server-b","labels":{"node-role.kubernetes.io/control-plane":"true"}},"status":{"conditions":[{"type":"Ready","status":"False"}],"nodeInfo":{"kubeletVersion":"v1.31.8+k3s1"}}}
      ]
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0]["name"] != "server-a" || nodes[0]["ready"] != true {
		t.Fatalf("unexpected native nodes: %#v", nodes)
	}
	roles, ok := nodes[0]["roles"].([]string)
	if !ok || len(roles) != 2 || roles[0] == roles[1] {
		t.Fatalf("expected distinct role labels, got %#v", nodes[0]["roles"])
	}
}

func TestHAEndpointValidationRejectsPathAndQuery(t *testing.T) {
	if err := validateEndpoint("https://k3s.example.test:6443/path"); err == nil {
		t.Fatal("endpoint path was accepted")
	}
	if err := validateEndpoint("https://k3s.example.test:6443?token=secret"); err == nil {
		t.Fatal("endpoint query was accepted")
	}
	if err := validateEndpoint("https://k3s.example.test:6443"); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointHTTPStatusAcceptableIncludesKubernetesAuthChallenge(t *testing.T) {
	for _, status := range []int{200, 301, 401, 403} {
		if !endpointHTTPStatusAcceptable(status) {
			t.Fatalf("endpointHTTPStatusAcceptable(%d) = false, want true", status)
		}
	}
	for _, status := range []int{404, 500, 502, 503} {
		if endpointHTTPStatusAcceptable(status) {
			t.Fatalf("endpointHTTPStatusAcceptable(%d) = true, want false", status)
		}
	}
}
