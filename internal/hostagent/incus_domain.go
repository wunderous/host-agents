package hostagent

import (
	"github.com/wunderous/host-agents/internal/domain/incus"
)

// These aliases name the incus domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
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

func (s *Service) Incus() *incus.Service {
	s.incusOnce.Do(func() {
		s.incusSvc = incus.New(&s.shared, incus.Deps{
			ProbeIncusGPU: func(args map[string]any) (map[string]any, error) {
				return s.Host().ProbeIncusGPU(args)
			},
			ReinstallIncusStack: func(onData func(string)) (map[string]any, error) {
				return s.Host().InstallIncusStack(InstallIncusStackArgs{}, onData)
			},
			RevokeRelays: s.revokeRelays,
		}, s.resetCheckpointPath)
	})
	return s.incusSvc
}

// revokeRelays stops every in-process relay pointed at a guest. Only domains
// that have already been constructed can hold one.
func (s *Service) revokeRelays() {
	if s.clusterSvc != nil {
		s.Cluster().StopGuestBridgeRelays()
	}
	if s.llmSvc != nil {
		s.Llm().StopRelays()
	}
	if s.postgresSvc != nil {
		s.Postgres().RevokeAllRelays()
	}
}
