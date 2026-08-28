package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/resource"
)

// DispatchTool executes a host MCP tool via HostOperationsService and returns
// an MCP CallToolResult. The argument map is the unchanged client/model input;
// resolved provider coordinates arrive only through the typed execution
// binding, never as synthetic argument fields.
func DispatchTool(ctx context.Context, svc *ops.HostOperationsService, name string, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if err := ctx.Err(); err != nil {
		return ErrorResult(err), nil
	}

	result, err := runTool(ctx, svc, name, args, binding, onData)
	if err != nil {
		return ErrorResult(err), nil
	}
	return result, nil
}

func listClustersFastArg(args map[string]any) bool {
	// Cluster inventory is a control-plane list operation. Keep the default
	// bounded and provider-independent; callers that need live node and
	// version probes can request the slower detail path explicitly.
	if requested, ok := args["fast"].(bool); ok {
		return requested
	}
	return true
}

func runTool(ctx context.Context, svc *ops.HostOperationsService, name string, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
	switch name {
	case "get_host_info":
		out := svc.DescribeHost()
		return structuredResult(out, ""), nil

	case "ensure_sqlite_database":
		out, err := svc.EnsureSQLiteDatabase(ctx, ops.SQLiteDatabaseArgs{
			ConsumerID:   stringField(args, "consumerId"),
			DatabaseName: stringField(args, "databaseName"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQLite database provisioned."), nil

	case "get_sqlite_database_status":
		out, err := svc.GetSQLiteDatabaseStatus(ctx, ops.SQLiteDatabaseArgs{
			ConsumerID:   stringField(args, "consumerId"),
			DatabaseName: stringField(args, "databaseName"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQLite database status returned."), nil

	case "remove_sqlite_database":
		out, err := svc.RemoveSQLiteDatabase(ctx, ops.SQLiteDatabaseArgs{
			ConsumerID:   stringField(args, "consumerId"),
			DatabaseName: stringField(args, "databaseName"),
		}, boolField(args, "confirm"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQLite database removed."), nil

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
		out, err := svc.ListClusters(listClustersFastArg(args))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "list_kubernetes_clusters":
		out, err := svc.ListKubernetesClusters(stringField(args, "source"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "get_cluster_details":
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

	case "get_cluster_runtime_details":
		vmName := vmNameFromBinding(binding)
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
			"uri":           out.URI,
			"containerName": out.ContainerName,
			"vmName":        out.ContainerName,
			"image":         out.Image,
			"status":        out.Status,
			"instanceType":  out.InstanceType,
		}
		return structuredResult(payload, fmt.Sprintf("Provisioned Incus container '%s'.", out.ContainerName)), nil

	case "run_instance_command":
		out, err := svc.RunInstanceCommand(ops.RunInstanceCommandArgs{
			URI:       stringField(args, "uri"),
			Command:   stringField(args, "command"),
			Args:      stringSliceField(args, "args"),
			TimeoutMs: intField(args, "timeoutMs"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Executed command on %s.", out["uri"])), nil

	case "probe_gpu_container":
		out, err := svc.ProbeGPUContainer(onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "System container GPU capability report generated"), nil

	case "check_local_llm_prerequisites":
		out, err := svc.CheckOllamaPrerequisites()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "probe_openai_compatible_server":
		out, err := svc.ProbeOpenAICompatibleServer(ctx, ops.ProbeOpenAICompatibleArgs{
			Endpoint:    stringField(args, "endpoint"),
			ModelRef:    stringField(args, "modelRef"),
			IncludeChat: boolField(args, "includeChat"),
			BearerToken: stringField(args, "bearerToken"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "detect_host_platform":
		out, err := svc.DetectHostPlatform()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Host platform detected: %s on %s.", out.Kind, out.CPU.Architecture)), nil

	case "probe_http_endpoint":
		out, err := svc.ProbeHTTPEndpoint(ctx, ops.ProbeHTTPEndpointArgs{Endpoint: stringField(args, "endpoint")})
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
		if _, err := localLLMModelRole(args); err != nil {
			return nil, err
		}
		var out any
		var err error
		if localLLMRuntime(args) == "llama-cpp" {
			out, err = svc.ProbeLlamaServer(ctx, ops.ProbeLlamaServerArgs{IncludeChat: boolField(args, "includeChat"), ModelRef: localLLMModelRef(args)})
		} else if localLLMRuntime(args) == "ollama" {
			out, err = svc.ProbeOllama(ctx, ops.ProbeOllamaArgs{IncludeChat: boolField(args, "includeChat"), ModelRef: localLLMModelRef(args)})
		} else {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; expected ollama or llama-cpp", localLLMRuntime(args))
		}
		if err != nil {
			return nil, err
		}
		if probe, ok := out.(*ops.LocalLLMProbeResult); ok {
			svc.AttachLocalLLMModelURIs(probe)
		}
		return structuredResult(out, ""), nil

	case "install_local_llm_model":
		role, err := localLLMModelRole(args)
		if err != nil {
			return nil, err
		}
		if localLLMRuntime(args) == "ollama" {
			modelRef, err := resolveLocalLLMModelArg(args)
			if err != nil {
				return nil, err
			}
			setDefault := role != "embedding"
			if _, present := args["setDefault"]; present {
				setDefault = boolField(args, "setDefault")
			}
			out, err := svc.InstallOllamaModel(ctx, ops.InstallOllamaModelArgs{ModelRef: modelRef, Port: optionalIntField(args, "port"), Role: role, SetDefault: setDefault})
			if err != nil {
				return nil, err
			}
			return structuredResult(out, "Ollama model is ready"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; expected ollama or llama-cpp", localLLMRuntime(args))
		}
		modelRef, err := resolveLocalLLMModelArg(args)
		if err != nil {
			return nil, err
		}
		out, err := svc.InstallLlamaServerModel(ctx, ops.InstallLlamaServerModelArgs{
			ModelRef:                modelRef,
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
		if localLLMRuntime(args) == "ollama" {
			modelRef := localLLMModelRef(args)
			contextSize := intField(args, "contextSize")
			if contextSize == 0 {
				contextSize = intField(args, "numCtx")
			}
			if contextSize > 0 {
				if modelRef == "" {
					return nil, fmt.Errorf("modelRef is required when setting contextSize")
				}
				out, err := svc.ConfigureOllamaModelContext(ctx, ops.ConfigureOllamaModelContextArgs{ModelRef: modelRef, ContextSize: contextSize})
				if err != nil {
					return nil, err
				}
				return structuredResult(out, "Ollama model context is persisted in the shared host runtime"), nil
			}
			out, err := svc.GetOllamaModelContext(ctx, modelRef)
			if err != nil {
				return nil, err
			}
			return structuredResult(out, "Ollama model context returned from the shared host runtime"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return nil, fmt.Errorf("configure_local_llm_model is retired; llama-server uses the pinned artifact manifest")

	case "start_local_llm_runtime":
		if localLLMRuntime(args) == "ollama" {
			out, err := svc.StartOllamaRuntime(ctx)
			if err != nil {
				return nil, err
			}
			return structuredResult(out, "shared Ollama runtime is ready"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		out, err := svc.StartLlamaServerRuntime(ctx)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "llama-server runtime is ready"), nil

	case "configure_local_llm_runtime":
		if localLLMRuntime(args) == "ollama" {
			return nil, fmt.Errorf("Ollama runtime configuration is host-wide and pinned to one process, two resident models, and one request at a time")
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return nil, fmt.Errorf("configure_local_llm_runtime is retired; configure llama-server through its pinned service manifest")

	case "stop_local_llm_runtime":
		if localLLMRuntime(args) == "ollama" {
			if err := svc.StopOllamaRuntime(ctx); err != nil {
				return nil, err
			}
			return structuredResult(map[string]any{"stopped": false, "shared": true, "reason": "shared Ollama runtime remains available to other Platform instances"}, "shared Ollama runtime left running"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		if err := svc.StopLlamaServerRuntime(ctx); err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"stopped": true}, "llama-server runtime stopped"), nil

	case "reconcile_postgresql_service":
		out, err := svc.ReconcilePostgreSQLService(ctx, postgresqlServiceArgs(args, binding), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service is ready"), nil
	case "get_postgresql_service_status":
		out, err := svc.GetPostgreSQLServiceStatus(ctx, postgresqlServiceArgs(args, binding))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service status returned"), nil
	case "remove_postgresql_service":
		out, err := svc.RemovePostgreSQLService(ctx, postgresqlServiceArgs(args, binding), boolField(args, "confirm"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service was removed"), nil
	case "release_postgresql_service_relay":
		out, err := svc.ReleasePostgreSQLServiceRelay(stringField(args, "sessionId"), stringField(args, "relayToken"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL service relay was released"), nil

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
		if localLLMRuntime(args) == "ollama" {
			return structuredResult(map[string]any{"removed": false, "purged": false, "shared": true, "reason": "Ollama model artifacts are shared host state; use the host Ollama lifecycle for explicit garbage collection"}, "shared Ollama model retained"), nil
		}
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
			VMName:         vmNameFromBinding(binding),
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
		out, err := svc.RemoveLocalLLMK3sProxy(vmNameFromBinding(binding), stringField(args, "namespace"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM K3s proxy removed"), nil

	case "get_vm_info":
		vmName := vmNameFromBinding(binding)
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
		out, err := svc.DiscoverServiceIngress(ops.DiscoverServiceIngressArgs{VMName: vmNameFromBinding(binding), Endpoints: endpoints, IngressNamespace: stringField(args, "ingressNamespace"), IngressService: stringField(args, "ingressService")})
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
		timeoutMs := intField(args, "timeoutMs")
		if timeoutMs < 0 {
			return nil, fmt.Errorf("timeoutMs must be non-negative")
		}
		if timeoutMs > 2*60*60*1000 {
			return nil, fmt.Errorf("timeoutMs exceeds the two-hour maximum")
		}
		res, err := svc.RunAgentShellWithTimeout(command, time.Duration(timeoutMs)*time.Millisecond, onData)
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
		parsed := execCommandArgs(args, binding)
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
		out, err := svc.StartVM(ops.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Started VM '%s'.", out["vmName"])), nil

	case "stop_vm":
		out, err := svc.StopVM(ops.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Stopped VM '%s'.", out["vmName"])), nil

	case "restart_vm":
		out, err := svc.RestartVM(ops.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Restarted VM '%s'.", out["vmName"])), nil

	case "update_vm_resources":
		out, err := svc.UpdateVMResources(ops.UpdateVMResourcesArgs{
			VMName: vmNameFromBinding(binding),
			CPUs:   intField(args, "cpus"),
			Memory: stringField(args, "memory"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Updated resources for '%s' (cpus=%s, memory=%s).", out["vmName"], out["cpus"], out["memory"])), nil

	case "delete_vm":
		out, err := svc.DeleteVM(ops.VMScopedArgs{VMName: vmNameFromBinding(binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Deleted VM '%s'.", out["vmName"])), nil

	case "install_postgresql":
		out, err := svc.InstallPostgreSQL(ops.InstallPostgreSQLArgs{
			VMName:    vmNameFromBinding(binding),
			Namespace: stringField(args, "namespace"),
			Database:  stringField(args, "database"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL is ready."), nil

	case "get_postgresql_status":
		out, err := svc.GetPostgreSQLStatus(vmNameFromBinding(binding), stringField(args, "namespace"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "delete_postgresql":
		out, err := svc.DeletePostgreSQL(vmNameFromBinding(binding), stringField(args, "namespace"), onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "PostgreSQL deleted."), nil

	case "run_sql":
		out, err := svc.RunSQL(vmNameFromBinding(binding), stringField(args, "namespace"), stringField(args, "database"), stringField(args, "sql"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "SQL completed."), nil

	case "install_helm_chart":
		parsed := installHelmChartArgs(args, binding)
		out, err := svc.InstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deployment initiated.", parsed.ReleaseName)), nil

	case "uninstall_helm_chart":
		parsed := uninstallHelmChartArgs(args, binding)
		out, err := svc.UninstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deleted.", parsed.ReleaseName)), nil

	case "apply_manifest":
		out, err := svc.ApplyManifest(ops.ApplyManifestArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Manifest: stringField(args, "manifest")}, onData)
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
		out, err := svc.PutK8sSecret(ops.PutK8sSecretArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Data: data}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes Secret configured."), nil

	case "get_k8s_resource":
		out, err := svc.GetK8sResource(ops.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil

	case "delete_k8s_resource":
		out, err := svc.DeleteK8sResource(ops.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, "Kubernetes resource deleted."), nil

	case "get_k8s_resource_status":
		out, err := svc.GetK8sResourceStatus(ops.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil

	case "list_k8s_events":
		limit, _ := args["limit"].(float64)
		out, err := svc.ListK8sEvents(ops.K8sEventsArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Limit: int(limit)})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil

	case "install_oci_registry":
		out, err := svc.InstallOCIRegistry(ops.InstallOCIRegistryArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Image: stringField(args, "image"), StorageSize: stringField(args, "storageSize"), StorageClass: stringField(args, "storageClass"), NodePort: intField(args, "nodePort")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI registry deployment initiated."), nil

	case "get_oci_registry_status":
		out, err := svc.GetOCIRegistryStatus(ops.InstallOCIRegistryArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Name: stringField(args, "name")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "delete_oci_registry":
		out, err := svc.DeleteOCIRegistry(ops.InstallOCIRegistryArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI registry deleted."), nil

	case "configure_service_domain":
		out, err := svc.ConfigureServiceDomain(ops.ConfigureServiceDomainArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName"), Hostname: stringField(args, "hostname"), ServiceName: stringField(args, "serviceName"), ServicePort: intField(args, "servicePort"), IngressClass: stringField(args, "ingressClass")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping configured."), nil

	case "remove_service_domain":
		out, err := svc.RemoveServiceDomain(ops.ConfigureServiceDomainArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping removed."), nil

	case "restart_host_service":
		out, err := svc.RestartHostService(ops.RestartHostServiceArgs{ServiceName: stringField(args, "serviceName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Restarted service '%s'.", out["serviceName"])), nil

	case "set_host_service_state":
		out, err := svc.SetHostServiceState(ops.SetHostServiceStateArgs{ServiceName: serviceNameFromBinding(args, binding), State: stringField(args, "state"), Scope: serviceScopeFromBinding(args, binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Applied service state '%s' to '%s'.", out["state"], out["serviceName"])), nil

	case "inspect_host_service":
		out, err := svc.InspectHostService(ops.InspectHostServiceArgs{ServiceName: serviceNameFromBinding(args, binding), Scope: serviceScopeFromBinding(args, binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host service status inspected."), nil

	case "list_host_services":
		out, err := svc.ListHostServices(stringField(args, "scope"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil

	case "ensure_host_service_supervisor":
		out, err := svc.EnsureHostServiceSupervisor(ops.EnsureHostServiceSupervisorArgs{Scope: stringField(args, "scope")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host service supervisor is ready."), nil

	case "ensure_host_file":
		out, err := svc.EnsureHostFile(ops.EnsureHostFileArgs{Path: stringField(args, "path"), Content: stringField(args, "content"), Mode: intField(args, "mode")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file reconciled."), nil

	case "remove_host_file":
		out, err := svc.RemoveHostFile(ops.RemoveHostFileArgs{Path: stringField(args, "path"), ExpectedSHA256: stringField(args, "expectedSha256"), Confirm: boolField(args, "confirm")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file removed."), nil

	case "ensure_host_artifact":
		out, err := svc.EnsureHostArtifact(ops.EnsureHostArtifactArgs{URI: stringField(args, "uri"), Destination: stringField(args, "destination"), SHA256: stringField(args, "sha256"), Executable: boolField(args, "executable")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Verified host artifact reconciled."), nil

	case "extract_host_archive":
		out, err := svc.ExtractHostArchive(ops.ExtractHostArchiveArgs{ArchivePath: stringField(args, "archivePath"), Destination: stringField(args, "destination"), Format: stringField(args, "format")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Verified host archive extracted."), nil

	case "inspect_host_file":
		out, err := svc.InspectHostFile(ops.InspectHostFileArgs{Path: stringField(args, "path"), ExpectedSHA256: stringField(args, "expectedSha256"), ExpectedContent: stringField(args, "expectedContent")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file inspected."), nil

	case "configure_agent_connection":
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
		vmName := vmNameFromBinding(binding)
		namespaces, err := svc.ListNamespaces(vmName)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"namespaces": namespaces}
		return structuredResult(out, ""), nil

	case "list_storage_classes":
		vmName := vmNameFromBinding(binding)
		storageClasses, err := svc.ListStorageClasses(vmName)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"storageClasses": storageClasses}
		return structuredResult(out, ""), nil

	case "list_ingress_classes":
		vmName := vmNameFromBinding(binding)
		ingressClasses, err := svc.ListIngressClasses(vmName)
		if err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"classes": ingressClasses, "ingressClasses": ingressClasses}, ""), nil

	case "list_pods":
		vmName := vmNameFromBinding(binding)
		namespace := stringField(args, "namespace")
		pods, err := svc.ListPods(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(withBindingURI(map[string]any{"pods": pods}, binding, "cluster"), ""), nil

	case "list_services":
		vmName := vmNameFromBinding(binding)
		namespace := stringField(args, "namespace")
		services, err := svc.ListServices(vmName, namespace)
		if err != nil {
			return nil, err
		}
		return structuredResult(withBindingURI(map[string]any{"services": services}, binding, "cluster"), ""), nil

	case "list_deployments":
		vmName := vmNameFromBinding(binding)
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

// CapabilityError is the typed owner boundary for a capability invocation
// failure. Owner records which layer failed — "capability" for capability-
// owned validation/execution, "admission" for tenant/resource binding, or
// "lifecycle" for generation state — so clients render capability-owned
// invalid-input errors distinctly from envelope or transport problems.
type CapabilityError struct {
	Owner   string
	Code    string
	Message string
	Err     error
}

func (e *CapabilityError) Error() string {
	message := e.Message
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if message == "" {
		message = "capability error"
	}
	return message
}

func (e *CapabilityError) Unwrap() error { return e.Err }

// NewCapabilityError wraps err in the typed owner boundary.
func NewCapabilityError(owner, code string, err error) *CapabilityError {
	return &CapabilityError{Owner: owner, Code: code, Err: err}
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
				"owner":        "admission",
			},
			IsError: true,
		}
	}
	if capabilityErr, ok := err.(*CapabilityError); ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
			StructuredContent: map[string]any{
				"code":    capabilityErr.Code,
				"owner":   capabilityErr.Owner,
				"message": capabilityErr.Error(),
			},
			IsError: true,
		}
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
		IsError: true,
	}
}

func vmNameFromBinding(binding ExecutionBinding) string {
	return binding.ProviderInstanceName()
}

func resourceURIFromBinding(binding ExecutionBinding) string {
	for _, resource := range binding.Resources {
		if resource.ResourceType == "cluster" && strings.TrimSpace(resource.URI) != "" {
			return resource.URI
		}
	}
	return ""
}

func serviceNameFromBinding(args map[string]any, binding ExecutionBinding) string {
	if name := stringField(args, "serviceName"); name != "" {
		return name
	}
	return binding.StringCoordinate("serviceName")
}

func serviceScopeFromBinding(args map[string]any, binding ExecutionBinding) string {
	if scope := stringField(args, "scope"); scope != "" {
		return scope
	}
	return binding.StringCoordinate("scope")
}

func withBindingURI(out map[string]any, binding ExecutionBinding, allowedTypes ...string) map[string]any {
	if out == nil {
		out = map[string]any{}
	}
	if _, exists := out["uri"]; exists {
		return out
	}
	for _, resource := range binding.Resources {
		if len(allowedTypes) > 0 && !containsString(allowedTypes, resource.ResourceType) {
			continue
		}
		if strings.TrimSpace(resource.URI) != "" {
			out["uri"] = resource.URI
			break
		}
	}
	return out
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
		SessionID:       stringField(raw, "sessionId"),
		ListenHost:      stringField(raw, "listenHost"),
		ListenPort:      intField(raw, "listenPort"),
		TargetHost:      stringField(raw, "targetHost"),
		TargetPort:      intField(raw, "targetPort"),
		TTLSeconds:      intField(raw, "ttlSeconds"),
		RelayToken:      stringField(raw, "relayToken"),
		Persistent:      boolField(raw, "persistent"),
		ReplaceExisting: boolField(raw, "replaceExisting"),
	}
}

func postgresqlServiceArgs(args map[string]any, binding ExecutionBinding) ops.PostgreSQLServiceArgs {
	return ops.PostgreSQLServiceArgs{
		VMName: vmNameFromBinding(binding), ClusterName: stringField(args, "clusterName"), Namespace: stringField(args, "namespace"),
		Instances: intField(args, "instances"), StorageClass: stringField(args, "storageClass"), StorageSize: stringField(args, "storageSize"),
		RetentionPolicy: stringField(args, "retentionPolicy"), RestartConsumers: optionalBoolField(args, "restartConsumers"),
		Databases: uniqueStringSlice(stringSliceField(args, "databases")), ConsumerDatabaseKeys: stringMapField(args, "consumerDatabaseKeys"),
		ConsumerSecretName: stringField(args, "consumerSecretName"), ConsumerSecretLabel: stringField(args, "consumerSecretLabel"),
		ServiceOwner: stringField(args, "serviceOwner"), ServicePartOf: stringField(args, "servicePartOf"), RelayDeviceName: stringField(args, "relayDeviceName"),
		Relay: postgresqlServiceRelayArgs(args),
	}
}

func resolveLocalLLMModelArg(args map[string]any) (string, error) {
	if modelRef, err := explicitLocalLLMModelRef(args); err != nil {
		return "", err
	} else if modelRef != "" {
		return modelRef, nil
	}
	switch stringField(args, "modelPreset") {
	case "":
		if localLLMRuntime(args) == "llama-cpp" {
			return "qwen3.5-0.8b/base-llama", nil
		}
		return ops.DefaultOllamaModel, nil
	case "lfm2-2.6b":
		if localLLMRuntime(args) == "llama-cpp" {
			return "", fmt.Errorf("model preset %q requires the ollama runtime", stringField(args, "modelPreset"))
		}
		return ops.DefaultOllamaModel, nil
	case "lfm2.5-thinking":
		if localLLMRuntime(args) == "llama-cpp" {
			return "", fmt.Errorf("model preset %q requires the ollama runtime", stringField(args, "modelPreset"))
		}
		return "lfm2.5-thinking:1.2b", nil
	case "qwen3.5":
		if localLLMRuntime(args) == "llama-cpp" {
			return "qwen3.5-0.8b/base-llama", nil
		}
		return "qwen3.5:2b", nil
	case "qwen3.5-0.8b":
		if localLLMRuntime(args) == "llama-cpp" {
			return "qwen3.5-0.8b/base-llama", nil
		}
		return "qwen3.5:0.8b", nil
	default:
		return "", fmt.Errorf("unsupported local LLM model preset %q", stringField(args, "modelPreset"))
	}
}

func explicitLocalLLMModelRef(args map[string]any) (string, error) {
	modelRef := stringField(args, "modelRef")
	model := stringField(args, "model")
	if modelRef != "" && model != "" && modelRef != model {
		return "", fmt.Errorf("model and modelRef must identify the same model when both are supplied")
	}
	if model != "" {
		return model, nil
	}
	return modelRef, nil
}

func localLLMModelRef(args map[string]any) string {
	model, err := explicitLocalLLMModelRef(args)
	if err != nil {
		return ""
	}
	return model
}

func localLLMModelRole(args map[string]any) (string, error) {
	role := stringField(args, "role")
	if role == "" {
		return "language", nil
	}
	if role != "language" && role != "embedding" {
		return "", fmt.Errorf("unsupported model role %q; expected language or embedding", role)
	}
	return role, nil
}

// localLLMRuntime defaults new payloads to the shared Ollama runtime. The
// llama-cpp path remains available only when explicitly selected.
func localLLMRuntime(args map[string]any) string {
	if runtime := stringField(args, "runtime"); runtime != "" {
		return runtime
	}
	return "ollama"
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
		RestartPolicy:   stringField(args, "restartPolicy"),
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

func execCommandArgs(args map[string]any, binding ExecutionBinding) ops.ExecCommandArgs {
	var argv []string
	if raw, ok := args["args"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok {
				argv = append(argv, s)
			}
		}
	}
	return ops.ExecCommandArgs{
		VMName:    vmNameFromBinding(binding),
		Command:   stringField(args, "command"),
		Args:      argv,
		TimeoutMs: intField(args, "timeout"),
	}
}

func installHelmChartArgs(args map[string]any, binding ExecutionBinding) ops.InstallHelmChartArgs {
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
		VMName:      vmNameFromBinding(binding),
		ReleaseName: releaseName,
		ChartSource: chartSource,
		Namespace:   namespace,
		Repo:        stringField(args, "repo"),
		Values:      ops.HelmValuesYAML(args["values"]),
	}
}

func uninstallHelmChartArgs(args map[string]any, binding ExecutionBinding) ops.UninstallHelmChartArgs {
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
		VMName:      vmNameFromBinding(binding),
		ReleaseName: releaseName,
		Namespace:   namespace,
	}
}
