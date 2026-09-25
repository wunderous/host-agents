package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

type cfMeshRuntimeRecord struct {
	TargetURI         string
	AgentReady        bool
	ControlPlaneReady bool
	AgentVersion      string
	ControlPlaneRef   string
	IngressClassName  string
}

var cfRuntimes = struct {
	sync.Mutex
	byTarget map[string]cfMeshRuntimeRecord
}{byTarget: make(map[string]cfMeshRuntimeRecord)}

func meshRuntimeOperations() []providercontract.Operation {
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input, map[string]any{"type": "object"}, []string{"host", "network"}, meshTargetBinding())
	}
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input, map[string]any{"type": "object"}, []string{"host", "network"}, meshTargetBinding())
	}
	props := map[string]any{
		"targetUri":        targetURISchema(),
		"hostAgentId":      map[string]any{"type": "string", "minLength": 1},
		"operatorMode":     map[string]any{"type": "boolean"},
		"ingressClassName": map[string]any{"type": "string"},
		"operatorEvidence": map[string]any{"type": "string"},
		"controlPlaneRef":  map[string]any{"type": "string"},
	}
	return []providercontract.Operation{
		read(capabilitycontract.MeshRuntimeValidateOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"}, "properties": props,
		}),
		mutation(capabilitycontract.MeshRuntimeEnsureAgentOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"}, "properties": props,
		}),
		mutation(capabilitycontract.MeshRuntimeEnsureControlPlaneOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"}, "properties": props,
		}),
		read(capabilitycontract.MeshRuntimeStatusOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"}, "properties": props,
		}),
	}
}

func dispatchMeshRuntimeOperation(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	_ = ctx
	target := strings.TrimSpace(stringInput(args, "targetUri", ""))
	if target == "" {
		return nil, fmt.Errorf("targetUri is required")
	}
	switch operation {
	case capabilitycontract.MeshRuntimeEnsureAgentOperation:
		cfRuntimes.Lock()
		rec := cfRuntimes.byTarget[target]
		rec.TargetURI = target
		rec.AgentReady = true
		rec.AgentVersion = "cloudflared"
		cfRuntimes.byTarget[target] = rec
		cfRuntimes.Unlock()
		return structured(cfMeshRuntimeOut(rec, true))
	case capabilitycontract.MeshRuntimeEnsureControlPlaneOperation:
		cfRuntimes.Lock()
		rec := cfRuntimes.byTarget[target]
		cfRuntimes.Unlock()
		if !rec.AgentReady {
			return nil, fmt.Errorf("mesh-runtime.ensure-control-plane requires mesh-runtime.ensure-agent first")
		}
		// Host-tunnel Cloudflare path has no separate operator control plane;
		// mark ready when agent is present. Kubernetes connector remains tunneling.v1.
		cfRuntimes.Lock()
		rec = cfRuntimes.byTarget[target]
		rec.ControlPlaneReady = true
		rec.ControlPlaneRef = firstNonEmpty(stringInput(args, "controlPlaneRef", ""), "cloudflare-edge")
		rec.IngressClassName = stringInput(args, "ingressClassName", "")
		cfRuntimes.byTarget[target] = rec
		cfRuntimes.Unlock()
		return structured(cfMeshRuntimeOut(rec, true))
	case capabilitycontract.MeshRuntimeValidateOperation, capabilitycontract.MeshRuntimeStatusOperation:
		cfRuntimes.Lock()
		rec := cfRuntimes.byTarget[target]
		cfRuntimes.Unlock()
		return structured(cfMeshRuntimeOut(rec, rec.AgentReady))
	default:
		return nil, fmt.Errorf("unknown mesh-runtime operation %q", operation)
	}
}

func cfMeshRuntimeOut(rec cfMeshRuntimeRecord, ready bool) map[string]any {
	return map[string]any{
		"contractVersion":   capabilitycontract.MeshRuntime,
		"provider":          "com.opute.cloudflare",
		"targetUri":         rec.TargetURI,
		"ready":             ready,
		"agentReady":        rec.AgentReady,
		"controlPlaneReady": rec.ControlPlaneReady,
		"agentVersion":      rec.AgentVersion,
		"controlPlaneRef":   rec.ControlPlaneRef,
		"ingressClassName":  rec.IngressClassName,
		"generation":        "com.opute.cloudflare@1.0.1",
		"probe": map[string]any{
			"agentReady":        rec.AgentReady,
			"controlPlaneReady": rec.ControlPlaneReady,
		},
	}
}
