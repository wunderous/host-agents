package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/host"
	"github.com/wunderous/host-agents/internal/domain/incus"
	"github.com/wunderous/host-agents/internal/domain/postgres"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

// These tools call methods still defined on ops/service.go and ops/standalone.go --
// the residue that M3's domain partition placed everything else out of. They are
// parked here rather than guessed into a domain; each one moves to its domain's
// dispatch.go as part of that partition.

func init() {
	register(toolname.GetHostInfo, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out := svc.Host().DescribeHost()
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.CheckLocalPrerequisites, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().CheckLocalPrerequisites()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.GetLocalStatus, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().GetLocalStatus()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ConfigureLocalLLMRuntime, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		if localLLMRuntime(args) == "ollama" {
			return nil, fmt.Errorf("the Ollama runtime configuration is host-wide and pinned to one process, two resident models, and one request at a time")
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return nil, fmt.Errorf("configure_local_llm_runtime is retired; configure llama-server through its pinned service manifest")
	})
}

func init() {
	register(toolname.RemoveLocalLLMModel, EffectDestructive, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		if localLLMRuntime(args) == "ollama" {
			return structuredResult(map[string]any{"removed": false, "purged": false, "shared": true, "reason": "Ollama model artifacts are shared host state; use the host Ollama lifecycle for explicit garbage collection"}, "shared Ollama model retained"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return structuredResult(map[string]any{"removed": false, "purged": false, "reason": "llama-server artifacts are retained by the artifact manifest"}, "llama-server model removal is not an inference operation"), nil
	})
}

func init() {
	h := func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		command, _ := args["command"].(string)
		command = strings.TrimSpace(command)
		if command == "" {
			return nil, fmt.Errorf("command is required")
		}
		timeoutMs := intField(args, "timeoutMs")
		if timeoutMs < 0 {
			return nil, fmt.Errorf("timeoutMs must be non-negative")
		}
		if timeoutMs > 2*60*60*1000 {
			return nil, fmt.Errorf("timeoutMs exceeds the two-hour maximum")
		}
		res, err := svc.Host().RunAgentShellWithTimeout(command, time.Duration(timeoutMs)*time.Millisecond, onData)
		if err != nil {
			return nil, err
		}
		payload := map[string]any{
			"exitCode": res.ExitCode,
			"stdout":   res.Stdout,
			"stderr":   res.Stderr,
		}
		text := res.Stdout
		if text == "" {
			text = res.Stderr
		}
		if text == "" {
			text = fmt.Sprintf("exit %d", res.ExitCode)
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: text}},
			StructuredContent: payload,
			IsError:           res.ExitCode != 0,
		}, nil
	}
	// One handler, two names. They differ in lifetime: a host command may
	// outlive its request and is handed to the task contract; agent_shell is
	// answered inline.
	register(toolname.AgentShell, EffectMutation, resource.ClassNormal, TaskInline, h)
	register(toolname.RunHostCommand, EffectMutation, resource.ClassNormal, TaskAware, h)
}

func init() {
	register(toolname.EnsureSqlConnector, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := postgres.EnsureSQLConnectorArgs{
			DatabaseID: stringField(args, "databaseId"),
			TargetHost: stringField(args, "targetHost"),
			TargetPort: intField(args, "targetPort"),
			ListenPort: intField(args, "listenPort"),
			ListenHost: stringField(args, "listenHost"),
		}
		out, err := svc.Postgres().EnsureSQLConnector(parsed)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("SQL connector %s listening on %s:%d", out.DatabaseID, out.ListenHost, out.ListenPort)
		return structuredResult(out, text), nil
	})
}

func init() {
	register(toolname.GetSqlConnectorStatus, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		databaseID := stringField(args, "databaseId")
		out, err := svc.Postgres().GetSQLConnectorStatus(databaseID)
		if err != nil {
			return nil, err
		}
		active, _ := out["active"].(bool)
		text := "SQL connector inactive"
		if active {
			text = fmt.Sprintf("SQL connector active on %v:%v", out["listenHost"], out["listenPort"])
		}
		return structuredResult(out, text), nil
	})
}

func init() {
	register(toolname.ReleaseSqlConnector, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		databaseID := stringField(args, "databaseId")
		force, _ := args["force"].(bool)
		released, err := svc.Postgres().ReleaseSQLConnector(databaseID, force)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Released SQL connector %s", databaseID)
		if !released {
			text = fmt.Sprintf("No SQL connector for %s", databaseID)
		}
		return structuredResult(map[string]any{"released": released, "databaseId": databaseID}, text), nil
	})
}

func init() {
	register(toolname.CreateVM, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := provisionArgs(args)
		if strings.TrimSpace(parsed.InstanceType) == "" {
			parsed.InstanceType = "virtual-machine"
		}
		out, err := svc.Incus().ProvisionVM(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Created VM '%s' from image '%s'.", out.VMName, out.Image)), nil
	})
}

