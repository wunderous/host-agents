package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/ops"
)

// DispatchTool executes a host MCP tool via HostOperationsService and returns an MCP CallToolResult.
func DispatchTool(ctx context.Context, svc *ops.HostOperationsService, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if err := ctx.Err(); err != nil {
		return ErrorResult(err), nil
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

	case "check_local_prerequisites":
		out, err := svc.CheckLocalPrerequisites()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "get_local_status":
		out, err := svc.GetLocalStatus()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "check_local_llm_prerequisites":
		out, err := svc.CheckLocalLLMPrerequisites()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "list_local_llm_models", "probe_local_llm":
		if boolField(args, "prerequisites") {
			out, err := svc.CheckLocalLLMPrerequisites()
			if err != nil {
				return nil, err
			}
			return structuredResult(out, ""), nil
		}
		out, err := svc.ProbeLocalLLM(ctx, boolField(args, "includeChat"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "install_local_llm_model":
		out, err := svc.InstallLocalLLMModel(ctx, stringField(args, "modelRef"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local Ollama model is ready"), nil

	case "start_local_llm_runtime":
		out, err := svc.StartLocalLLMRuntime(ctx)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local Ollama runtime is ready"), nil

	case "stop_local_llm_runtime":
		if err := svc.StopLocalLLMRuntime(ctx); err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"stopped": true}, "Local Ollama runtime stopped"), nil

	case "remove_local_llm_model":
		if err := svc.RemoveLocalLLMModel(ctx, stringField(args, "modelRef")); err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"removed": true}, "Local Ollama model removed"), nil

	case "ensure_local_llm_relay":
		out, err := svc.EnsureLocalLLMRelay(ctx, ops.LocalLLMRelayArgs{SessionID: stringField(args, "sessionId"), ListenHost: stringField(args, "listenHost"), ListenPort: intField(args, "listenPort"), TargetHost: stringField(args, "targetHost"), TargetPort: intField(args, "targetPort"), IncomingToken: stringField(args, "incomingToken"), UpstreamToken: stringField(args, "upstreamToken"), AllowedSourceCIDRs: stringSliceField(args, "allowedSourceCIDRs"), RelayToken: stringField(args, "relayToken"), AllowedSourceIP: stringField(args, "allowedSourceIP")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM relay is ready"), nil

	case "remove_local_llm_relay":
		out, err := svc.RemoveLocalLLMRelay(stringField(args, "sessionId"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM relay removed"), nil

	case "ensure_local_llm_k3s_proxy":
		out, err := svc.EnsureLocalLLMK3sProxy(ops.LocalLLMK3sProxyArgs{VMName: vmNameFromArgs(args), NodePort: intField(args, "nodePort"), RelayHost: stringField(args, "relayHost"), RelayPort: intField(args, "relayPort"), RelayToken: stringField(args, "relayToken"), BearerKey: stringField(args, "bearerKey")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM K3s proxy is ready"), nil

	case "remove_local_llm_k3s_proxy":
		out, err := svc.RemoveLocalLLMK3sProxy(vmNameFromArgs(args))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM K3s proxy removed"), nil

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

	case "ensure_cloudflared_tunnel":
		parsed := ops.EnsureCloudflaredTunnelArgs{
			BindingID:   stringField(args, "bindingId"),
			Hostname:    stringField(args, "hostname"),
			LocalTarget: stringField(args, "localTarget"),
			RunToken:    stringField(args, "runToken"),
			Quick:       boolField(args, "quick"),
			Native:      boolField(args, "native"),
		}
		out, err := svc.EnsureCloudflaredTunnel(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Tunnel ready for %s", out.Hostname)), nil

	case "remove_local_llm_cloudflared_tunnel":
		out, err := svc.RemoveHostExposure(ops.RemoveHostExposureArgs{BindingID: stringField(args, "bindingId")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM Cloudflare connector removed"), nil

	case "ensure_platform_opute_stack":
		parsed := ops.EnsurePlatformOputeStackArgs{
			RepoRoot: stringField(args, "repoRoot"),
		}
		out, err := svc.EnsurePlatformOputeStack(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Platform.opute.io stack is ready on 919x"), nil

	case "provision_platform_opute_tunnel":
		parsed := ops.ProvisionPlatformOputeTunnelArgs{
			RepoRoot: stringField(args, "repoRoot"),
		}
		out, err := svc.ProvisionPlatformOputeTunnel(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Provisioned platform.opute.io Cloudflare tunnel"), nil

	case "probe_host_exposure":
		parsed := ops.ProbeHostExposureArgs{
			BindingID:   stringField(args, "bindingId"),
			LocalTarget: stringField(args, "localTarget"),
		}
		out, err := svc.ProbeHostExposure(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Exposure summary: %s", out.Summary)), nil

	case "remove_host_exposure":
		parsed := ops.RemoveHostExposureArgs{
			BindingID: stringField(args, "bindingId"),
		}
		out, err := svc.RemoveHostExposure(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Removed host exposure"), nil

	case "ensure_host_firewall_rule":
		parsed := ops.EnsureHostFirewallRuleArgs{
			BindingID: stringField(args, "bindingId"),
			Port:      intField(args, "port"),
		}
		out, err := svc.EnsureHostFirewallRule(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Firewall rule applied=%v", out.Applied)), nil

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
		out, err := svc.InstallK3s(ctx, parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "K3s installation completed."), nil

	case "get_k3s_status":
		vmName := stringField(args, "vmName")
		out, err := svc.GetK3sStatus(vmName)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "uninstall_k3s":
		parsed := uninstallK3sArgs(args)
		out, err := svc.UninstallK3s(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "K3s uninstall completed."), nil

	case "install_postgresql":
		out, err := svc.InstallPostgreSQL(ops.InstallPostgreSQLArgs{
			VMName:    stringField(args, "vmName"),
			Namespace: stringField(args, "namespace"),
			Database:  stringField(args, "database"),
			Password:  stringField(args, "password"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL is ready."), nil

	case "get_postgresql_status":
		out, err := svc.GetPostgreSQLStatus(stringField(args, "vmName"), stringField(args, "namespace"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "delete_postgresql":
		out, err := svc.DeletePostgreSQL(stringField(args, "vmName"), stringField(args, "namespace"), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL deleted."), nil

	case "run_sql":
		out, err := svc.RunSQL(stringField(args, "vmName"), stringField(args, "namespace"), stringField(args, "database"), stringField(args, "sql"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQL completed."), nil

	case "create_cloudflare_tunnel":
		out, err := svc.EnsureCloudflaredTunnel(ops.EnsureCloudflaredTunnelArgs{
			BindingID:   stringField(args, "bindingId"),
			Hostname:    stringField(args, "hostname"),
			LocalTarget: stringField(args, "localTarget"),
			RunToken:    stringField(args, "runToken"),
			Quick:       boolField(args, "quick"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Cloudflare Tunnel started."), nil

	case "get_cloudflare_tunnel_status":
		out, err := svc.ProbeHostExposure(ops.ProbeHostExposureArgs{BindingID: stringField(args, "bindingId"), LocalTarget: stringField(args, "localTarget")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "delete_cloudflare_tunnel":
		out, err := svc.RemoveHostExposure(ops.RemoveHostExposureArgs{BindingID: stringField(args, "bindingId")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Cloudflare Tunnel deleted."), nil

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

	case "restart_cluster_agent":
		out, err := svc.RestartClusterAgent(vmNameFromArgs(args), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Cluster agent restarted."), nil

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

	case "apply_manifest":
		out, err := svc.ApplyManifest(ops.ApplyManifestArgs{VMName: vmNameFromArgs(args), Manifest: stringField(args, "manifest")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes manifest applied."), nil

	case "put_k8s_secret":
		data := map[string]string{}
		if raw, ok := args["data"].(map[string]any); ok {
			for key, value := range raw {
				if text, ok := value.(string); ok {
					data[key] = text
				}
			}
		}
		out, err := svc.PutK8sSecret(ops.PutK8sSecretArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Data: data}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes Secret configured."), nil

	case "get_k8s_resource":
		out, err := svc.GetK8sResource(ops.K8sResourceArgs{VMName: vmNameFromArgs(args), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "delete_k8s_resource":
		out, err := svc.DeleteK8sResource(ops.K8sResourceArgs{VMName: vmNameFromArgs(args), Kind: stringField(args, "kind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes resource deleted."), nil

	case "get_k8s_resource_status":
		out, err := svc.GetK8sResourceStatus(ops.K8sResourceArgs{VMName: vmNameFromArgs(args), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "install_oci_registry":
		out, err := svc.InstallOCIRegistry(ops.InstallOCIRegistryArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Image: stringField(args, "image"), StorageSize: stringField(args, "storageSize"), StorageClass: stringField(args, "storageClass"), NodePort: intField(args, "nodePort")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI registry deployment initiated."), nil

	case "get_oci_registry_status":
		out, err := svc.GetOCIRegistryStatus(ops.InstallOCIRegistryArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace"), Name: stringField(args, "name")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "delete_oci_registry":
		out, err := svc.DeleteOCIRegistry(ops.InstallOCIRegistryArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI registry deleted."), nil

	case "configure_k3s_registry":
		out, err := svc.ConfigureK3sRegistry(ops.ConfigureK3sRegistryArgs{VMName: vmNameFromArgs(args), Endpoint: stringField(args, "endpoint"), Registry: stringField(args, "registry"), Insecure: boolField(args, "insecure")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "K3s registry configuration applied."), nil

	case "configure_service_domain":
		out, err := svc.ConfigureServiceDomain(ops.ConfigureServiceDomainArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName"), Hostname: stringField(args, "hostname"), ServiceName: stringField(args, "serviceName"), ServicePort: intField(args, "servicePort"), IngressClass: stringField(args, "ingressClass")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping configured."), nil

	case "remove_service_domain":
		out, err := svc.RemoveServiceDomain(ops.ConfigureServiceDomainArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping removed."), nil

	case "install_cloudflared_connector":
		out, err := svc.InstallCloudflaredConnector(ops.InstallCloudflaredConnectorArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Token: stringField(args, "token"), Image: stringField(args, "image"), Replicas: intField(args, "replicas"), LocalTargets: cloudflaredLocalTargets(args["localTargets"])}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "In-cluster Cloudflare connector deployment initiated."), nil

	case "delete_cloudflared_connector":
		out, err := svc.DeleteCloudflaredConnector(ops.InstallCloudflaredConnectorArgs{VMName: vmNameFromArgs(args), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "In-cluster Cloudflare connector deleted."), nil

	case "restart_host_service":
		out, err := svc.RestartHostService(ops.RestartHostServiceArgs{ServiceName: stringField(args, "serviceName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Restarted service '%s'.", out["serviceName"])), nil

	case "set_host_service_state":
		out, err := svc.SetHostServiceState(ops.SetHostServiceStateArgs{ServiceName: stringField(args, "serviceName"), State: stringField(args, "state"), Scope: stringField(args, "scope")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Applied service state '%s' to '%s'.", out["state"], out["serviceName"])), nil

	case "ensure_docker":
		out, err := svc.EnsureDocker(onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Docker daemon is running."), nil

	case "ensure_oci_builder":
		out, err := svc.EnsureOciBuilder(ops.EnsureOciBuilderArgs{Builder: stringField(args, "builder")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host OCI image builder is available."), nil

	case "ensure_host_tool":
		out, err := svc.EnsureHostTool(ops.EnsureHostToolArgs{Tool: stringField(args, "tool")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Generic host tool is available."), nil

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

func stringSliceField(args map[string]any, key string) []string {
	values, ok := args[key].([]any)
	if !ok {
		if typed, typedOK := args[key].([]string); typedOK {
			return typed
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func boolField(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
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

func cloudflaredLocalTargets(value any) []ops.CloudflaredLocalTarget {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	targets := make([]ops.CloudflaredLocalTarget, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		targets = append(targets, ops.CloudflaredLocalTarget{LocalPort: intField(obj, "localPort"), Target: stringField(obj, "target")})
	}
	return targets
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
