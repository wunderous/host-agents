package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/cluster"
	"github.com/wunderous/host-agents/internal/domain/host"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

func init() {
	register(toolname.InstallClusterAgent, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Cluster().InstallClusterAgent(cluster.InstallClusterAgentArgs{
			VMName:      stringField(args, "vmName"),
			ClusterID:   stringField(args, "clusterId"),
			ClusterName: stringField(args, "clusterName"),
			AgentID:     stringField(args, "agentId"),
			BridgeToken: stringField(args, "bridgeToken"),
			BridgeURL:   stringField(args, "bridgeUrl"),
			BridgePort:  intField(args, "bridgePort"),
			APIEndpoint: stringField(args, "apiEndpoint"),
			ProviderID:  stringField(args, "providerId"),
			ResourceID:  stringField(args, "resourceId"),
			Source:      stringField(args, "source"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes cluster agent installed."), nil
	})
}

func init() {
	register(toolname.ListClusters, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Cluster().ListClusters(listClustersFastArg(args))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.GetClusterDetails, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		fast, _ := args["fast"].(bool)
		out, err := svc.Cluster().GetClusterDetails(vmName, fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.GetClusterRuntimeDetails, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		out, err := svc.Cluster().GetClusterRuntimeDetails(vmName)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ConfigureAgentConnection, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		env := map[string]string{}
		if raw, ok := args["environment"].(map[string]any); ok {
			for key, value := range raw {
				if text, ok := value.(string); ok {
					env[key] = text
				}
			}
		}
		remove := []string{}
		if raw, ok := args["remove"].([]any); ok {
			for _, value := range raw {
				if key, ok := value.(string); ok {
					remove = append(remove, key)
				}
			}
		}
		out, err := svc.Host().ConfigureAgentConnection(host.ConfigureAgentConnectionArgs{EnvFile: stringField(args, "envFile"), Environment: env, Remove: remove, ServiceName: stringField(args, "serviceName"), Restart: optionalBoolField(args, "restart"), Scope: stringField(args, "scope")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Agent connection configuration written."), nil
	})
}
