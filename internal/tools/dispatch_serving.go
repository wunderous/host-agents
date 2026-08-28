package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/serving"
	"github.com/wunderous/host-agents/internal/hostagent"
)

func init() {
	register(toolname.ReconcileServingAssignment, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Serving().ReconcileServingAssignment(servingAssignmentArgs(args), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Serving assignment reconciled."), nil
	})
}

func init() {
	register(toolname.DiscoverServiceIngress, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		rawEndpoints, _ := args["endpoints"].([]any)
		endpoints := make([]serving.ServiceIngressEndpoint, 0, len(rawEndpoints))
		for _, raw := range rawEndpoints {
			if row, ok := raw.(map[string]any); ok {
				endpoints = append(endpoints, serving.ServiceIngressEndpoint{Name: stringField(row, "name"), Hostname: stringField(row, "hostname")})
			}
		}
		out, err := svc.Serving().DiscoverServiceIngress(serving.DiscoverServiceIngressArgs{VMName: vmNameFromBinding(binding), Endpoints: endpoints, IngressNamespace: stringField(args, "ingressNamespace"), IngressService: stringField(args, "ingressService")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Discovered service ingress endpoints."), nil
	})
}
