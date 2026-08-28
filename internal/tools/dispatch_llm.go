package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/llm"
	"github.com/wunderous/host-agents/internal/hostagent"
)

func init() {
	register(toolname.CheckLocalLLMPrerequisites, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Llm().CheckOllamaPrerequisites()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.EnsureLocalLLMServerBinary, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		out, err := svc.Llm().EnsureLlamaServerBinary(ctx, llm.BuildLlamaServerBinaryArgs{
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
	})
}

func init() {
	h := func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		if _, err := localLLMModelRole(args); err != nil {
			return nil, err
		}
		var out any
		var err error
		if localLLMRuntime(args) == "llama-cpp" {
			out, err = svc.Llm().ProbeLlamaServer(ctx, llm.ProbeLlamaServerArgs{IncludeChat: boolField(args, "includeChat"), ModelRef: localLLMModelRef(args)})
		} else if localLLMRuntime(args) == "ollama" {
			out, err = svc.Llm().ProbeOllama(ctx, llm.ProbeOllamaArgs{IncludeChat: boolField(args, "includeChat"), ModelRef: localLLMModelRef(args)})
		} else {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; expected ollama or llama-cpp", localLLMRuntime(args))
		}
		if err != nil {
			return nil, err
		}
		if probe, ok := out.(*llm.LocalLLMProbeResult); ok {
			svc.AttachLocalLLMModelURIs(probe)
		}
		return structuredResult(out, ""), nil
	}
	for _, n := range []string{toolname.ListLocalLLMModels, toolname.ProbeLocalLLM} {
		register(n, h)
	}
}

func init() {
	register(toolname.InstallLocalLLMModel, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
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
			out, err := svc.Llm().InstallOllamaModel(ctx, llm.InstallOllamaModelArgs{ModelRef: modelRef, Port: optionalIntField(args, "port"), Role: role, SetDefault: setDefault})
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
		out, err := svc.Llm().InstallLlamaServerModel(ctx, llm.InstallLlamaServerModelArgs{
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
	})
}

func init() {
	register(toolname.ConfigureLocalLLMModel, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
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
				out, err := svc.Llm().ConfigureOllamaModelContext(ctx, llm.ConfigureOllamaModelContextArgs{ModelRef: modelRef, ContextSize: contextSize})
				if err != nil {
					return nil, err
				}
				return structuredResult(out, "Ollama model context is persisted in the shared host runtime"), nil
			}
			out, err := svc.Llm().GetOllamaModelContext(ctx, modelRef)
			if err != nil {
				return nil, err
			}
			return structuredResult(out, "Ollama model context returned from the shared host runtime"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		return nil, fmt.Errorf("configure_local_llm_model is retired; llama-server uses the pinned artifact manifest")
	})
}

func init() {
	register(toolname.StartLocalLLMRuntime, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		if localLLMRuntime(args) == "ollama" {
			out, err := svc.Llm().StartOllamaRuntime(ctx)
			if err != nil {
				return nil, err
			}
			return structuredResult(out, "shared Ollama runtime is ready"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		out, err := svc.Llm().StartLlamaServerRuntime(ctx)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "llama-server runtime is ready"), nil
	})
}

func init() {
	register(toolname.StopLocalLLMRuntime, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		if localLLMRuntime(args) == "ollama" {
			if err := svc.Llm().StopOllamaRuntime(ctx); err != nil {
				return nil, err
			}
			return structuredResult(map[string]any{"stopped": false, "shared": true, "reason": "shared Ollama runtime remains available to other Platform instances"}, "shared Ollama runtime left running"), nil
		}
		if localLLMRuntime(args) != "llama-cpp" {
			return nil, fmt.Errorf("unsupported local LLM runtime %q; only llama-cpp is supported", localLLMRuntime(args))
		}
		if err := svc.Llm().StopLlamaServerRuntime(ctx); err != nil {
			return nil, err
		}
		return structuredResult(map[string]any{"stopped": true}, "llama-server runtime stopped"), nil
	})
}

func init() {
	register(toolname.EnsureLocalLLMRelay, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Llm().EnsureLocalLLMRelay(ctx, llm.LocalLLMRelayArgs{SessionID: stringField(args, "sessionId"), ListenHost: stringField(args, "listenHost"), ListenPort: intField(args, "listenPort"), TargetHost: stringField(args, "targetHost"), TargetPort: intField(args, "targetPort"), IncomingToken: stringField(args, "incomingToken"), UpstreamToken: stringField(args, "upstreamToken"), AllowedSourceCIDRs: stringSliceField(args, "allowedSourceCIDRs"), RelayToken: stringField(args, "relayToken"), AllowedSourceIP: stringField(args, "allowedSourceIP")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM relay is ready"), nil
	})
}

func init() {
	register(toolname.RemoveLocalLLMRelay, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Llm().RemoveLocalLLMRelay(stringField(args, "sessionId"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM relay removed"), nil
	})
}

func init() {
	register(toolname.EnsureLocalLLMK3sProxy, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Llm().EnsureLocalLLMK3sProxy(llm.LocalLLMK3sProxyArgs{
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
	})
}

func init() {
	register(toolname.RemoveLocalLLMK3sProxy, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Llm().RemoveLocalLLMK3sProxy(vmNameFromBinding(binding), stringField(args, "namespace"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Local LLM K3s proxy removed"), nil
	})
}
