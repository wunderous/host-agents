package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opute-io/host-agents/internal/ops"
)

// DispatchTool executes a host MCP tool via HostOperationsService and returns an MCP CallToolResult.
func DispatchTool(ctx context.Context, svc *ops.HostOperationsService, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tool name is required")
	}

	result, err := runTool(ctx, svc, name, args, onData)
	if err != nil {
		return ErrorResult(err), nil
	}
	return result, nil
}

func runTool(ctx context.Context, svc *ops.HostOperationsService, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error) {
	switch name {
	case "get_host_info":
		out := svc.DescribeHost()
		return structuredResult(out, ""), nil

	case "list_vms":
		fast, _ := args["fast"].(bool)
		out, err := svc.ListVMs(fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "get_vm_info":
		vmName := vmNameFromArgs(args)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		fast, _ := args["fast"].(bool)
		out, err := svc.GetVMInfo(vmName, fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "agent_shell":
		command, _ := args["command"].(string)
		command = strings.TrimSpace(command)
		if command == "" {
			return nil, fmt.Errorf("command is required")
		}
		res, err := svc.RunAgentShell(command, onData)
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

	case "exec_command":
		parsed := execCommandArgs(args)
		out, err := svc.ExecCommand(parsed, onData)
		if err != nil {
			return nil, err
		}
		output, _ := out["output"].(string)
		return structuredResult(out, output), nil

	case "ensure_sql_connector":
		parsed := ops.EnsureSQLConnectorArgs{
			DatabaseID: stringField(args, "databaseId"),
			TargetHost: stringField(args, "targetHost"),
			TargetPort: intField(args, "targetPort"),
			ListenPort: intField(args, "listenPort"),
			ListenHost: stringField(args, "listenHost"),
		}
		out, err := svc.EnsureSQLConnector(parsed)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("SQL connector %s listening on %s:%d", out.DatabaseID, out.ListenHost, out.ListenPort)
		return structuredResult(out, text), nil

	case "get_sql_connector_status":
		databaseID := stringField(args, "databaseId")
		out, err := svc.GetSQLConnectorStatus(databaseID)
		if err != nil {
			return nil, err
		}
		active, _ := out["active"].(bool)
		text := "SQL connector inactive"
		if active {
			text = fmt.Sprintf("SQL connector active on %v:%v", out["listenHost"], out["listenPort"])
		}
		return structuredResult(out, text), nil

	case "release_sql_connector":
		databaseID := stringField(args, "databaseId")
		force, _ := args["force"].(bool)
		released, err := svc.ReleaseSQLConnector(databaseID, force)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("Released SQL connector %s", databaseID)
		if !released {
			text = fmt.Sprintf("No SQL connector for %s", databaseID)
		}
		return structuredResult(map[string]any{"released": released, "databaseId": databaseID}, text), nil

	case "create_vm", "provision_vm":
		parsed := provisionArgs(args)
		out, err := svc.ProvisionVM(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Created VM '%s' from image '%s'.", out.VMName, out.Image)), nil

	case "start_vm":
		out, err := svc.StartVM(ops.VMScopedArgs{VMName: stringField(args, "vmName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Started VM '%s'.", out["vmName"])), nil

	case "stop_vm":
		out, err := svc.StopVM(ops.VMScopedArgs{VMName: stringField(args, "vmName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Stopped VM '%s'.", out["vmName"])), nil

	case "restart_vm":
		out, err := svc.RestartVM(ops.VMScopedArgs{VMName: stringField(args, "vmName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Restarted VM '%s'.", out["vmName"])), nil

	case "delete_vm":
		out, err := svc.DeleteVM(ops.VMScopedArgs{VMName: stringField(args, "vmName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Deleted VM '%s'.", out["vmName"])), nil

	case "install_k3s":
		parsed := installK3sArgs(args)
		out, err := svc.InstallK3s(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "K3s installation completed."), nil

	case "uninstall_k3s":
		parsed := uninstallK3sArgs(args)
		out, err := svc.UninstallK3s(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "K3s uninstall completed."), nil

	case "configure_k3s_load_balancer":
		out, err := svc.ConfigureK3sLoadBalancer(args, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Configured K3s load balancer."), nil

	case "configure_k3s_ha_servers":
		out, err := svc.ConfigureK3sHaServers(args, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Configured K3s HA servers."), nil

	case "install_cluster_agent":
		parsed := installClusterAgentArgs(args)
		out, err := svc.InstallClusterAgent(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Cluster agent installed."), nil

	case "install_helm_chart":
		parsed := installHelmChartArgs(args)
		out, err := svc.InstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deployment initiated.", parsed.ReleaseName)), nil

	case "uninstall_helm_chart":
		parsed := uninstallHelmChartArgs(args)
		out, err := svc.UninstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deleted.", parsed.ReleaseName)), nil

	case "restart_host_service":
		out, err := svc.RestartHostService(ops.RestartHostServiceArgs{ServiceName: stringField(args, "serviceName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Restarted service '%s'.", out["serviceName"])), nil

	case "ensure_docker":
		out, err := svc.EnsureDocker(onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Docker daemon is running."), nil

	case "ensure_k3d":
		out, err := svc.EnsureK3d(onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "k3d is installed."), nil

	case "list_namespaces":
		vmName := stringField(args, "vmName")
		namespaces, err := svc.ListNamespaces(vmName)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"namespaces": namespaces}
		return structuredResult(out, ""), nil

	case "list_storage_classes":
		vmName := stringField(args, "vmName")
		storageClasses, err := svc.ListStorageClasses(vmName)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"storageClasses": storageClasses}
		return structuredResult(out, ""), nil

	case "list_pods":
		vmName := stringField(args, "vmName")
		namespace := stringField(args, "namespace")
		pods, err := svc.ListPods(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"pods": pods}, ""), nil

	case "list_services":
		vmName := stringField(args, "vmName")
		namespace := stringField(args, "namespace")
		services, err := svc.ListServices(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"services": services}, ""), nil

	case "list_deployments":
		vmName := stringField(args, "vmName")
		namespace := stringField(args, "namespace")
		deployments, err := svc.ListDeployments(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"deployments": deployments}, ""), nil

	case "diagnose_bridge":
		out, err := svc.DiagnoseBridge(ctx)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "recover_bridge":
		out, err := svc.RecoverBridge(ctx, onData)
		if err != nil {
			return nil, err
		}
		serviceName := out.BridgeProcess.Command
		return structuredResult(out, fmt.Sprintf("Attempted bridge recovery by restarting '%s'.", serviceName)), nil

	default:
		if IsOmittedToolName(name) {
			return nil, fmt.Errorf("tool '%s' is not available in the Go host agent (bridge-backed capability omitted)", name)
		}
		return nil, fmt.Errorf("tool not found: %s", name)
	}
}

func structuredResult(structured any, text string) *mcp.CallToolResult {
	if text == "" {
		b, _ := json.Marshal(structured)
		text = string(b)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: structured,
	}
}

// ErrorResult builds an MCP error tool result.
func ErrorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
		IsError: true,
	}
}

func vmNameFromArgs(args map[string]any) string {
	if v := stringField(args, "vmName"); v != "" {
		return v
	}
	return stringField(args, "name")
}

func stringField(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func intField(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	default:
		return 0
	}
}

func provisionArgs(args map[string]any) ops.ProvisionVMArgs {
	vmName := stringField(args, "vmName")
	if vmName == "" {
		vmName = stringField(args, "name")
	}
	return ops.ProvisionVMArgs{
		VMName: vmName,
		Image:  stringField(args, "image"),
		CPUs:   intField(args, "cpus"),
		Memory: stringField(args, "memory"),
		Disk:   stringField(args, "disk"),
	}
}

func installK3sArgs(args map[string]any) ops.InstallK3sArgs {
	var installArgs []string
	if raw, ok := args["installArgs"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok {
				installArgs = append(installArgs, s)
			}
		}
	}
	return ops.InstallK3sArgs{
		Target:      stringField(args, "target"),
		VMName:      stringField(args, "vmName"),
		ClusterID:   stringField(args, "clusterId"),
		InstallArgs: installArgs,
	}
}

func uninstallK3sArgs(args map[string]any) ops.UninstallK3sArgs {
	return ops.UninstallK3sArgs{
		Target:    stringField(args, "target"),
		VMName:    stringField(args, "vmName"),
		ClusterID: stringField(args, "clusterId"),
	}
}

func installClusterAgentArgs(args map[string]any) ops.InstallClusterAgentArgs {
	return ops.InstallClusterAgentArgs{
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
	}
}

func execCommandArgs(args map[string]any) ops.ExecCommandArgs {
	var argv []string
	if raw, ok := args["args"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok {
				argv = append(argv, s)
			}
		}
	}
	return ops.ExecCommandArgs{
		VMName:    vmNameFromArgs(args),
		Command:   stringField(args, "command"),
		Args:      argv,
		TimeoutMs: intField(args, "timeout"),
	}
}

func installHelmChartArgs(args map[string]any) ops.InstallHelmChartArgs {
	releaseName := stringField(args, "releaseName")
	if releaseName == "" {
		releaseName = stringField(args, "chartName")
	}
	if releaseName == "" {
		releaseName = stringField(args, "name")
	}
	chartSource := stringField(args, "chartSource")
	if chartSource == "" {
		chartSource = stringField(args, "chart")
	}
	namespace := stringField(args, "namespace")
	if namespace == "" {
		namespace = "kube-system"
	}
	return ops.InstallHelmChartArgs{
		VMName:      vmNameFromArgs(args),
		ReleaseName: releaseName,
		ChartSource: chartSource,
		Namespace:   namespace,
		Repo:        stringField(args, "repo"),
		Values:      ops.HelmValuesYAML(args["values"]),
	}
}

func uninstallHelmChartArgs(args map[string]any) ops.UninstallHelmChartArgs {
	releaseName := stringField(args, "releaseName")
	if releaseName == "" {
		releaseName = stringField(args, "chartName")
	}
	if releaseName == "" {
		releaseName = stringField(args, "name")
	}
	namespace := stringField(args, "namespace")
	if namespace == "" {
		namespace = "kube-system"
	}
	return ops.UninstallHelmChartArgs{
		VMName:      vmNameFromArgs(args),
		ReleaseName: releaseName,
		Namespace:   namespace,
	}
}
