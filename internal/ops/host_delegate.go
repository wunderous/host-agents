package ops

import (
	"context"
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/domain/host"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/pkg/hostplatform"
)

// The host domain owns these types and operations. HostOperationsService keeps
// delegating methods so the dispatch registry and the domains that have not
// moved yet are unaffected; this file disappears with internal/ops itself.
type (
	ConfigureAgentConnectionArgs    = host.ConfigureAgentConnectionArgs
	ExtractHostArchiveArgs          = host.ExtractHostArchiveArgs
	EnsureHostArtifactArgs          = host.EnsureHostArtifactArgs
	ProbeHTTPEndpointArgs           = host.ProbeHTTPEndpointArgs
	HTTPObservation                 = host.HTTPObservation
	ExecCommandArgs                 = host.ExecCommandArgs
	RunInstanceCommandArgs          = host.RunInstanceCommandArgs
	HostInfoResult                  = host.HostInfoResult
	BridgeDiagnosticResult          = host.BridgeDiagnosticResult
	RestartHostServiceArgs          = host.RestartHostServiceArgs
	SetHostServiceStateArgs         = host.SetHostServiceStateArgs
	EnsureHostServiceSupervisorArgs = host.EnsureHostServiceSupervisorArgs
	EnsureHostFileArgs              = host.EnsureHostFileArgs
	InspectHostFileArgs             = host.InspectHostFileArgs
	RemoveHostFileArgs              = host.RemoveHostFileArgs
	EnsureHostFirewallRuleArgs      = host.EnsureHostFirewallRuleArgs
	EnsureHostFirewallRuleResult    = host.EnsureHostFirewallRuleResult
	PrepareHostAgentArtifactsArgs   = host.PrepareHostAgentArtifactsArgs
	InspectHostServiceArgs          = host.InspectHostServiceArgs
	EnsureHostToolArgs              = host.EnsureHostToolArgs
	InstallIncusStackArgs           = host.InstallIncusStackArgs
)

// hostSvc is stateless, so it is rebuilt per call rather than held. Unlike
// kubernetes, llm, and oci, nothing here survives between operations.
func (s *HostOperationsService) host() *host.Service {
	return host.New(&s.shared, host.Deps{
		ResolveResource: func(uri, wantType string) (hostruntime.Coordinates, error) {
			return s.ResolveResource(uri, wantType)
		},
		VMInventoryCapacity: func() (vminfo.VMInventoryCapacity, error) {
			return s.VMInventoryCapacity()
		},
		RunVMExec: func(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
			return s.incus().RunVMExec(vmName, guestArgv, onData, timeout)
		},
		SupportedTools: s.toolsFn,
	})
}

func (s *HostOperationsService) ExtractHostArchive(args ExtractHostArchiveArgs, onData func(string)) (map[string]any, error) {
	return s.host().ExtractHostArchive(args, onData)
}

func (s *HostOperationsService) EnsureHostFirewallRule(args EnsureHostFirewallRuleArgs) (*EnsureHostFirewallRuleResult, error) {
	return s.host().EnsureHostFirewallRule(args)
}

func (s *HostOperationsService) PrepareHostAgentArtifacts(args PrepareHostAgentArtifactsArgs, onData func(string)) (map[string]any, error) {
	return s.host().PrepareHostAgentArtifacts(args, onData)
}

func (s *HostOperationsService) EnsureHostArtifact(args EnsureHostArtifactArgs, onData func(string)) (map[string]any, error) {
	return s.host().EnsureHostArtifact(args, onData)
}

func (s *HostOperationsService) ProbeHTTPEndpoint(ctx context.Context, args ProbeHTTPEndpointArgs) (*HTTPObservation, error) {
	return s.host().ProbeHTTPEndpoint(ctx, args)
}

func (s *HostOperationsService) RunInstanceCommand(args RunInstanceCommandArgs, onData func(string)) (map[string]any, error) {
	return s.host().RunInstanceCommand(args, onData)
}

func (s *HostOperationsService) ExecCommand(args ExecCommandArgs, onData func(string)) (map[string]any, error) {
	return s.host().ExecCommand(args, onData)
}

func (s *HostOperationsService) DescribeHost() HostInfoResult {
	return s.host().DescribeHost()
}

func (s *HostOperationsService) RunAgentShell(command string, onData func(string)) (hostexec.Result, error) {
	return s.host().RunAgentShell(command, onData)
}

func (s *HostOperationsService) RunAgentShellWithTimeout(command string, timeout time.Duration, onData func(string)) (hostexec.Result, error) {
	return s.host().RunAgentShellWithTimeout(command, timeout, onData)
}

func (s *HostOperationsService) RestartHostService(args RestartHostServiceArgs, onData func(string)) (map[string]string, error) {
	return s.host().RestartHostService(args, onData)
}

func (s *HostOperationsService) SetHostServiceState(args SetHostServiceStateArgs, onData func(string)) (map[string]any, error) {
	return s.host().SetHostServiceState(args, onData)
}

func (s *HostOperationsService) EnsureHostServiceSupervisor(args EnsureHostServiceSupervisorArgs, onData func(string)) (map[string]any, error) {
	return s.host().EnsureHostServiceSupervisor(args, onData)
}

func (s *HostOperationsService) EnsureDocker(onData func(string)) (map[string]any, error) {
	return s.host().EnsureDocker(onData)
}

func (s *HostOperationsService) EnsureK3d(onData func(string)) (map[string]any, error) {
	return s.host().EnsureK3d(onData)
}

func (s *HostOperationsService) DiagnoseBridge(ctx context.Context) (BridgeDiagnosticResult, error) {
	return s.host().DiagnoseBridge(ctx)
}

func (s *HostOperationsService) RecoverBridge(ctx context.Context, onData func(string)) (BridgeDiagnosticResult, error) {
	return s.host().RecoverBridge(ctx, onData)
}

func (s *HostOperationsService) InspectHostService(args InspectHostServiceArgs, onData func(string)) (map[string]any, error) {
	return s.host().InspectHostService(args, onData)
}

func (s *HostOperationsService) ListHostServices(scope string) (map[string]any, error) {
	return s.host().ListHostServices(scope)
}

func (s *HostOperationsService) EnsureHostFile(args EnsureHostFileArgs) (map[string]any, error) {
	return s.host().EnsureHostFile(args)
}

func (s *HostOperationsService) InspectHostFile(args InspectHostFileArgs) (map[string]any, error) {
	return s.host().InspectHostFile(args)
}

func (s *HostOperationsService) RemoveHostFile(args RemoveHostFileArgs) (map[string]any, error) {
	return s.host().RemoveHostFile(args)
}

func (s *HostOperationsService) DetectHostPlatform() (*hostplatform.Platform, error) {
	return s.host().DetectHostPlatform()
}

func (s *HostOperationsService) InstallIncusStack(args InstallIncusStackArgs, onData func(string)) (map[string]any, error) {
	return s.host().InstallIncusStack(args, onData)
}

func (s *HostOperationsService) ProbeIncusGPU(args map[string]any) (map[string]any, error) {
	return s.host().ProbeIncusGPU(args)
}

func (s *HostOperationsService) EnsureHostTool(args EnsureHostToolArgs, onData func(string)) (map[string]any, error) {
	return s.host().EnsureHostTool(args, onData)
}

func (s *HostOperationsService) ConfigureAgentConnection(args ConfigureAgentConnectionArgs, onData func(string)) (map[string]any, error) {
	return s.host().ConfigureAgentConnection(args, onData)
}
