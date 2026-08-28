package ops

import (
	"context"
	"os/exec"

	"github.com/wunderous/host-agents/internal/domain/incus"
)

// The incus domain owns these types and operations. HostOperationsService keeps
// delegating methods so the dispatch registry is unaffected; this file
// disappears with internal/ops itself.
type (
	IncusOwnershipMismatchError = incus.IncusOwnershipMismatchError
	ProvisionContainerArgs      = incus.ProvisionContainerArgs
	ContainerStatusResult       = incus.ContainerStatusResult
	ResetIncusStackArgs         = incus.ResetIncusStackArgs
	ResetIncusInventoryItem     = incus.ResetIncusInventoryItem
	VMListResult                = incus.VMListResult
	ProvisionVMArgs             = incus.ProvisionVMArgs
	VMStatusResult              = incus.VMStatusResult
	VMScopedArgs                = incus.VMScopedArgs
	UpdateVMResourcesArgs       = incus.UpdateVMResourcesArgs
	LocalPrerequisitesResult    = incus.LocalPrerequisitesResult
	VMInventoryCapacity         = incus.VMInventoryCapacity
)

func (s *HostOperationsService) incus() *incus.Service {
	s.incusOnce.Do(func() {
		s.incusSvc = incus.New(&s.shared, incus.Deps{
			ProbeIncusGPU: func(args map[string]any) (map[string]any, error) {
				return s.host().ProbeIncusGPU(args)
			},
			ReinstallIncusStack: func(onData func(string)) (map[string]any, error) {
				return s.host().InstallIncusStack(InstallIncusStackArgs{}, onData)
			},
			RevokeRelays: s.revokeRelays,
		}, s.resetCheckpointPath)
	})
	return s.incusSvc
}

// revokeRelays stops every in-process relay pointed at a guest. Only domains
// that have already been constructed can hold one.
func (s *HostOperationsService) revokeRelays() {
	if s.clusterSvc != nil {
		s.cluster().StopGuestBridgeRelays()
	}
	if s.llmSvc != nil {
		s.llm().StopRelays()
	}
	if s.postgresSvc != nil {
		s.postgres().RevokeAllRelays()
	}
}

func (s *HostOperationsService) ProvisionContainer(args ProvisionContainerArgs, onData func(string)) (ContainerStatusResult, error) {
	return s.incus().ProvisionContainer(args, onData)
}

func (s *HostOperationsService) ProbeGPUContainer(onData func(string)) (map[string]any, error) {
	return s.incus().ProbeGPUContainer(onData)
}

func (s *HostOperationsService) ResetIncusStack(ctx context.Context, args ResetIncusStackArgs, onData func(string)) (map[string]any, error) {
	return s.incus().ResetIncusStack(ctx, args, onData)
}

func (s *HostOperationsService) ListVMs(fast bool) (VMListResult, error) {
	return s.incus().ListVMs(fast)
}

func (s *HostOperationsService) VMInventoryCapacity() (VMInventoryCapacity, error) {
	return s.incus().VMInventoryCapacity()
}

func (s *HostOperationsService) VMInventoryStats() (int, int, error) {
	return s.incus().VMInventoryStats()
}

func (s *HostOperationsService) GetVMInfo(vmName string, fast bool) (VMInfo, error) {
	return s.incus().GetVMInfo(vmName, fast)
}

func (s *HostOperationsService) CheckLocalPrerequisites() (*LocalPrerequisitesResult, error) {
	return s.incus().CheckLocalPrerequisites()
}

func (s *HostOperationsService) GetLocalStatus() (map[string]any, error) {
	return s.incus().GetLocalStatus()
}

func (s *HostOperationsService) NewVMInteractiveCommand(vmName string) (*exec.Cmd, error) {
	return s.incus().NewVMInteractiveCommand(vmName)
}

func (s *HostOperationsService) CreateVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
	return s.incus().CreateVM(args, onData)
}

func (s *HostOperationsService) ProvisionVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
	return s.incus().ProvisionVM(args, onData)
}

func (s *HostOperationsService) StartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	return s.incus().StartVM(args, onData)
}

func (s *HostOperationsService) StopVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	return s.incus().StopVM(args, onData)
}

func (s *HostOperationsService) RestartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	return s.incus().RestartVM(args, onData)
}

func (s *HostOperationsService) UpdateVMResources(args UpdateVMResourcesArgs, onData func(string)) (map[string]string, error) {
	return s.incus().UpdateVMResources(args, onData)
}

func (s *HostOperationsService) DeleteVM(args VMScopedArgs, onData func(string)) (map[string]any, error) {
	return s.incus().DeleteVM(args, onData)
}
