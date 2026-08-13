package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/resource"
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

	case "embed_texts":
		texts, ok := args["texts"].([]any)
		if !ok {
			if typed, typedOK := args["texts"].([]string); typedOK {
				texts = make([]any, len(typed))
				for index, value := range typed {
					texts[index] = value
				}
			}
		}
		values := make([]string, len(texts))
		for index, value := range texts {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("texts[%d] must be a string", index)
			}
			values[index] = text
		}
		out, err := svc.EmbedTexts(ctx, ops.EmbedTextsArgs{Texts: values})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "reconcile_serving_assignment":
		out, err := svc.ReconcileServingAssignment(servingAssignmentArgs(args), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Serving assignment reconciled."), nil

	case "list_vms":
		fast, _ := args["fast"].(bool)
		out, err := svc.ListVMs(fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "list_clusters":
		fast, _ := args["fast"].(bool)
		out, err := svc.ListClusters(fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "get_cluster_details":
		vmName := vmNameFromArgs(args)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		fast, _ := args["fast"].(bool)
		out, err := svc.GetClusterDetails(vmName, fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "get_cluster_runtime_details":
		vmName := vmNameFromArgs(args)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		out, err := svc.GetClusterRuntimeDetails(vmName)
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

	case "install_incus_stack":
		out, err := svc.InstallIncusStack(ops.InstallIncusStackArgs{
			IncusPackage: stringField(args, "incusPackage"), QemuPackage: stringField(args, "qemuPackage"),
			GPUPackages:  stringSliceField(args, "gpuPackages"),
			IncusChannel: stringField(args, "incusChannel"), IncusVersion: stringField(args, "incusVersion"),
			InstallQEMU: boolField(args, "installQemu"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Incus virtualization prerequisites installed"), nil

	case "probe_incus_gpu":
		out, err := svc.ProbeIncusGPU(args)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Incus GPU capability report generated"), nil

	case "provision_container":
		nesting := optionalBoolField(args, "nesting")
		out, err := svc.ProvisionContainer(ops.ProvisionContainerArgs{
			ContainerName: stringField(args, "containerName"),
			Image:         stringField(args, "image"),
			CPUs:          intField(args, "cpus"),
			Memory:        stringField(args, "memory"),
			Disk:          stringField(args, "disk"),
			GPU:           boolField(args, "gpu"),
			WSLGpuLibs:    boolField(args, "wslGpuLibs"),
			Nesting:       nesting,
			Port:          intField(args, "port"),
			ModelVolume:   stringField(args, "modelVolume"),
		}, onData)
		if err != nil {
			return nil, err
		}
		payload := map[string]any{
			"containerName": out.ContainerName,
			"vmName":        out.ContainerName,
			"image":         out.Image,
			"status":        out.Status,
			"instanceType":  out.InstanceType,
		}
		return structuredResult(payload, fmt.Sprintf("Provisioned Incus container '%s'.", out.ContainerName)), nil

	case "probe_gpu_container":
		out, err := svc.ProbeGPUContainer(onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "System container GPU capability report generated"), nil

	case "check_local_llm_prerequisites":
		out, err := svc.CheckLlamaServerPrerequisites()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "ensure_local_llm_server_binary":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		out, err := svc.EnsureLlamaServerBinary(ctx, ops.BuildLlamaServerBinaryArgs{
			SourceURI:         stringField(args, "sourceUri"),
			SourceSHA256:      stringField(args, "sourceSha256"),
			SourceRevision:    stringField(args, "sourceRevision"),
			OutputPath:        stringField(args, "outputPath"),
			CudaArchitectures: stringField(args, "cudaArchitectures"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "CUDA llama-server binary is built and verified"), nil

	case "list_local_llm_models", "probe_local_llm":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		out, err := svc.ProbeLlamaServer(ctx, ops.ProbeLlamaServerArgs{
			IncludeChat: boolField(args, "includeChat"),
			ModelRef:    stringField(args, "modelRef"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "install_local_llm_model":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		out, err := svc.InstallLlamaServerModel(ctx, ops.InstallLlamaServerModelArgs{
			ModelRef:                stringField(args, "modelRef"),
			ArtifactPath:            stringField(args, "artifactPath"),
			ArtifactSHA256:          stringField(args, "artifactSha256"),
			ArtifactURI:             stringField(args, "artifactUri"),
			BaseModel:               stringField(args, "baseModel"),
			Revision:                stringField(args, "revision"),
			TokenizerRevision:       stringField(args, "tokenizerRevision"),
			ChatTemplateHash:        stringField(args, "chatTemplateHash"),
			Quantization:            stringField(args, "quantization"),
			ChatTemplate:            stringField(args, "chatTemplate"),
			ChatTemplateKwargs:      stringField(args, "chatTemplateKwargs"),
			ContextSize:             intField(args, "contextSize"),
			GpuLayers:               intField(args, "gpuLayers"),
			BinaryPath:              stringField(args, "binaryPath"),
			BinaryVersion:           stringField(args, "binaryVersion"),
			BinarySHA256:            stringField(args, "binarySha256"),
			BinaryURI:               stringField(args, "binaryUri"),
			BinarySource:            stringField(args, "binarySource"),
			SourceRevision:          stringField(args, "sourceRevision"),
			SourceSHA256:            stringField(args, "sourceSha256"),
			CudaEnabled:             boolField(args, "cudaEnabled"),
			CudaArchitectures:       stringField(args, "cudaArchitectures"),
			BinaryBuildSourceURI:    stringField(args, "binaryBuildSourceUri"),
			BinaryBuildSourceSHA256: stringField(args, "binaryBuildSourceSha256"),
			BinaryBuildRevision:     stringField(args, "binaryBuildRevision"),
			Port:                    optionalIntField(args, "port"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "llama-server model is ready"), nil

	case "configure_local_llm_model":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return nil, fmt.Errorf("configure_local_llm_model is retired; llama-server uses the pinned artifact manifest")

	case "start_local_llm_runtime":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		out, err := svc.StartLlamaServerRuntime(ctx)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "llama-server runtime is ready"), nil

	case "configure_local_llm_runtime":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return nil, fmt.Errorf("configure_local_llm_runtime is retired; configure llama-server through its pinned service manifest")

	case "stop_local_llm_runtime":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		if err := svc.StopLlamaServerRuntime(ctx); err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"stopped": true}, "llama-server runtime stopped"), nil

	case "reconcile_postgresql_service":
		out, err := svc.ReconcilePostgreSQLService(ctx, postgresqlServiceArgs(args), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service is ready"), nil
	case "get_postgresql_service_status":
		out, err := svc.GetPostgreSQLServiceStatus(ctx, postgresqlServiceArgs(args))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service status returned"), nil
	case "remove_postgresql_service":
		out, err := svc.RemovePostgreSQLService(ctx, postgresqlServiceArgs(args), boolField(args, "confirm"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service was removed"), nil

	case "reconcile_tidb_service":
		out, err := svc.ReconcileTiDBService(ctx, ops.TiDBServiceArgs{VMName: stringField(args, "vmName"), ClusterName: stringField(args, "clusterName"), Namespace: stringField(args, "namespace"), PDReplicas: intField(args, "pdReplicas"), TiKVReplicas: intField(args, "tikvReplicas"), TiDBReplicas: intField(args, "tidbReplicas"), StorageClass: stringField(args, "storageClass"), StorageSize: stringField(args, "storageSize"), TiDBVersion: stringField(args, "tidbVersion"), RetentionPolicy: stringField(args, "retentionPolicy")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "TiDB service is ready"), nil

	case "get_tidb_service_status":
		out, err := svc.GetTiDBServiceStatus(ctx, ops.TiDBServiceArgs{VMName: stringField(args, "vmName"), ClusterName: stringField(args, "clusterName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "TiDB service status returned"), nil

	case "remove_tidb_service":
		out, err := svc.RemoveTiDBService(ctx, ops.TiDBServiceArgs{VMName: stringField(args, "vmName"), ClusterName: stringField(args, "clusterName"), Namespace: stringField(args, "namespace"), RetentionPolicy: stringField(args, "retentionPolicy")}, boolField(args, "confirm"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "TiDB service was removed"), nil

	case "ensure_pgvector":
		out, err := svc.EnsurePgVector(ctx, ops.PgVectorArgs{
			VMName:      stringField(args, "vmName"),
			ClusterName: stringField(args, "clusterName"),
			Namespace:   stringField(args, "namespace"),
			Databases:   stringSliceField(args, "databases"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "pgvector is ready"), nil

	case "get_pgvector_status":
		out, err := svc.GetPgVectorStatus(ctx, ops.PgVectorArgs{
			VMName:      stringField(args, "vmName"),
			ClusterName: stringField(args, "clusterName"),
			Namespace:   stringField(args, "namespace"),
			Databases:   stringSliceField(args, "databases"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "pgvector status returned"), nil

	case "reset_incus_stack":
		out, err := svc.ResetIncusStack(ctx, ops.ResetIncusStackArgs{
			InstanceNames:               stringSliceField(args, "instanceNames"),
			InstancePrefix:              stringField(args, "instancePrefix"),
			Confirm:                     boolField(args, "confirm"),
			Reinstall:                   boolField(args, "reinstall"),
			DryRun:                      boolField(args, "dryRun"),
			DisposableHostFingerprint:   stringField(args, "disposableHostFingerprint"),
			ExpectedHostFingerprint:     stringField(args, "expectedHostFingerprint"),
			DisposableHostAuthorization: stringField(args, "disposableHostAuthorization"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Incus stack reset phase completed"), nil

	case "remove_local_llm_model":
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return structuredResult(map[string]any{"removed": false, "purged": false, "reason": "llama-server artifacts are retained by the artifact manifest"}, "llama-server model removal is not an inference operation"), nil

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
		out, err := svc.EnsureLocalLLMK3sProxy(ops.LocalLLMK3sProxyArgs{
			VMName:         vmNameFromArgs(args),
			Namespace:      stringField(args, "namespace"),
			SecretName:     stringField(args, "secretName"),
			ConfigMapName:  stringField(args, "configMapName"),
			DeploymentName: stringField(args, "deploymentName"),
			ServiceName:    stringField(args, "serviceName"),
			ContainerImage: stringField(args, "containerImage"),
			NodePort:       intField(args, "nodePort"),
			RelayHost:      stringField(args, "relayHost"),
			RelayPort:      intField(args, "relayPort"),
			RelayToken:     stringField(args, "relayToken"),
			BearerKey:      stringField(args, "bearerKey"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM K3s proxy is ready"), nil

	case "remove_local_llm_k3s_proxy":
		out, err := svc.RemoveLocalLLMK3sProxy(vmNameFromArgs(args), stringField(args, "namespace"))
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

	case "discover_service_ingress":
		rawEndpoints, _ := args["endpoints"].([]any)
		endpoints := make([]ops.ServiceIngressEndpoint, 0, len(rawEndpoints))
		for _, raw := range rawEndpoints {
			if row, ok := raw.(map[string]any); ok {
				endpoints = append(endpoints, ops.ServiceIngressEndpoint{Name: stringField(row, "name"), Hostname: stringField(row, "hostname")})
			}
		}
		out, err := svc.DiscoverServiceIngress(ops.DiscoverServiceIngressArgs{VMName: vmNameFromArgs(args), Endpoints: endpoints, IngressNamespace: stringField(args, "ingressNamespace"), IngressService: stringField(args, "ingressService")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Discovered service ingress endpoints."), nil

	case "agent_shell", "run_host_command":
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
			BindingID:         stringField(args, "bindingId"),
			Hostname:          stringField(args, "hostname"),
			LocalTarget:       stringField(args, "localTarget"),
			RunToken:          stringField(args, "runToken"),
			Connector:         stringField(args, "connector"),
			AllowedLocalPorts: intSliceField(args, "allowedLocalPorts"),
			Quick:             boolField(args, "quick"),
			Native:            boolField(args, "native"),
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

	case "create_vm":
		parsed := provisionArgs(args)
		if strings.TrimSpace(parsed.InstanceType) == "" {
			parsed.InstanceType = "virtual-machine"
		}
		out, err := svc.ProvisionVM(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Created VM '%s' from image '%s'.", out.VMName, out.Image)), nil

	case "provision_vm":
		parsed := provisionArgs(args)
		out, err := svc.ProvisionVM(parsed, onData)
		if err != nil {
			return nil, err
		}
		kind := strings.TrimSpace(out.InstanceType)
		if kind == "" {
			kind = "container"
		}
		return structuredResult(out, fmt.Sprintf("Provisioned %s '%s' from image '%s'.", kind, out.VMName, out.Image)), nil

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

	case "update_vm_resources":
		out, err := svc.UpdateVMResources(ops.UpdateVMResourcesArgs{
			VMName: stringField(args, "vmName"),
			CPUs:   intField(args, "cpus"),
			Memory: stringField(args, "memory"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Updated resources for '%s' (cpus=%s, memory=%s).", out["vmName"], out["cpus"], out["memory"])), nil

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
			BindingID:         stringField(args, "bindingId"),
			Hostname:          stringField(args, "hostname"),
			LocalTarget:       stringField(args, "localTarget"),
			RunToken:          stringField(args, "runToken"),
			Connector:         stringField(args, "connector"),
			AllowedLocalPorts: intSliceField(args, "allowedLocalPorts"),
			Quick:             boolField(args, "quick"),
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

	case "restart_cluster":
		out, err := svc.RestartCluster(vmNameFromArgs(args), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "K3s cluster restarted."), nil

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
		out, err := svc.InstallCloudflaredConnector(ops.InstallCloudflaredConnectorArgs{VMName: vmNameFromArgs(args), Target: stringField(args, "target"), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Token: stringField(args, "token"), Image: stringField(args, "image"), Replicas: intField(args, "replicas"), LocalTargets: cloudflaredLocalTargets(args["localTargets"])}, onData)
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

	case "configure_agent_connection":
		env := map[string]string{}
		if raw, ok := args["environment"].(map[string]any); ok {
			for key, value := range raw {
				if text, ok := value.(string); ok {
					env[key] = text
				}
			}
		}
		out, err := svc.ConfigureAgentConnection(ops.ConfigureAgentConnectionArgs{EnvFile: stringField(args, "envFile"), Environment: env, ServiceName: stringField(args, "serviceName"), Restart: optionalBoolField(args, "restart"), Scope: stringField(args, "scope")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Agent connection configuration written."), nil

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

	case "configure_oci_storage":
		out, err := svc.ConfigureOciStorage(ctx, ops.ConfigureOciStorageArgs{
			Runtime:       stringField(args, "runtime"),
			MaxBytes:      optionalInt64Field(args, "maxBytes"),
			MinAgeSeconds: optionalInt64Field(args, "minAgeSeconds"),
			PruneNow:      boolField(args, "pruneNow"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI image storage retention policy applied."), nil

	case "inspect_container_storage":
		out, err := svc.InspectContainerStorage(ctx, ops.InspectContainerStorageArgs{
			Runtime: stringField(args, "runtime"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Container runtime storage usage inspected."), nil

	case "cleanup_container_storage":
		out, err := svc.CleanupContainerStorage(ctx, ops.CleanupContainerStorageArgs{
			Runtime:       stringField(args, "runtime"),
			MaxBytes:      optionalInt64Field(args, "maxBytes"),
			MinAgeSeconds: optionalInt64Field(args, "minAgeSeconds"),
			DryRun:        boolField(args, "dryRun"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Container runtime storage cleanup completed."), nil

	case "build_and_push_oci_image":
		out, err := svc.BuildAndPushOciImage(ctx, ops.BuildAndPushOciImageArgs{
			ContextDir:       stringField(args, "contextDir"),
			Dockerfile:       stringField(args, "dockerfile"),
			Image:            stringField(args, "image"),
			Builder:          stringField(args, "builder"),
			InsecureRegistry: boolField(args, "insecureRegistry"),
			Platform:         stringField(args, "platform"),
			BuildArgs:        stringMapField(args, "buildArgs"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Built and pushed %s.", out["image"])), nil

	case "stage_build_context":
		files := map[string]string{}
		if raw, ok := args["files"].(map[string]any); ok {
			for key, value := range raw {
				if text, ok := value.(string); ok {
					files[key] = text
				}
			}
		}
		out, err := svc.StageBuildContext(ops.StageBuildContextArgs{
			DestDir:      stringField(args, "destDir"),
			Files:        files,
			FileEncoding: stringField(args, "fileEncoding"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Staged %v files into build context.", out["fileCount"])), nil

	case "ensure_host_tool":
		out, err := svc.EnsureHostTool(ops.EnsureHostToolArgs{Tool: stringField(args, "tool")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Generic host tool is available."), nil

	case "render_helm_template":
		out, err := svc.RenderHelmTemplate(ops.RenderHelmTemplateArgs{
			ChartPath:   stringField(args, "chartPath"),
			ReleaseName: stringField(args, "releaseName"),
			ValuesFiles: stringSliceField(args, "valuesFiles"),
			Set:         stringSliceField(args, "set"),
			Namespace:   stringField(args, "namespace"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Helm chart rendered."), nil

	case "prepare_host_agent_artifacts":
		out, err := svc.PrepareHostAgentArtifacts(ops.PrepareHostAgentArtifactsArgs{
			SourceDir: stringField(args, "sourceDir"),
			DestDir:   stringField(args, "destDir"),
			Archs:     stringSliceField(args, "archs"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host-agent build artifacts are ready."), nil

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

	case "list_ingress_classes":
		vmName := stringField(args, "vmName")
		ingressClasses, err := svc.ListIngressClasses(vmName)
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"classes": ingressClasses, "ingressClasses": ingressClasses}, ""), nil

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
	if admissionErr, ok := err.(*resource.AdmissionError); ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
			StructuredContent: map[string]any{
				"code":         admissionErr.Code,
				"class":        admissionErr.Class,
				"pressure":     admissionErr.Pressure,
				"reason":       admissionErr.Reason,
				"retryAfterMs": admissionErr.RetryAfterMs,
			},
			IsError: true,
		}
	}
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

func postgresqlServiceRelayArgs(args map[string]any) *ops.PostgreSQLServiceRelayArgs {
	raw, ok := args["localRelay"].(map[string]any)
	if !ok || raw == nil {
		return nil
	}
	return &ops.PostgreSQLServiceRelayArgs{
		SessionID:  stringField(raw, "sessionId"),
		ListenHost: stringField(raw, "listenHost"),
		ListenPort: intField(raw, "listenPort"),
		TargetHost: stringField(raw, "targetHost"),
		TargetPort: intField(raw, "targetPort"),
		TTLSeconds: intField(raw, "ttlSeconds"),
		RelayToken: stringField(raw, "relayToken"),
	}
}

func postgresqlServiceArgs(args map[string]any) ops.PostgreSQLServiceArgs {
	return ops.PostgreSQLServiceArgs{
		VMName: stringField(args, "vmName"), ClusterName: stringField(args, "clusterName"), Namespace: stringField(args, "namespace"),
		Instances: intField(args, "instances"), StorageClass: stringField(args, "storageClass"), StorageSize: stringField(args, "storageSize"),
		RetentionPolicy: stringField(args, "retentionPolicy"), RestartConsumers: optionalBoolField(args, "restartConsumers"),
		Databases: uniqueStringSlice(stringSliceField(args, "databases")), ConsumerDatabaseKeys: stringMapField(args, "consumerDatabaseKeys"),
		ConsumerSecretName: stringField(args, "consumerSecretName"), ConsumerSecretLabel: stringField(args, "consumerSecretLabel"),
		ServiceOwner: stringField(args, "serviceOwner"), ServicePartOf: stringField(args, "servicePartOf"), RelayDeviceName: stringField(args, "relayDeviceName"),
		Relay: postgresqlServiceRelayArgs(args),
	}
}

func resolveLocalLLMModelArg(args map[string]any) (string, error) {
	if modelRef := stringField(args, "modelRef"); modelRef != "" {
		return modelRef, nil
	}
	switch stringField(args, "modelPreset") {
	case "", "qwen3.5", "qwen3.5-0.8b":
		return "qwen3.5-0.8b/base-llama", nil
	default:
		return "", fmt.Errorf("unsupported llama-server model preset %q", stringField(args, "modelPreset"))
	}
}

// localLLMRuntime defaults old payloads to the only supported production
// runtime. Explicit legacy runtime values are rejected by the dispatch paths.
func localLLMRuntime(args map[string]any) string {
	if runtime := stringField(args, "runtime"); runtime != "" {
		return runtime
	}
	return "llama-cpp"
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

func uniqueStringSlice(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func stringMapField(args map[string]any, key string) map[string]string {
	raw, ok := args[key].(map[string]any)
	if !ok || raw == nil {
		return nil
	}
	result := make(map[string]string, len(raw))
	for name, value := range raw {
		if text, ok := value.(string); ok {
			result[name] = text
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

func intSliceField(args map[string]any, key string) []int {
	values, ok := args[key].([]any)
	if !ok {
		if typed, typedOK := args[key].([]int); typedOK {
			return typed
		}
		return nil
	}
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, intValue(value))
	}
	return result
}

func intValue(value any) int {
	switch v := value.(type) {
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
		if err == nil {
			return int(n)
		}
	}
	return 0
}

func int64Field(args map[string]any, key string) int64 {
	switch v := args[key].(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

func optionalIntField(args map[string]any, key string) *int {
	if args == nil {
		return nil
	}
	if _, ok := args[key]; !ok {
		return nil
	}
	v := intField(args, key)
	return &v
}

func optionalInt64Field(args map[string]any, key string) *int64 {
	if args == nil {
		return nil
	}
	if _, ok := args[key]; !ok {
		return nil
	}
	v := int64Field(args, key)
	return &v
}

func optionalBoolField(args map[string]any, key string) *bool {
	if args == nil {
		return nil
	}
	if _, ok := args[key]; !ok {
		return nil
	}
	v := boolField(args, key)
	return &v
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
		VMName:       vmName,
		Image:        stringField(args, "image"),
		CPUs:         intField(args, "cpus"),
		Memory:       stringField(args, "memory"),
		Disk:         stringField(args, "disk"),
		InstanceType: stringField(args, "instanceType"),
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

func servingAssignmentArgs(args map[string]any) ops.ServingAssignmentArgs {
	return ops.ServingAssignmentArgs{
		ContractVersion: stringField(args, "contractVersion"),
		AssignmentID:    stringField(args, "assignmentId"),
		Generation:      intField(args, "generation"),
		IdempotencyKey:  stringField(args, "idempotencyKey"),
		Service:         stringField(args, "service"),
		Mode:            stringField(args, "mode"),
		Runtime:         stringField(args, "runtime"),
		Target:          mapField(args, "target"),
		Artifact:        mapField(args, "artifact"),
		Endpoints:       anySliceField(args, "endpoints"),
		Readiness:       anySliceField(args, "readiness"),
		Exposure:        mapField(args, "exposure"),
		ServiceUnit:     stringField(args, "serviceUnit"),
		DesiredState:    stringField(args, "desiredState"),
	}
}

func mapField(args map[string]any, name string) map[string]any {
	if value, ok := args[name].(map[string]any); ok {
		return value
	}
	return nil
}

func anySliceField(args map[string]any, name string) []any {
	if value, ok := args[name].([]any); ok {
		return value
	}
	return nil
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
