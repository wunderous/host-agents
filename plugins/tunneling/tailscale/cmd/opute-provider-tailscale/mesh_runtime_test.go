package main

import (
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

func TestMeshRuntimeEnsureAgentIsSeparateSeam(t *testing.T) {
	resetOwnershipStoreForTest()
	target := "vm:local:runtime-a"
	status, err := dispatchOverlayOperation(t.Context(), capabilitycontract.MeshRuntimeStatusOperation, map[string]any{
		"hostAgentId": "test-host",
		"targetUri":   target,
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	body := asMap(t, status.StructuredContent)
	if body["agentReady"] == true {
		t.Fatalf("expected agent not ready before ensure-agent: %#v", body)
	}
	if _, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, map[string]any{
		"hostAgentId": "test-host",
		"targetUri":   target,
		"name":        "runtime-a",
		"authKey":     "tskey-auth-test",
		"credentialKind": "auth-key",
	}); err == nil {
		t.Fatal("enroll must fail closed when mesh-runtime.ensure-agent was not called")
	}
	ensure, err := dispatchOverlayOperation(t.Context(), capabilitycontract.MeshRuntimeEnsureAgentOperation, map[string]any{
		"hostAgentId": "test-host",
		"targetUri":   target,
	})
	if err != nil {
		t.Fatalf("ensure-agent: %v", err)
	}
	body = asMap(t, ensure.StructuredContent)
	if body["ready"] != true || body["agentReady"] != true {
		t.Fatalf("ensure-agent not ready: %#v", body)
	}
	if body["contractVersion"] != capabilitycontract.MeshRuntime {
		t.Fatalf("contractVersion: %#v", body["contractVersion"])
	}
	cp, err := dispatchOverlayOperation(t.Context(), capabilitycontract.MeshRuntimeEnsureControlPlaneOperation, map[string]any{
		"hostAgentId":      "test-host",
		"targetUri":        target,
		"operatorMode":     true,
		"ingressClassName": "tailscale",
	})
	if err != nil {
		t.Fatalf("ensure-control-plane: %v", err)
	}
	body = asMap(t, cp.StructuredContent)
	if body["controlPlaneReady"] != true {
		t.Fatalf("control plane not ready: %#v", body)
	}
}
