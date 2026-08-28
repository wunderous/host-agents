package ops

import (
	"time"

	"github.com/wunderous/host-agents/internal/domain/cluster"
	hostexec "github.com/wunderous/host-agents/internal/exec"
)

// The cluster domain owns these types and operations. HostOperationsService
// keeps delegating methods so the dispatch registry is unaffected; this file
// disappears with internal/ops itself.
type (
	InstallClusterAgentArgs = cluster.InstallClusterAgentArgs
	ClusterListResult       = cluster.ClusterListResult
	ClusterNode             = cluster.ClusterNode
	ClusterDetail           = cluster.ClusterDetail
)

func (s *HostOperationsService) cluster() *cluster.Service {
	s.clusterOnce.Do(func() {
		s.clusterSvc = cluster.New(&s.shared, cluster.Deps{
			BridgeIP: func(vmName string) (string, error) {
				info, err := s.GetVMInfo(vmName, true)
				if err != nil {
					return "", err
				}
				return firstBridgeIPv4(info.IPv4), nil
			},
			GetVMInfo:                 s.GetVMInfo,
			ListKubernetesClusters:    s.ListKubernetesClusters,
			RunAgentShellWithTimeout:  s.RunAgentShellWithTimeout,
			ExecuteKubernetesProvider: s.executeKubernetesProvider,
			EnsureIncusDevice:         s.incus().EnsureDevice,
			ReadIncusInstanceType:     s.incus().ReadInstanceType,
			RunVMExec: func(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
				return s.incus().RunVMExec(vmName, guestArgv, onData, timeout)
			},
			WaitForVMExecReady:     s.incus().WaitForVMExecReady,
			WaitForVMServiceActive: s.incus().WaitForVMServiceActive,
			WaitForSystemdActive:   s.host().WaitForSystemdActive,
		})
	})
	return s.clusterSvc
}

func (s *HostOperationsService) ListClusters(fast bool) (ClusterListResult, error) {
	return s.cluster().ListClusters(fast)
}

func (s *HostOperationsService) GetClusterDetails(vmName string, fast bool) (ClusterDetail, error) {
	return s.cluster().GetClusterDetails(vmName, fast)
}

func (s *HostOperationsService) GetClusterRuntimeDetails(vmName string) (ClusterDetail, error) {
	return s.cluster().GetClusterRuntimeDetails(vmName)
}

func (s *HostOperationsService) InstallClusterAgent(args InstallClusterAgentArgs, onData func(string)) (map[string]any, error) {
	return s.cluster().InstallClusterAgent(args, onData)
}
