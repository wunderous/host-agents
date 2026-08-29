package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

func init() {
	register(toolname.GetHostCapacity, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		service := svc.ResourceService()
		if service == nil {
			return nil, fmt.Errorf("host_resource_unavailable: typed host resource service is not mounted")
		}
		return structuredResult(service.Snapshot(), "Host capacity and enforcement state observed."), nil
	})
}

func init() {
	// Policy reconciliation is the recovery/control-plane path that repairs
	// the workload boundary admission depends on. It must remain callable when
	// workload enforcement is unknown; the backend still validates the exact
	// approved policy revision and host-service URI before changing anything.
	register(toolname.ReconcileHostResourcePolicy, EffectMutation, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		service := svc.ResourceService()
		if service == nil {
			return nil, fmt.Errorf("host_resource_unavailable: typed host resource service is not mounted")
		}
		snapshot, err := service.Reconcile(ctx, stringField(args, "policyRevision"), stringField(args, "uri"))
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{
			"reconciled":     true,
			"policyRevision": snapshot.PolicyRevision,
			"enforcement":    snapshot.Enforcement,
			"capacity":       snapshot,
		}, "Host resource policy revision reconciled."), nil
	})
}