func init() {
	register(toolname.ProvisionVM, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := provisionArgs(args)
		out, err := svc.Incus().ProvisionVM(parsed, onData)
		if err != nil {
			return nil, err
		}
		kind := strings.TrimSpace(out.InstanceType)
		if kind == "" {
			kind = "container"
		}
		return structuredResult(out, fmt.Sprintf("Provisioned %s '%s' from image '%s'.", kind, out.VMName, out.Image)), nil
	})
}

func init() {
	register(toolname.StartVM, EffectMutation, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().StartVM(incus.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Started VM '%s'.", out["vmName"])), nil
	})
}

func init() {
	register(toolname.StopVM, EffectMutation, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().StopVM(incus.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Stopped VM '%s'.", out["vmName"])), nil
	})
}

func init() {
	register(toolname.RestartVM, EffectMutation, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().RestartVM(incus.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Restarted VM '%s'.", out["vmName"])), nil
	})
}

func init() {
	register(toolname.UpdateVMResources, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().UpdateVMResources(incus.UpdateVMResourcesArgs{
			VMName: vmNameFromBinding(binding),
			CPUs:   intField(args, "cpus"),
			Memory: stringField(args, "memory"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Updated resources for '%s' (cpus=%s, memory=%s).", out["vmName"], out["cpus"], out["memory"])), nil
	})
}

func init() {
	register(toolname.DeleteVM, EffectDestructive, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().DeleteVM(incus.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Deleted VM '%s'.", out["vmName"])), nil
	})
}

func init() {
	register(toolname.InstallPostgreSQL, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().InstallPostgreSQL(postgres.InstallPostgreSQLArgs{
			VMName:    vmNameFromBinding(binding),
			Namespace: stringField(args, "namespace"),
			Database:  stringField(args, "database"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL is ready."), nil
	})
}

func init() {
	register(toolname.GetPostgreSQLStatus, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().GetPostgreSQLStatus(vmNameFromBinding(binding), stringField(args, "namespace"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.DeletePostgreSQL, EffectDestructive, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().DeletePostgreSQL(vmNameFromBinding(binding), stringField(args, "namespace"), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL deleted."), nil
	})
}

func init() {
	register(toolname.RunSql, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Postgres().RunSQL(vmNameFromBinding(binding), stringField(args, "namespace"), stringField(args, "database"), stringField(args, "sql"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQL completed."), nil
	})
}

func init() {
	register(toolname.RestartHostService, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().RestartHostService(host.RestartHostServiceArgs{ServiceName: stringField(args, "serviceName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Restarted service '%s'.", out["serviceName"])), nil
	})
}

func init() {
	register(toolname.SetHostServiceState, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().SetHostServiceState(host.SetHostServiceStateArgs{ServiceName: serviceNameFromBinding(args, binding), State: stringField(args, "state"), Scope: serviceScopeFromBinding(args, binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Applied service state '%s' to '%s'.", out["state"], out["serviceName"])), nil
	})
}

func init() {
	register(toolname.EnsureHostServiceSupervisor, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().EnsureHostServiceSupervisor(host.EnsureHostServiceSupervisorArgs{Scope: stringField(args, "scope")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host service supervisor is ready."), nil
	})
}

func init() {
	register(toolname.EnsureDocker, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		// EnsureDocker is an unsupported stub on Incus Linux hosts: it always errors,
		// so the success path below was dead.
		_, err := svc.Host().EnsureDocker(onData)
		return nil, err
	})
}

func init() {
	register(toolname.EnsureK3d, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		// EnsureK3d is an unsupported stub on Incus Linux hosts: it always errors,
		// so the success path below was dead.
		_, err := svc.Host().EnsureK3d(onData)
		return nil, err
	})
}

func init() {
	register(toolname.ListNamespaces, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		namespaces, err := svc.Kubernetes().ListNamespaces(vmName)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"namespaces": namespaces}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ListStorageClasses, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		storageClasses, err := svc.Kubernetes().ListStorageClasses(vmName)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"storageClasses": storageClasses}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ListIngressClasses, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		ingressClasses, err := svc.Kubernetes().ListIngressClasses(vmName)
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"classes": ingressClasses, "ingressClasses": ingressClasses}, ""), nil
	})
}

func init() {
	register(toolname.ListPods, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		namespace := stringField(args, "namespace")
		pods, err := svc.Kubernetes().ListPods(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(withBindingURI(map[string]any{"pods": pods}, binding, "cluster"), ""), nil
	})
}

func init() {
	register(toolname.ListServices, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		namespace := stringField(args, "namespace")
		services, err := svc.Kubernetes().ListServices(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(withBindingURI(map[string]any{"services": services}, binding, "cluster"), ""), nil
	})
}

func init() {
	register(toolname.ListDeployments, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		namespace := stringField(args, "namespace")
		deployments, err := svc.Kubernetes().ListDeployments(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"deployments": deployments}, ""), nil
	})
}

func init() {
	register(toolname.DiagnoseBridge, EffectRead, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().DiagnoseBridge(ctx)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.RecoverBridge, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().RecoverBridge(ctx, onData)
		if err != nil {
			return nil, err
		}
		serviceName := out.BridgeProcess.Command
		return structuredResult(out, fmt.Sprintf("Attempted bridge recovery by restarting '%s'.", serviceName)), nil
	})
}
