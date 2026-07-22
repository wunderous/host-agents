package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/provider"
)

const (
	defaultDiscoveryTimeout = 45 * time.Second
	provisionVMTimeout      = 10 * time.Minute
	hostK3sServiceWait      = 120 * time.Second
	clusterAgentServiceName = "opute-cluster-agent"
	sqlConnectorMaxPerHost  = 32
	sqlConnectorIdleDrain   = 120 * time.Second
)

var clusterScopedK8sResources = map[string]bool{
	"namespaces": true, "ingressclasses": true, "storageclasses": true, "clusterissuers": true,
}

// HostInfoResult mirrors the TypeScript describeHost payload.
type HostInfoResult struct {
	HostName       string   `json:"hostName"`
	ProviderID     string   `json:"providerId"`
	LXCBinaryPath  string   `json:"lxcBinaryPath"`
	SystemctlPath  string   `json:"systemctlPath"`
	SupportedTools []string `json:"supportedTools"`
}

// BridgeDiagnosticResult is returned by DiagnoseBridge.
type BridgeDiagnosticResult struct {
	BridgeProcess struct {
		Status    string `json:"status"`
		Command   string `json:"command,omitempty"`
		Restarted bool   `json:"restarted,omitempty"`
	} `json:"bridgeProcess"`
	BridgePort struct {
		Port   int    `json:"port"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"bridgePort"`
	Database struct {
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"database"`
	LastHeartbeat struct {
		At *string `json:"at"`
	} `json:"lastHeartbeat"`
	BridgeStatus string `json:"bridgeStatus"`
	CheckedAt    string `json:"checkedAt"`
}

// HostOperationsService implements host MCP operations against Incus on Linux.
type HostOperationsService struct {
	runtime *provider.Runtime
	toolsFn func(providerID string) []string

	sqlSupervisor          *sqlConnectorSupervisor
	guestBridgeRelay       *tcpRelayManager
	localLLMRelay          *localLLMRelayManager
	allowInsecureDownloads bool
}

type Options struct {
	ProviderID             provider.ID
	ToolsForProvider       func(providerID string) []string
	AllowInsecureDownloads bool
}

func NewHostOperationsService(opts Options) *HostOperationsService {
	cfg := provider.ResolveConfig(opts.ProviderID)
	rt := provider.NewRuntime(cfg)
	toolsFn := opts.ToolsForProvider
	if toolsFn == nil {
		toolsFn = func(string) []string { return nil }
	}
	return &HostOperationsService{
		runtime:                rt,
		toolsFn:                toolsFn,
		sqlSupervisor:          newSQLConnectorSupervisor(),
		guestBridgeRelay:       newTCPRelayManager(),
		localLLMRelay:          newPersistentLocalLLMRelayManager(),
		allowInsecureDownloads: opts.AllowInsecureDownloads,
	}
}

func (s *HostOperationsService) ReadProviderID() string {
	return string(s.runtime.ReadProviderID())
}

func (s *HostOperationsService) DescribeHost() HostInfoResult {
	pid := s.ReadProviderID()
	host, _ := os.Hostname()
	return HostInfoResult{
		HostName:       host,
		ProviderID:     pid,
		LXCBinaryPath:  s.runtime.ProviderBinary(),
		SystemctlPath:  provider.DefaultSystemctlPath,
		SupportedTools: s.toolsFn(pid),
	}
}

func (s *HostOperationsService) waitForVMExecReady(vmName string, timeout time.Duration, onData func(string)) error {
	return s.waitForIncusAgent(vmName, timeout, onData)
}

func (s *HostOperationsService) RunAgentShell(command string, onData func(string)) (hostexec.Result, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return hostexec.Result{}, errors.New("command is required")
	}
	return s.runtime.RunHost([]string{"bash", "-lc", command}, onData, 0)
}

func (s *HostOperationsService) commandRunner(args []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.runtime.RunProvider(args, onData, timeout)
}

func (s *HostOperationsService) hostCommandRunner(command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.runtime.RunHost(command, onData, timeout)
}

func (s *HostOperationsService) hostCommandRunnerContext(ctx context.Context, command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.runtime.RunHostContext(ctx, command, onData, timeout)
}

func (s *HostOperationsService) vmExecArgv(vmName string, guestArgv []string) []string {
	return append([]string{"exec", vmName, "--"}, guestArgv...)
}

func (s *HostOperationsService) runVMExec(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.commandRunner(s.vmExecArgv(vmName, guestArgv), onData, timeout)
}

func (s *HostOperationsService) runVMExecContext(ctx context.Context, vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.runtime.RunVMExecContext(ctx, vmName, guestArgv, onData, timeout)
}

type VMListResult struct {
	VMs []VMInfo `json:"vms"`
}

type VMInfo struct {
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	State      map[string]any `json:"state"`
	IPv4       []string       `json:"ipv4"`
	Release    string         `json:"release"`
	ProviderID string         `json:"providerId"`
	CPUs       *int           `json:"cpus,omitempty"`
	Memory     string         `json:"memory,omitempty"`
	Disk       string         `json:"disk,omitempty"`
	AgentReady bool           `json:"agentReady,omitempty"`
}

// --- VM lifecycle ---

type ProvisionVMArgs struct {
	VMName string `json:"vmName"`
	Image  string `json:"image,omitempty"`
	CPUs   int    `json:"cpus,omitempty"`
	Memory string `json:"memory,omitempty"`
	Disk   string `json:"disk,omitempty"`
}

type VMStatusResult struct {
	VMName string `json:"vmName"`
	Image  string `json:"image,omitempty"`
	Status string `json:"status"`
}

func (s *HostOperationsService) CreateVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
	return s.provisionVM(args, onData)
}

func (s *HostOperationsService) ProvisionVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
	return s.provisionVM(args, onData)
}

func (s *HostOperationsService) provisionVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return VMStatusResult{}, errors.New("vmName is required")
	}
	image := normalizeIncusLaunchImage(strings.TrimSpace(args.Image))
	if image == "" {
		image = "images:ubuntu/22.04"
	}
	cpus := args.CPUs
	if cpus <= 0 {
		cpus = 2
	}
	memory := strings.TrimSpace(args.Memory)
	if memory == "" {
		memory = "2GiB"
	}
	disk := strings.TrimSpace(args.Disk)
	if disk == "" {
		disk = "10GiB"
	}

	if err := s.launchVM(vmName, image, cpus, memory, disk, onData, provisionVMTimeout); err != nil {
		return VMStatusResult{}, err
	}
	if image == "" {
		image = "images:ubuntu/22.04"
	} else {
		image = normalizeIncusLaunchImage(image)
	}
	return VMStatusResult{VMName: vmName, Image: image, Status: "running"}, nil
}

type VMScopedArgs struct {
	VMName string `json:"vmName"`
}

func (s *HostOperationsService) StartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	res, err := s.commandRunner([]string{"start", vmName}, onData, 0)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to start VM"))
	}
	return map[string]string{"vmName": vmName, "status": "running"}, nil
}

func (s *HostOperationsService) StopVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	cmd := s.stopVMArgs(vmName)
	res, err := s.commandRunner(cmd, onData, 0)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to stop VM"))
	}
	return map[string]string{"vmName": vmName, "status": "stopped"}, nil
}

func (s *HostOperationsService) RestartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	stop, err := s.commandRunner(s.stopVMArgs(vmName), onData, 0)
	if err != nil {
		return nil, err
	}
	if stop.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(stop.Stderr, stop.Stdout, "failed to stop VM during restart"))
	}
	start, err := s.commandRunner([]string{"start", vmName}, onData, 0)
	if err != nil {
		return nil, err
	}
	if start.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(start.Stderr, start.Stdout, "failed to start VM during restart"))
	}
	return map[string]string{"vmName": vmName, "status": "running"}, nil
}

func (s *HostOperationsService) DeleteVM(args VMScopedArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	res, err := s.commandRunner(s.deleteVMArgs(vmName), onData, 0)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to delete VM"))
	}
	return map[string]any{"vmName": vmName, "deleted": true}, nil
}

func (s *HostOperationsService) launchVM(vmName, image string, cpus int, memory, disk string, onData func(string), timeout time.Duration) error {
	return s.launchIncusVMViaAPI(vmName, image, cpus, memory, disk, onData, timeout)
}

func (s *HostOperationsService) stopVMArgs(vmName string) []string {
	return []string{"stop", vmName, "--force"}
}

func (s *HostOperationsService) deleteVMArgs(vmName string) []string {
	return []string{"delete", vmName, "--force"}
}

// --- K3s ---

type InstallK3sArgs struct {
	Target      string   `json:"target,omitempty"`
	VMName      string   `json:"vmName,omitempty"`
	ClusterID   string   `json:"clusterId,omitempty"`
	Version     string   `json:"version,omitempty"`
	InstallArgs []string `json:"installArgs,omitempty"`
}

func (s *HostOperationsService) InstallK3s(ctx context.Context, args InstallK3sArgs, onData func(string)) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target := strings.TrimSpace(args.Target)
	if target == "" {
		target = "vm"
	}
	// Pin a concrete version by default. update.k3s.io channel resolution often
	// 404s from guest NAT; the upstream script then treats "stable" as a GitHub
	// release tag and fails on .../download/stable/sha256sum-amd64.txt.
	// Download the installer to a file, then run with an explicit env so the
	// version cannot be lost across pipes / login shells.
	k3sVersion := strings.TrimSpace(args.Version)
	if k3sVersion == "" {
		k3sVersion = "v1.31.8+k3s1"
	}
	curlFlags := "-sfL"
	if s.allowInsecureDownloads {
		// Some local VM images do not contain the host's corporate CA. Keep the
		// weaker TLS behavior explicit and standalone-only; platform mode remains
		// certificate-verifying by default.
		curlFlags = "-k -sfL --retry 4 --retry-delay 2 --retry-connrefused"
	}
	execEnv := ""
	if len(args.InstallArgs) > 0 {
		execEnv = fmt.Sprintf(" INSTALL_K3S_EXEC=%s", shellEscape(strings.Join(args.InstallArgs, " ")))
	}
	// Single-line bash -c (not login -lc): Incus/guest argv must not depend on
	// multiline -c parsing. Echo a pin marker so failures prove this path ran.
	installCmd := fmt.Sprintf(
		`echo OPUTE_K3S_PIN=%s && tmp=$(mktemp) && curl %s https://get.k3s.io -o "$tmp" && env INSTALL_K3S_VERSION=%s%s sh "$tmp"; ec=$?; rm -f "$tmp"; exit $ec`,
		shellEscape(k3sVersion),
		curlFlags,
		shellEscape(k3sVersion),
		execEnv,
	)
	if target == "host" {
		clusterID := strings.TrimSpace(args.ClusterID)
		if clusterID == "" {
			return nil, errors.New("clusterId is required for host K3s installation")
		}
		prep := `if test -x /usr/local/bin/k3s-uninstall.sh; then /usr/local/bin/k3s-uninstall.sh || true; fi
rm -f /etc/systemd/system/k3s.service.env /etc/systemd/system/k3s.service 2>/dev/null || true
systemctl daemon-reload 2>/dev/null || true`
		if _, err := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", prep}, onData, 0); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := s.hostCommandRunnerContext(ctx, []string{"bash", "-c", installCmd}, onData, 0)
		if err != nil {
			return nil, err
		}
		if res.ExitCode != 0 && !isRecoverableHostK3sInstall(res) {
			return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to install K3s on host"))
		}
		if err := s.waitForSystemdActive("k3s", onData, hostK3sServiceWait); err != nil {
			return nil, err
		}
		ready, err := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", "sudo -n /usr/local/bin/k3s kubectl get nodes -o name"}, onData, defaultDiscoveryTimeout)
		if err != nil || ready.ExitCode != 0 {
			return nil, fmt.Errorf("%s", firstNonEmpty(ready.Stderr, ready.Stdout, "K3s API not ready on host"))
		}
		return map[string]any{"clusterId": clusterID, "serviceName": "k3s", "status": "active", "target": "host"}, nil
	}

	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	if err := s.waitForVMExecReady(vmName, 5*time.Minute, onData); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ensureCurl := `if ! command -v curl >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive apt-get install -y curl >/dev/null || (apt-get update >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y curl >/dev/null); fi`
	if res, err := s.runVMExecContext(ctx, vmName, []string{"bash", "-lc", ensureCurl}, onData, 0); err != nil || res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to ensure curl in VM"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	install, err := s.runVMExecContext(ctx, vmName, []string{"bash", "-c", installCmd}, onData, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	if install.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(install.Stderr, install.Stdout, "failed to install K3s in VM"))
	}
	if err := s.waitForVMServiceActive(vmName, "k3s", onData, 5*time.Minute); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "serviceName": "k3s", "status": "active", "target": "vm"}, nil
}

