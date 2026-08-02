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

const containerK3sKubeletInstallArg = "--kubelet-arg=feature-gates=KubeletInUserNamespace=true"

// containerK3sInstallArgs returns the installer arguments required by the
// pinned K3s v1.31 release when it runs in an Incus container. K3s v1.31 does
// not read the newer config-file kubelet-arg form, so this must be present in
// INSTALL_K3S_EXEC at install time. Keep caller-supplied arguments intact.
func containerK3sInstallArgs(installArgs []string) []string {
	out := append([]string(nil), installArgs...)
	hasServerCommand := false
	for _, arg := range out {
		if strings.TrimSpace(arg) == "server" {
			hasServerCommand = true
		}
		if strings.Contains(arg, "KubeletInUserNamespace=true") {
			if hasServerCommand {
				return out
			}
			return append([]string{"server"}, out...)
		}
	}
	if !hasServerCommand {
		out = append([]string{"server"}, out...)
	}
	return append(out, containerK3sKubeletInstallArg)
}

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

func (s *HostOperationsService) containerK3sHasCanonicalInstallArg(vmName string, onData func(string)) bool {
	check := fmt.Sprintf("systemctl cat k3s 2>/dev/null | grep -Fq -- %s", shellEscape(containerK3sKubeletInstallArg))
	result, err := s.runVMExec(vmName, []string{"bash", "-lc", check}, onData, defaultDiscoveryTimeout)
	return err == nil && result.ExitCode == 0
}

func (s *HostOperationsService) waitForK3sNodeReady(ctx context.Context, vmName string, onData func(string), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	consecutiveReady := 0
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := s.GetK3sStatus(vmName)
		if err == nil {
			readyNodes, _ := status["readyNodes"].(int)
			totalNodes, _ := status["totalNodes"].(int)
			if strings.EqualFold(fmt.Sprint(status["status"]), "ready") && totalNodes > 0 && readyNodes == totalNodes {
				consecutiveReady++
				if consecutiveReady >= 3 {
					return nil
				}
			} else {
				consecutiveReady = 0
			}
			if onData != nil && totalNodes > 0 {
				onData(fmt.Sprintf("K3s nodes %d/%d ready...", readyNodes, totalNodes))
			}
		} else {
			consecutiveReady = 0
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("K3s API on '%s' did not report ready nodes within %s", vmName, timeout)
}
