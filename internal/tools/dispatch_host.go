package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/host"
	"github.com/wunderous/host-agents/internal/domain/llm"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

func init() {
	register(toolname.InstallIncusStack, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().InstallIncusStack(host.InstallIncusStackArgs{
			IncusPackage: stringField(args, "incusPackage"), QemuPackage: stringField(args, "qemuPackage"),
			GPUPackages:  stringSliceField(args, "gpuPackages"),
			IncusChannel: stringField(args, "incusChannel"), IncusVersion: stringField(args, "incusVersion"),
			InstallQEMU: boolField(args, "installQemu"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Incus virtualization prerequisites installed"), nil
	})
}

func init() {
	register(toolname.ProbeIncusGPU, EffectRead, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().ProbeIncusGPU(args)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Incus GPU capability report generated"), nil
	})
}

func init() {
	register(toolname.RunInstanceCommand, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().RunInstanceCommand(host.RunInstanceCommandArgs{
			URI:       stringField(args, "uri"),
			Command:   stringField(args, "command"),
			Args:      stringSliceField(args, "args"),
			TimeoutMs: intField(args, "timeoutMs"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Executed command on %s.", out["uri"])), nil
	})
}

func init() {
	register(toolname.ProbeOpenaiCompatibleServer, EffectRead, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Llm().ProbeOpenAICompatibleServer(ctx, llm.ProbeOpenAICompatibleArgs{
			Endpoint:    stringField(args, "endpoint"),
			ModelRef:    stringField(args, "modelRef"),
			IncludeChat: boolField(args, "includeChat"),
			BearerToken: stringField(args, "bearerToken"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.DetectHostPlatform, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().DetectHostPlatform()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Host platform detected: %s on %s.", out.Kind, out.CPU.Architecture)), nil
	})
}

func init() {
	register(toolname.ProbeHTTPEndpoint, EffectRead, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().ProbeHTTPEndpoint(ctx, host.ProbeHTTPEndpointArgs{Endpoint: stringField(args, "endpoint")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ExecCommand, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := execCommandArgs(args, binding)
		out, err := svc.Host().ExecCommand(parsed, onData)
		if err != nil {
			return nil, err
		}
		output, _ := out["output"].(string)
		return structuredResult(out, output), nil
	})
}

func init() {
	register(toolname.EnsureHostFirewallRule, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := host.EnsureHostFirewallRuleArgs{
			BindingID: stringField(args, "bindingId"),
			Port:      intField(args, "port"),
		}
		out, err := svc.Host().EnsureHostFirewallRule(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Firewall rule applied=%v", out.Applied)), nil
	})
}

func init() {
	register(toolname.InspectHostService, EffectRead, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().InspectHostService(host.InspectHostServiceArgs{ServiceName: serviceNameFromBinding(args, binding), Scope: serviceScopeFromBinding(args, binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host service status inspected."), nil
	})
}

func init() {
	register(toolname.ListHostServices, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().ListHostServices(stringField(args, "scope"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.EnsureHostFile, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().EnsureHostFile(host.EnsureHostFileArgs{Path: stringField(args, "path"), Content: stringField(args, "content"), Mode: intField(args, "mode")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file reconciled."), nil
	})
}

func init() {
	register(toolname.RemoveHostFile, EffectDestructive, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().RemoveHostFile(host.RemoveHostFileArgs{Path: stringField(args, "path"), ExpectedSHA256: stringField(args, "expectedSha256"), Confirm: boolField(args, "confirm")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file removed."), nil
	})
}

func init() {
	register(toolname.EnsureHostArtifact, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().EnsureHostArtifact(host.EnsureHostArtifactArgs{URI: stringField(args, "uri"), Destination: stringField(args, "destination"), SHA256: stringField(args, "sha256"), Executable: boolField(args, "executable")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Verified host artifact reconciled."), nil
	})
}

func init() {
	register(toolname.ExtractHostArchive, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().ExtractHostArchive(host.ExtractHostArchiveArgs{ArchivePath: stringField(args, "archivePath"), Destination: stringField(args, "destination"), Format: stringField(args, "format")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Verified host archive extracted."), nil
	})
}

func init() {
	register(toolname.InspectHostFile, EffectRead, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().InspectHostFile(host.InspectHostFileArgs{Path: stringField(args, "path"), ExpectedSHA256: stringField(args, "expectedSha256"), ExpectedContent: stringField(args, "expectedContent")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file inspected."), nil
	})
}

func init() {
	register(toolname.EnsureHostTool, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().EnsureHostTool(host.EnsureHostToolArgs{Tool: stringField(args, "tool")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Generic host tool is available."), nil
	})
}

func init() {
	register(toolname.PrepareHostAgentArtifacts, EffectMutation, resource.ClassHeavy, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Host().PrepareHostAgentArtifacts(host.PrepareHostAgentArtifactsArgs{
			SourceDir: stringField(args, "sourceDir"),
			DestDir:   stringField(args, "destDir"),
			Archs:     stringSliceField(args, "archs"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host-agent build artifacts are ready."), nil
	})
}