type UninstallK3sArgs struct {
	Target    string `json:"target,omitempty"`
	VMName    string `json:"vmName,omitempty"`
	ClusterID string `json:"clusterId,omitempty"`
}

func (s *HostOperationsService) UninstallK3s(args UninstallK3sArgs, onData func(string)) (map[string]any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		target = "vm"
	}
	if target == "host" {
		clusterID := strings.TrimSpace(args.ClusterID)
		if clusterID == "" {
			return nil, errors.New("clusterId is required for host K3s uninstall")
		}
		res, err := s.hostCommandRunner([]string{"bash", "-lc", "/usr/local/bin/k3s-uninstall.sh"}, onData, 0)
		if err != nil || res.ExitCode != 0 {
			return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to uninstall K3s from host"))
		}
		verify, err := s.hostCommandRunner([]string{"bash", "-lc", "test ! -x /usr/local/bin/k3s"}, onData, 0)
		if err != nil || verify.ExitCode != 0 {
			return nil, errors.New("k3s is still installed on host after uninstall")
		}
		return map[string]any{"clusterId": clusterID, "serviceName": "k3s", "status": "removed", "target": "host"}, nil
	}
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	if err := s.waitForVMExecReady(vmName, 5*time.Minute, onData); err != nil {
		return nil, err
	}
	uninstall, err := s.runVMExec(vmName, []string{"bash", "-lc", "/usr/local/bin/k3s-uninstall.sh"}, onData, 0)
	if err != nil || uninstall.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(uninstall.Stderr, uninstall.Stdout, "failed to uninstall K3s from VM"))
	}
	verify, err := s.runVMExec(vmName, []string{"bash", "-lc", "test ! -x /usr/local/bin/k3s"}, onData, 0)
	if err != nil || verify.ExitCode != 0 {
		return nil, errors.New("k3s is still installed in VM after uninstall")
	}
	return map[string]any{"vmName": vmName, "serviceName": "k3s", "status": "removed", "target": "vm"}, nil
}

