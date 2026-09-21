package main

import (
	"encoding/json"
	"strings"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

func TestEnrollIsIdempotent(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	mustEnsureAgent(t, "vm:local:server-a")
	args := map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "name": "server-a",
		"credentialKind": "auth-key", "authKey": "tskey-auth-test",
	}
	first, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, args)
	if err != nil {
		t.Fatal(err)
	}
	a := asMap(t, first.StructuredContent)
	b := asMap(t, second.StructuredContent)
	if a["membershipRef"] != b["membershipRef"] || a["overlayUri"] != b["overlayUri"] || a["meshIp"] != b["meshIp"] {
		t.Fatalf("enroll was not idempotent: %#v vs %#v", a, b)
	}
	if strings.Contains(mustJSON(t, a), "tskey-auth-test") {
		t.Fatal("secret leaked into enroll result")
	}
}

func TestPrivateProbeRejectsPublicOnlyPath(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	enrollA := mustEnroll(t, "vm:local:server-a", "server-a")
	_ = mustEnroll(t, "vm:local:server-b", "server-b")
	_, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "localTarget": "http://127.0.0.1:8080/",
		"membershipRef": enrollA["membershipRef"],
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayProbeOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "peerMeshIp": "100.64.1.1", "pathClass": "private-mesh",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := asMap(t, result.StructuredContent)
	if body["ready"] == true {
		t.Fatalf("public ingress must not satisfy private-mesh probe: %#v", body)
	}
}

func TestCredentialKindFailClosed(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	mustEnsureAgent(t, "vm:local:server-a")
	_, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "name": "server-a",
		"credentialKind": "api-key", "apiKey": "tskey-api-test",
	})
	if err == nil || !strings.Contains(err.Error(), "credential kind mismatch") {
		t.Fatalf("expected credential kind mismatch, got %v", err)
	}
	_, err = dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayValidateOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "credentialKind": "api-key", "authKey": "tskey-auth-test",
	})
	if err == nil || !strings.Contains(err.Error(), "credential kind mismatch") {
		t.Fatalf("expected validate credential kind mismatch, got %v", err)
	}
}

func TestForeignOwnershipTeardownRejected(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	seedForeignMembershipForTest("vm:local:foreign", "mesh-foreign")
	_, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayRemoveMembershipOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:foreign", "membershipRef": "mesh-foreign",
	})
	if err == nil || !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("expected foreign ownership rejection, got %v", err)
	}
	if err := finalizeOwnedTeardown(map[string]any{"generation": "foreign@0.0.0"}); err == nil {
		t.Fatal("expected foreign generation teardown rejection")
	}
}

func TestPublicIngressRejectsControlPlaneTargets(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	enroll := mustEnroll(t, "vm:local:server-a", "server-a")
	for _, kind := range []string{"etcd", "k8s-api", "cni", "host-admin"} {
		_, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, map[string]any{
			"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "localTarget": "http://127.0.0.1:2379/",
			"membershipRef": enroll["membershipRef"], "targetKind": kind,
		})
		if err == nil || !strings.Contains(err.Error(), "rejects control-plane") {
			t.Fatalf("expected control-plane rejection for %s, got %v", kind, err)
		}
	}
}

func TestPublicIngressStableFalseForNodeSpecific(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	enroll := mustEnroll(t, "vm:local:server-a", "server-a")
	result, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "localTarget": "http://127.0.0.1:8080/app",
		"membershipRef": enroll["membershipRef"], "hostname": "a.example.ts.net",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := asMap(t, result.StructuredContent)
	if body["stable"] != false {
		t.Fatalf("node-specific public ingress must be stable=false: %#v", body)
	}
	if body["pathClass"] != "public-ingress" {
		t.Fatalf("pathClass = %#v", body["pathClass"])
	}
}

func TestPrivateMeshBothDirections(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	a := mustEnroll(t, "vm:local:server-a", "server-a")
	b := mustEnroll(t, "vm:local:server-b", "server-b")
	_, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "peerTargetUri": "vm:local:server-b", "peerMeshIp": b["meshIp"],
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayProbeOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "peerMeshIp": b["meshIp"], "pathClass": "private-mesh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if asMap(t, probe.StructuredContent)["ready"] != true {
		t.Fatalf("private mesh probe not ready: %#v", probe.StructuredContent)
	}
	_ = a
}

func mustEnsureAgent(t *testing.T, target string) {
	t.Helper()
	if _, err := dispatchOverlayOperation(t.Context(), capabilitycontract.MeshRuntimeEnsureAgentOperation, map[string]any{
		"hostAgentId": "test-host",
		"targetUri":   target,
	}); err != nil {
		t.Fatalf("ensure-agent: %v", err)
	}
}

func mustEnroll(t *testing.T, target, name string) map[string]any {
	t.Helper()
	mustEnsureAgent(t, target)
	result, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": target, "name": name, "credentialKind": "auth-key", "authKey": "tskey-auth-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return asMap(t, result.StructuredContent)
}

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	body, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("structured content is not a map: %#v", value)
	}
	return body
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestReportTwoNodeReadinessAxes(t *testing.T) {
	resetOwnershipStoreForTest()
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	mustEnsureAgent(t, "vm:local:a")
	mustEnsureAgent(t, "vm:local:b")
	if _, err := enrollOverlay(map[string]any{
		"targetUri": "vm:local:a", "hostAgentId": "host-a", "name": "a",
		"credentialKind": "auth-key", "authKey": "tskey-auth-test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := enrollOverlay(map[string]any{
		"targetUri": "vm:local:b", "hostAgentId": "host-b", "name": "b",
		"credentialKind": "auth-key", "authKey": "tskey-auth-test",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := reportTwoNodeReadiness(map[string]any{
		"targetUri": "vm:local:a", "peerTargetUri": "vm:local:b", "hostAgentId": "host-a",
		"datastoreMode": "embedded-etcd-two-node",
	})
	if err != nil {
		t.Fatal(err)
	}
	content, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content type %T", res.StructuredContent)
	}
	if content["availabilityClass"] != "serving-continuity-only" {
		t.Fatalf("availabilityClass=%v", content["availabilityClass"])
	}
	if content["ready"] == true {
		t.Fatalf("expected not ready without bidirectional mesh")
	}
	if _, err := reportTwoNodeReadiness(map[string]any{
		"targetUri": "vm:local:a", "peerTargetUri": "vm:local:b", "hostAgentId": "host-a",
		"datastoreMode": "external-datastore",
	}); err == nil {
		t.Fatal("expected external-datastore without evidence to fail closed")
	}
}

func TestHostAgentIDRequired(t *testing.T) {
	resetOwnershipStoreForTest()
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	if _, err := enrollOverlay(map[string]any{
		"targetUri": "vm:local:a", "name": "a",
		"credentialKind": "auth-key", "authKey": "tskey-auth-test",
	}); err == nil {
		t.Fatal("expected missing hostAgentId to fail")
	}
}

func TestEnsurePublicIngressOperatorModeFailClosed(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	resetOwnershipStoreForTest()
	enroll := mustEnroll(t, "vm:local:server-a", "server-a")
	_, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, map[string]any{
		"hostAgentId": "host-a", "targetUri": "vm:local:server-a", "localTarget": "http://127.0.0.1:8080/",
		"membershipRef": enroll["membershipRef"], "operatorMode": true,
	})
	if err == nil {
		t.Fatal("expected operatorMode without evidence to fail")
	}
}
