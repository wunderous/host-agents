// Package cluster owns Kubernetes cluster discovery on this host and the
// cluster agent that runs inside a guest: installing it, reaching the control
// plane bridge from the guest, and reporting what clusters exist.
package cluster

import (
	"time"

	"github.com/wunderous/host-agents/internal/contract/clusterinfo"
	"github.com/wunderous/host-agents/internal/contract/vminfo"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/tcprelay"
)

// Deps are the cross-domain capabilities cluster requires: incus for the guest
// it installs into, kubernetes for the control plane it reports on, and host
// for the systemd unit the agent runs as.
type Deps struct {
	// BridgeIP is the guest-reachable address of a VM. It is a primitive seam
	// rather than GetVMInfo because the caller only needs the one address.
	BridgeIP                  func(vmName string) (string, error)
	GetVMInfo                 func(vmName string, fast bool) (vminfo.VMInfo, error)
	ListKubernetesClusters    func(source string) (clusterinfo.ClusterListResult, error)
	RunAgentShellWithTimeout  func(command string, timeout time.Duration, onData func(string)) (hostexec.Result, error)
	ExecuteKubernetesProvider func(operation, targetURI string, arguments map[string]any) (map[string]any, bool, error)
	EnsureIncusDevice         func(instanceName, deviceName string, settings []string) error
	ReadIncusInstanceType     func(instanceName string) (string, error)
	RunVMExec                 func(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error)
	WaitForVMExecReady        func(vmName string, timeout time.Duration, onData func(string)) error
	WaitForVMServiceActive    func(vmName, service string, onData func(string), timeout time.Duration) error
	WaitForSystemdActive      func(service string, onData func(string), timeout time.Duration) error
}

// Service is the cluster domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps

	// guestBridgeRelay forwards the control plane bridge port to a guest. It is
	// a live listener, so a Service is constructed once per host service.
	guestBridgeRelay *tcprelay.Manager
}

// New builds the cluster domain over the shared runtime seam.
func New(shared *hostruntime.Shared, deps Deps) *Service {
	return &Service{shared: shared, deps: deps, guestBridgeRelay: tcprelay.New()}
}