func (s *HostOperationsService) ConfigureK3sLoadBalancer(_ map[string]any, _ func(string)) (map[string]any, error) {
	return nil, errors.New("configure_k3s_load_balancer is not implemented in the Go host agent; HA load balancer setup requires the full TypeScript host MCP implementation")
}

func (s *HostOperationsService) ConfigureK3sHaServers(_ map[string]any, _ func(string)) (map[string]any, error) {
	return nil, errors.New("configure_k3s_ha_servers is not implemented in the Go host agent; multi-server HA provisioning requires the full TypeScript host MCP implementation")
}

// --- Kubernetes inventory ---

type k8sMeta struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	CreationTimestamp string `json:"creationTimestamp"`
}

type k8sPodItem struct {
	Metadata k8sMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		PodIP             string `json:"podIP"`
		ContainerStatuses []struct {
			Ready        bool `json:"ready"`
			RestartCount int  `json:"restartCount"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type k8sDeploymentItem struct {
	Metadata k8sMeta `json:"metadata"`
	Spec     struct {
		Replicas int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas       int `json:"readyReplicas"`
		AvailableReplicas   int `json:"availableReplicas"`
		UnavailableReplicas int `json:"unavailableReplicas"`
	} `json:"status"`
}

func (s *HostOperationsService) ListNamespaces(vmName string) ([]string, error) {
	data, err := s.getKubernetesList(vmName, "namespaces", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		out = append(out, meta["name"].(string))
	}
	return out, nil
}

func (s *HostOperationsService) ListStorageClasses(vmName string) ([]string, error) {
	data, err := s.getKubernetesList(vmName, "storageclasses", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		out = append(out, meta["name"].(string))
	}
	return out, nil
}

func (s *HostOperationsService) ListServices(vmName, namespace string) ([]map[string]string, error) {
	data, err := s.getKubernetesList(vmName, "services", namespace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]string, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		out = append(out, map[string]string{
			"name":      meta["name"].(string),
			"namespace": meta["namespace"].(string),
		})
	}
	return out, nil
}

func (s *HostOperationsService) ListPods(vmName, namespace string) ([]map[string]any, error) {
	data, err := s.getKubernetesList(vmName, "pods", namespace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, raw := range data["items"].([]any) {
		var item k8sPodItem
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &item)
		ready := true
		restarts := 0
		for _, cs := range item.Status.ContainerStatuses {
			if !cs.Ready {
				ready = false
			}
			restarts += cs.RestartCount
		}
		row := map[string]any{
			"name":      item.Metadata.Name,
			"namespace": item.Metadata.Namespace,
			"status":    defaultString(item.Status.Phase, "Unknown"),
			"ready":     ready,
			"restarts":  restarts,
			"age":       k8sAge(item.Metadata.CreationTimestamp),
		}
		if item.Status.PodIP != "" {
			row["ip"] = item.Status.PodIP
		}
		if item.Spec.NodeName != "" {
			row["node"] = item.Spec.NodeName
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *HostOperationsService) ListDeployments(vmName, namespace string) ([]map[string]any, error) {
	data, err := s.getKubernetesList(vmName, "deployments", namespace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, raw := range data["items"].([]any) {
		var item k8sDeploymentItem
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &item)
		ready := item.Status.ReadyReplicas
		desired := item.Spec.Replicas
		status := "pending"
		if ready >= desired && ready > 0 {
			status = "ready"
		}
		out = append(out, map[string]any{
			"name":        item.Metadata.Name,
			"namespace":   item.Metadata.Namespace,
			"ready":       ready,
			"desired":     desired,
			"available":   item.Status.AvailableReplicas,
			"unavailable": item.Status.UnavailableReplicas,
			"age":         k8sAge(item.Metadata.CreationTimestamp),
			"status":      status,
		})
	}
	return out, nil
}

func (s *HostOperationsService) getKubernetesList(vmName, resource, namespace string) (map[string]any, error) {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	nsArgs := []string{"--all-namespaces"}
	if namespace != "" {
		nsArgs = []string{"-n", namespace}
	} else if clusterScopedK8sResources[resource] {
		nsArgs = nil
	}
	stdout, err := s.runKubernetesKubectl(vmName, append([]string{"get", resource}, append(nsArgs, "-o", "json")...), "list "+resource)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(stdout, "{") {
		return nil, fmt.Errorf("expected JSON output while listing %s", resource)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		return nil, err
	}
	items, ok := parsed["items"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid Kubernetes %s response: missing items array", resource)
	}
	return map[string]any{"items": items}, nil
}

func (s *HostOperationsService) runKubernetesKubectl(vmName string, kubectlArgs []string, label string) (string, error) {
	return s.runKubernetesKubectlTimed(vmName, kubectlArgs, label, defaultDiscoveryTimeout)
}

func (s *HostOperationsService) runKubernetesKubectlTimed(vmName string, kubectlArgs []string, label string, timeout time.Duration) (string, error) {
	variants := [][]string{
		s.vmExecArgv(vmName, append([]string{"kubectl"}, kubectlArgs...)),
		s.vmExecArgv(vmName, append([]string{"k3s", "kubectl"}, kubectlArgs...)),
	}
	var lastErr error
	for _, cmd := range variants {
		res, err := s.commandRunner(cmd, nil, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		if res.ExitCode != 0 {
			lastErr = errors.New(firstNonEmpty(res.Stderr, res.Stdout, fmt.Sprintf("exit %d", res.ExitCode)))
			continue
		}
		return strings.TrimSpace(res.Stdout), nil
	}
	if lastErr == nil {
		lastErr = errors.New("unknown error")
	}
	return "", fmt.Errorf("failed to %s in %s: %w", label, vmName, lastErr)
}

func (s *HostOperationsService) ensureHelmTargetNamespace(vmName, namespace string) error {
	if namespace == "" || namespace == "kube-system" {
		return nil
	}
	if _, err := s.runKubernetesKubectl(vmName, []string{"get", "namespace", namespace}, "get namespace"); err == nil {
		return nil
	}
	_, err := s.runKubernetesKubectl(vmName, []string{"create", "namespace", namespace}, "create namespace")
	return err
}

// --- Cluster agent ---

type InstallClusterAgentArgs struct {
	VMName      string `json:"vmName,omitempty"`
	ClusterID   string `json:"clusterId"`
	ClusterName string `json:"clusterName"`
	AgentID     string `json:"agentId"`
	BridgeToken string `json:"bridgeToken"`
	BridgeURL   string `json:"bridgeUrl,omitempty"`
	BridgePort  int    `json:"bridgePort,omitempty"`
	APIEndpoint string `json:"apiEndpoint,omitempty"`
	ProviderID  string `json:"providerId,omitempty"`
	ResourceID  string `json:"resourceId,omitempty"`
	Source      string `json:"source,omitempty"`
}

// --- Host services / prerequisites ---

type RestartHostServiceArgs struct {
	ServiceName string `json:"serviceName"`
}

var safeSystemdUnitName = regexp.MustCompile(`^[A-Za-z0-9_.@:-]+$`)

func restartServiceCommand(serviceName string) []string {
	// The production host agent itself is a systemd *user* unit.  Invoking
	// plain systemctl from the unprivileged agent asks polkit for interactive
	// elevation and fails over MCP with “Interactive authentication required”.
	// Keep system services on the existing system scope, but route Opute-owned
	// user units through the user manager they actually belong to.
	if strings.HasPrefix(serviceName, "opute-") {
		// --no-block is essential when the target is this very host-agent
		// service: waiting for systemd to finish stopping the process closes the
		// reverse-tunnel request before the MCP operation can receive a result.
		return []string{provider.DefaultSystemctlPath, "--user", "--no-block", "restart", serviceName}
	}
	return []string{provider.DefaultSystemctlPath, "restart", serviceName}
}

func serviceStatusCommand(serviceName string) []string {
	if strings.HasPrefix(serviceName, "opute-") {
		return []string{provider.DefaultSystemctlPath, "--user", "is-active", serviceName}
	}
	return []string{provider.DefaultSystemctlPath, "is-active", serviceName}
}

func (s *HostOperationsService) RestartHostService(args RestartHostServiceArgs, onData func(string)) (map[string]string, error) {
	serviceName := strings.TrimSpace(args.ServiceName)
	if serviceName == "" {
		return nil, errors.New("serviceName is required")
	}
	if !safeSystemdUnitName.MatchString(serviceName) {
		return nil, errors.New("serviceName contains invalid characters")
	}
	restart, err := s.hostCommandRunner(restartServiceCommand(serviceName), onData, 0)
	if err != nil || restart.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(restart.Stderr, restart.Stdout, "failed to restart service"))
	}
	if strings.HasPrefix(serviceName, "opute-") {
		return map[string]string{"serviceName": serviceName, "status": "scheduled"}, nil
	}
	verify, err := s.hostCommandRunner(serviceStatusCommand(serviceName), onData, 0)
	if err != nil || verify.ExitCode != 0 || strings.TrimSpace(verify.Stdout) != "active" {
		return nil, fmt.Errorf("service '%s' is not active after restart", serviceName)
	}
	return map[string]string{"serviceName": serviceName, "status": "active"}, nil
}

func (s *HostOperationsService) EnsureDocker(onData func(string)) (map[string]any, error) {
	return nil, errors.New("ensure_docker is not supported on Incus Linux host agents")
}

func (s *HostOperationsService) EnsureK3d(onData func(string)) (map[string]any, error) {
	return nil, errors.New("ensure_k3d is not supported on Incus Linux host agents")
}

// --- SQL connector (TCP relay) ---

type EnsureSQLConnectorArgs struct {
	DatabaseID string `json:"databaseId"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
	ListenPort int    `json:"listenPort,omitempty"`
	ListenHost string `json:"listenHost,omitempty"`
}

type SQLConnectorResult struct {
	DatabaseID string `json:"databaseId"`
	SessionID  string `json:"sessionId"`
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort"`
	PathMode   string `json:"pathMode"`
	RefCount   int    `json:"refCount"`
}

func (s *HostOperationsService) EnsureSQLConnector(args EnsureSQLConnectorArgs) (SQLConnectorResult, error) {
	return s.sqlSupervisor.ensureConnector(args)
}

func (s *HostOperationsService) GetSQLConnectorStatus(databaseID string) (map[string]any, error) {
	return s.sqlSupervisor.getStatus(databaseID), nil
}

func (s *HostOperationsService) ReleaseSQLConnector(databaseID string, force bool) (bool, error) {
	return s.sqlSupervisor.releaseConnector(databaseID, force)
}

func (s *HostOperationsService) StopAllHostTCPRelays() error {
	return s.sqlSupervisor.stopAll()
}

// --- Bridge diagnostics ---

func (s *HostOperationsService) DiagnoseBridge(ctx context.Context) (BridgeDiagnosticResult, error) {
	return probeBridgeHealth(ctx)
}

func (s *HostOperationsService) RecoverBridge(ctx context.Context, onData func(string)) (BridgeDiagnosticResult, error) {
	serviceName := envOr("BRIDGE_SERVICE_NAME", "opute-bridge")
	if _, err := s.RestartHostService(RestartHostServiceArgs{ServiceName: serviceName}, onData); err != nil {
		return BridgeDiagnosticResult{}, err
	}
	result, err := probeBridgeHealth(ctx)
	if err != nil {
		return result, err
	}
	result.BridgeProcess.Restarted = true
	return result, nil
}

func probeBridgeHealth(ctx context.Context) (BridgeDiagnosticResult, error) {
	port := 9093
	if p := strings.TrimSpace(os.Getenv("BRIDGE_PORT")); p != "" {
		fmt.Sscanf(p, "%d", &port)
	} else if p := strings.TrimSpace(os.Getenv("PLATFORM_MCP_PORT")); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	bridgeURL := envOr("BRIDGE_URL", fmt.Sprintf("http://127.0.0.1:%d", port))
	serviceName := envOr("BRIDGE_SERVICE_NAME", "opute-bridge")
	checkedAt := time.Now().UTC().Format(time.RFC3339)

	portOpen, portErr := probeTCPPort(ctx, "127.0.0.1", port)
	result := BridgeDiagnosticResult{CheckedAt: checkedAt}
	result.BridgeProcess.Command = serviceName
	if portOpen {
		result.BridgeProcess.Status = "running"
		result.BridgePort.Port = port
		result.BridgePort.Status = "open"
		result.BridgeStatus = "online"
	} else {
		result.BridgeProcess.Status = "stopped"
		result.BridgePort.Port = port
		result.BridgePort.Status = "closed"
		if portErr != nil {
			result.BridgePort.Error = portErr.Error()
		}
		result.BridgeStatus = "offline"
	}

	dbStatus := "unhealthy"
	dbErr := "Bridge health check failed"
	var lastHeartbeat *string
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(bridgeURL, "/")+"/health", nil)
	if err == nil {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var body struct {
					Database struct {
						Status string `json:"status"`
						Error  string `json:"error"`
					} `json:"database"`
					LastHeartbeatAt *string `json:"lastHeartbeatAt"`
				}
				if json.NewDecoder(resp.Body).Decode(&body) == nil {
					lastHeartbeat = body.LastHeartbeatAt
					if body.Database.Status == "healthy" {
						dbStatus = "healthy"
						dbErr = ""
					} else if body.Database.Error != "" {
						dbErr = body.Database.Error
					}
				}
			} else {
				dbErr = fmt.Sprintf("Bridge health check failed with HTTP %d", resp.StatusCode)
			}
		} else {
			dbErr = err.Error()
		}
	}
	result.Database.Status = dbStatus
	if dbErr != "" {
		result.Database.Error = dbErr
	}
	result.LastHeartbeat.At = lastHeartbeat
	return result, nil
}

