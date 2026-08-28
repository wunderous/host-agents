package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/ops"
)

func init() {
	register(toolname.ListClusters, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ListClusters(listClustersFastArg(args))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.GetClusterDetails, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		fast, _ := args["fast"].(bool)
		out, err := svc.GetClusterDetails(vmName, fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.GetClusterRuntimeDetails, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		out, err := svc.GetClusterRuntimeDetails(vmName)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ConfigureAgentConnection, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
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
		out, err := svc.ConfigureAgentConnection(ops.ConfigureAgentConnectionArgs{EnvFile: stringField(args, "envFile"), Environment: env, Remove: remove, ServiceName: stringField(args, "serviceName"), Restart: optionalBoolField(args, "restart"), Scope: stringField(args, "scope")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Agent connection configuration written."), nil
	})
}
