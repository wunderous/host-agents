package hostagent

import (
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/domain/host"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// These aliases name the host domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
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
func (s *Service) Host() *host.Service {
	return host.New(&s.shared, host.Deps{
		ResolveResource: func(uri, wantType string) (hostruntime.Coordinates, error) {
			return s.ResolveResource(uri, wantType)
		},
		VMInventoryCapacity: func() (vminfo.VMInventoryCapacity, error) {
			return s.Incus().VMInventoryCapacity()
		},
		RunVMExec: func(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
			return s.Incus().RunVMExec(vmName, guestArgv, onData, timeout)
		},
		SupportedTools: s.toolsFn,
	})
}