func probeTCPPort(ctx context.Context, host string, port int) (bool, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

// --- helpers ---

func (s *HostOperationsService) waitForSystemdActive(service string, onData func(string), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := s.hostCommandRunner([]string{provider.DefaultSystemctlPath, "is-active", service}, onData, 0)
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "active" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("systemd service '%s' did not become active within %s", service, timeout)
}

func (s *HostOperationsService) waitForVMServiceActive(vmName, service string, onData func(string), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := s.runVMExec(vmName, []string{provider.DefaultSystemctlPath, "is-active", service}, onData, 30*time.Second)
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "active" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("VM service '%s' on '%s' did not become active within %s", service, vmName, timeout)
}

func isRecoverableHostK3sInstall(res hostexec.Result) bool {
	out := res.Stdout + "\n" + res.Stderr
	return strings.Contains(out, "Failed to restart k3s.service") ||
		strings.Contains(out, "Unit k3s.service not found") ||
		strings.Contains(out, "k3s.service.env")
}

func shellEscape(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func resolveBridgeURLFromEnv() string {
	for _, key := range []string{"BRIDGE_URL", "OPUTE_BRIDGE_PUBLIC_URL", "BRIDGE_PUBLIC_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	port := envOr("BRIDGE_PORT", envOr("PLATFORM_MCP_PORT", "9093"))
	return fmt.Sprintf("http://127.0.0.1:%s", port)
}

func k8sAge(creationTimestamp string) string {
	if creationTimestamp == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, creationTimestamp)
	if err != nil {
		return "unknown"
	}
	elapsed := time.Since(t)
	minutes := int(elapsed.Minutes())
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}

// --- TCP relay + SQL connector supervisor ---

type tcpRelayManager struct {
	mu            sync.Mutex
	sessions      map[string]*relaySession
	portToSession map[int]string
}

type relaySession struct {
	sessionID  string
	listenHost string
	listenPort int
	targetHost string
	targetPort int
	listener   net.Listener
	active     map[net.Conn]struct{}
}

func newTCPRelayManager() *tcpRelayManager {
	return &tcpRelayManager{
		sessions:      make(map[string]*relaySession),
		portToSession: make(map[int]string),
	}
}

func (m *tcpRelayManager) startRelay(sessionID, listenHost string, listenPort int, targetHost string, targetPort int) (relaySession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return relaySession{}, errors.New("sessionId is required")
	}
	targetHost = strings.TrimSpace(targetHost)
	if targetHost == "" {
		return relaySession{}, errors.New("targetHost is required")
	}
	if listenHost == "" {
		listenHost = "0.0.0.0"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[sessionID]; exists {
		return relaySession{}, fmt.Errorf("TCP relay session '%s' is already active", sessionID)
	}
	if listenPort != 0 {
		if sid, inUse := m.portToSession[listenPort]; inUse {
			return relaySession{}, fmt.Errorf("TCP relay listen port %d is already in use by %s", listenPort, sid)
		}
	}

	var lc net.ListenConfig
	addr := fmt.Sprintf("%s:%d", listenHost, listenPort)
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return relaySession{}, err
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close()
		return relaySession{}, errors.New("TCP relay failed to bind listener")
	}

	session := &relaySession{
		sessionID:  sessionID,
		listenHost: tcpAddr.IP.String(),
		listenPort: tcpAddr.Port,
		targetHost: targetHost,
		targetPort: targetPort,
		listener:   ln,
		active:     make(map[net.Conn]struct{}),
	}
	m.sessions[sessionID] = session
	m.portToSession[session.listenPort] = sessionID

	go m.acceptLoop(session)
	return *session, nil
}

func (m *tcpRelayManager) acceptLoop(session *relaySession) {
	for {
		client, err := session.listener.Accept()
		if err != nil {
			return
		}
		go m.pipe(session, client)
	}
}

func (m *tcpRelayManager) pipe(session *relaySession, client net.Conn) {
	upstream, err := net.Dial("tcp", net.JoinHostPort(session.targetHost, strconv.Itoa(session.targetPort)))
	if err != nil {
		client.Close()
		return
	}
	session.active[client] = struct{}{}
	session.active[upstream] = struct{}{}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, client)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	}()
	go func() {
		wg.Wait()
		upstream.Close()
		client.Close()
		delete(session.active, client)
		delete(session.active, upstream)
	}()
}

func closeWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

func (m *tcpRelayManager) stopRelay(sessionID string) bool {
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	delete(m.sessions, sessionID)
	delete(m.portToSession, session.listenPort)
	m.mu.Unlock()

	for conn := range session.active {
		conn.Close()
	}
	session.listener.Close()
	return true
}

func (m *tcpRelayManager) stopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stopRelay(id)
	}
}

type sqlConnectorSupervisor struct {
	relay    *tcpRelayManager
	mu       sync.Mutex
	sessions map[string]*sqlConnectorSession
}

type sqlConnectorSession struct {
	databaseID string
	sessionID  string
	listenHost string
	listenPort int
	targetHost string
	targetPort int
	refCount   int
	idleTimer  *time.Timer
}

func newSQLConnectorSupervisor() *sqlConnectorSupervisor {
	return &sqlConnectorSupervisor{
		relay:    newTCPRelayManager(),
		sessions: make(map[string]*sqlConnectorSession),
	}
}

func (s *sqlConnectorSupervisor) sessionIDForDatabase(databaseID string) string {
	return "sql-connector:" + strings.TrimSpace(databaseID)
}

func (s *sqlConnectorSupervisor) ensureConnector(args EnsureSQLConnectorArgs) (SQLConnectorResult, error) {
	databaseID := strings.TrimSpace(args.DatabaseID)
	if databaseID == "" {
		return SQLConnectorResult{}, errors.New("databaseId is required")
	}

	s.mu.Lock()
	if existing, ok := s.sessions[databaseID]; ok {
		if existing.idleTimer != nil {
			existing.idleTimer.Stop()
			existing.idleTimer = nil
		}
		existing.refCount++
		res := SQLConnectorResult{
			DatabaseID: databaseID,
			SessionID:  existing.sessionID,
			ListenHost: existing.listenHost,
			ListenPort: existing.listenPort,
			PathMode:   "host_tcp_relay",
			RefCount:   existing.refCount,
		}
		s.mu.Unlock()
		return res, nil
	}
	if len(s.sessions) >= sqlConnectorMaxPerHost {
		s.mu.Unlock()
		return SQLConnectorResult{}, fmt.Errorf("host SQL connector limit reached (%d)", sqlConnectorMaxPerHost)
	}
	s.mu.Unlock()

	sessionID := s.sessionIDForDatabase(databaseID)
	relay, err := s.relay.startRelay(sessionID, args.ListenHost, args.ListenPort, args.TargetHost, args.TargetPort)
	if err != nil {
		return SQLConnectorResult{}, err
	}

	s.mu.Lock()
	s.sessions[databaseID] = &sqlConnectorSession{
		databaseID: databaseID,
		sessionID:  sessionID,
		listenHost: relay.listenHost,
		listenPort: relay.listenPort,
		targetHost: relay.targetHost,
		targetPort: relay.targetPort,
		refCount:   1,
	}
	s.mu.Unlock()

	return SQLConnectorResult{
		DatabaseID: databaseID,
		SessionID:  sessionID,
		ListenHost: relay.listenHost,
		ListenPort: relay.listenPort,
		PathMode:   "host_tcp_relay",
		RefCount:   1,
	}, nil
}

