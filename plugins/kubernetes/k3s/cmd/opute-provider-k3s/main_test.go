package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

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
		if len(operation.Requires) != 1 || operation.Requires[0].Argument != "targetUri" || operation.Requires[0].ResourceType != "cluster" || !operation.Requires[0].Required {
			t.Fatalf("operation %q is missing required canonical cluster binding: %#v", operation.ID, operation.Requires)
		}
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

func TestK3sResourceValidationRequiresTypedFields(t *testing.T) {
	if _, _, _, err := resourceArguments(map[string]any{"kind": "Deployment"}); err == nil {
		t.Fatal("resource without name was accepted")
	}
	if _, _, _, err := resourceArguments(map[string]any{"resourceName": "cloudflared"}); err == nil {
		t.Fatal("resource without kind was accepted")
	}
}
