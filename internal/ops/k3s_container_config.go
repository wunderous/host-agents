package ops

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// containerK3sKubeletConfig is required for K3s inside unprivileged Incus system
// containers on WSL — kubelet cannot write /proc/sys/vm/overcommit_memory without it.
const containerK3sKubeletConfig = `kubelet-arg:
  - feature-gates=KubeletInUserNamespace=true
`

func containerK3sKubeletConfigScript() string {
	encoded := shellEscape(containerK3sKubeletConfig)
	return fmt.Sprintf(
		`mkdir -p /etc/rancher/k3s
if test -f /etc/rancher/k3s/config.yaml && grep -q 'KubeletInUserNamespace' /etc/rancher/k3s/config.yaml; then
  exit 0
fi
printf %%s %s > /etc/rancher/k3s/config.yaml`,
		encoded,
	)
}

func (s *HostOperationsService) ensureContainerK3sKubeletConfig(vmName string, onData func(string)) (changed bool, err error) {
	check := `test -f /etc/rancher/k3s/config.yaml && grep -q 'KubeletInUserNamespace' /etc/rancher/k3s/config.yaml`
	existing, checkErr := s.runVMExec(vmName, []string{"bash", "-lc", check}, onData, defaultDiscoveryTimeout)
	if checkErr == nil && existing.ExitCode == 0 {
		return false, nil
	}
	if onData != nil {
		onData("Applying K3s container kubelet config (KubeletInUserNamespace)...")
	}
	write, writeErr := s.runVMExec(vmName, []string{"bash", "-lc", containerK3sKubeletConfigScript()}, onData, defaultDiscoveryTimeout)
	if writeErr != nil {
		return false, writeErr
	}
	if write.ExitCode != 0 {
		return false, fmt.Errorf("%s", firstNonEmpty(write.Stderr, write.Stdout, "failed to write K3s container config"))
	}
	return true, nil
}

func (s *HostOperationsService) restartVMK3sIfPresent(vmName string, onData func(string)) error {
	present, err := s.runVMExec(vmName, []string{"bash", "-lc", "test -x /usr/local/bin/k3s"}, onData, 30*time.Second)
	if err != nil || present.ExitCode != 0 {
		return nil
	}
	restart, restartErr := s.runVMExec(vmName, []string{"systemctl", "restart", "k3s"}, onData, 2*time.Minute)
	if restartErr != nil {
		return restartErr
	}
	if restart.ExitCode != 0 {
		return fmt.Errorf("%s", firstNonEmpty(restart.Stderr, restart.Stdout, "restart K3s failed"))
	}
	return nil
}

func (s *HostOperationsService) waitForK3sNodeReady(ctx context.Context, vmName string, onData func(string), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := s.GetK3sStatus(vmName)
		if err == nil {
			readyNodes, _ := status["readyNodes"].(int)
			totalNodes, _ := status["totalNodes"].(int)
			if strings.EqualFold(fmt.Sprint(status["status"]), "ready") && totalNodes > 0 && readyNodes == totalNodes {
				return nil
			}
			if onData != nil && totalNodes > 0 {
				onData(fmt.Sprintf("K3s nodes %d/%d ready...", readyNodes, totalNodes))
			}
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("K3s API on '%s' did not report ready nodes within %s", vmName, timeout)
}