func (s *sqlConnectorSupervisor) getStatus(databaseID string) map[string]any {
	databaseID = strings.TrimSpace(databaseID)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[databaseID]
	if !ok {
		return map[string]any{"databaseId": databaseID, "active": false, "refCount": 0}
	}
	return map[string]any{
		"databaseId": databaseID,
		"active":     true,
		"sessionId":  session.sessionID,
		"listenHost": session.listenHost,
		"listenPort": session.listenPort,
		"refCount":   session.refCount,
		"targetHost": session.targetHost,
		"targetPort": session.targetPort,
	}
}

func (s *sqlConnectorSupervisor) releaseConnector(databaseID string, force bool) (bool, error) {
	databaseID = strings.TrimSpace(databaseID)
	s.mu.Lock()
	session, ok := s.sessions[databaseID]
	if !ok {
		s.mu.Unlock()
		return false, nil
	}
	if force {
		s.mu.Unlock()
		s.drainSession(databaseID)
		return true, nil
	}
	session.refCount--
	if session.refCount > 0 {
		s.mu.Unlock()
		return true, nil
	}
	if session.idleTimer != nil {
		session.idleTimer.Stop()
	}
	dbID := databaseID
	session.idleTimer = time.AfterFunc(sqlConnectorIdleDrain, func() {
		s.mu.Lock()
		current, ok := s.sessions[dbID]
		if !ok || current.refCount > 0 {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		s.drainSession(dbID)
	})
	s.mu.Unlock()
	return true, nil
}

func (s *sqlConnectorSupervisor) drainSession(databaseID string) {
	s.mu.Lock()
	session, ok := s.sessions[databaseID]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.sessions, databaseID)
	if session.idleTimer != nil {
		session.idleTimer.Stop()
	}
	sessionID := session.sessionID
	s.mu.Unlock()
	s.relay.stopRelay(sessionID)
}

func (s *sqlConnectorSupervisor) stopAll() error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.sessions = make(map[string]*sqlConnectorSession)
	s.mu.Unlock()
	for _, id := range ids {
		s.drainSession(id)
	}
	s.relay.stopAll()
	return nil
}
