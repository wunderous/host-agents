package hostagent

import (
	"time"

	"github.com/wunderous/host-agents/internal/domain/cluster"
	hostexec "github.com/wunderous/host-agents/internal/exec"
)

// These aliases name the cluster domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
type (
	InstallClusterAgentArgs = cluster.InstallClusterAgentArgs
	ClusterListResult       = cluster.ClusterListResult
	ClusterNode             = cluster.ClusterNode
	ClusterDetail           = cluster.ClusterDetail
)

func (s *Service) Cluster() *cluster.Service {
	s.clusterOnce.Do(func() {
		s.clusterSvc = cluster.New(&s.shared, cluster.Deps{
			BridgeIP: func(vmName string) (string, error) {
				info, err := s.Incus().GetVMInfo(vmName, true)
				if err != nil {
					return "", err
				}
				return firstBridgeIPv4(info.IPv4), nil
			},
			GetVMInfo:                 s.Incus().GetVMInfo,
			ListKubernetesClusters:    s.Kubernetes().ListKubernetesClusters,
			RunAgentShellWithTimeout:  s.Host().RunAgentShellWithTimeout,
			ExecuteKubernetesProvider: s.executeKubernetesProvider,
			EnsureIncusDevice:         s.Incus().EnsureDevice,
			ReadIncusInstanceType:     s.Incus().ReadInstanceType,
			RunVMExec: func(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
				return s.Incus().RunVMExec(vmName, guestArgv, onData, timeout)
			},
			WaitForVMExecReady:     s.Incus().WaitForVMExecReady,
			WaitForVMServiceActive: s.Incus().WaitForVMServiceActive,
			WaitForSystemdActive:   s.Host().WaitForSystemdActive,
		})
	})
	return s.clusterSvc
}
