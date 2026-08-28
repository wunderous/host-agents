package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/ops"
)

func init() {
	register(toolname.InstallIncusStack, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
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
	})
}

func init() {
	register(toolname.ProbeIncusGPU, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ProbeIncusGPU(args)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Incus GPU capability report generated"), nil
	})
}

func init() {
	register(toolname.RunInstanceCommand, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
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
	})
}

func init() {
	register(toolname.ProbeOpenaiCompatibleServer, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
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
	})
}

func init() {
	register(toolname.DetectHostPlatform, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.DetectHostPlatform()
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Host platform detected: %s on %s.", out.Kind, out.CPU.Architecture)), nil
	})
}

func init() {
	register(toolname.ProbeHTTPEndpoint, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ProbeHTTPEndpoint(ctx, ops.ProbeHTTPEndpointArgs{Endpoint: stringField(args, "endpoint")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ExecCommand, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := execCommandArgs(args, binding)
		out, err := svc.ExecCommand(parsed, onData)
		if err != nil {
			return nil, err
		}
		output, _ := out["output"].(string)
		return structuredResult(out, output), nil
	})
}

func init() {
	register(toolname.EnsureHostFirewallRule, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := ops.EnsureHostFirewallRuleArgs{
			BindingID: stringField(args, "bindingId"),
			Port:      intField(args, "port"),
		}
		out, err := svc.EnsureHostFirewallRule(parsed)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Firewall rule applied=%v", out.Applied)), nil
	})
}

func init() {
	register(toolname.InspectHostService, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.InspectHostService(ops.InspectHostServiceArgs{ServiceName: serviceNameFromBinding(args, binding), Scope: serviceScopeFromBinding(args, binding)}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host service status inspected."), nil
	})
}

func init() {
	register(toolname.ListHostServices, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ListHostServices(stringField(args, "scope"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.EnsureHostFile, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.EnsureHostFile(ops.EnsureHostFileArgs{Path: stringField(args, "path"), Content: stringField(args, "content"), Mode: intField(args, "mode")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file reconciled."), nil
	})
}

func init() {
	register(toolname.RemoveHostFile, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.RemoveHostFile(ops.RemoveHostFileArgs{Path: stringField(args, "path"), ExpectedSHA256: stringField(args, "expectedSha256"), Confirm: boolField(args, "confirm")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file removed."), nil
	})
}

func init() {
	register(toolname.EnsureHostArtifact, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.EnsureHostArtifact(ops.EnsureHostArtifactArgs{URI: stringField(args, "uri"), Destination: stringField(args, "destination"), SHA256: stringField(args, "sha256"), Executable: boolField(args, "executable")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Verified host artifact reconciled."), nil
	})
}

func init() {
	register(toolname.ExtractHostArchive, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ExtractHostArchive(ops.ExtractHostArchiveArgs{ArchivePath: stringField(args, "archivePath"), Destination: stringField(args, "destination"), Format: stringField(args, "format")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Verified host archive extracted."), nil
	})
}

func init() {
	register(toolname.InspectHostFile, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.InspectHostFile(ops.InspectHostFileArgs{Path: stringField(args, "path"), ExpectedSHA256: stringField(args, "expectedSha256"), ExpectedContent: stringField(args, "expectedContent")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Managed host file inspected."), nil
	})
}

func init() {
	register(toolname.EnsureHostTool, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.EnsureHostTool(ops.EnsureHostToolArgs{Tool: stringField(args, "tool")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Generic host tool is available."), nil
	})
}

func init() {
	register(toolname.PrepareHostAgentArtifacts, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.PrepareHostAgentArtifacts(ops.PrepareHostAgentArtifactsArgs{
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
