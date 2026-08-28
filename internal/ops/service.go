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
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/domain/llm"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/heartbeat"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/textutil"
)

const (
	provisionVMTimeout      = 10 * time.Minute
	clusterAgentServiceName = "opute-cluster-agent"
	sqlConnectorMaxPerHost  = 32
	sqlConnectorIdleDrain   = 120 * time.Second
)

var clusterScopedK8sResources = map[string]bool{
	"namespaces": true, "ingressclasses": true, "storageclasses": true, "clusterissuers": true,
}

// HostInfoResult mirrors the TypeScript describeHost payload.
type HostInfoResult struct {
	URI            string               `json:"uri"`
	HostName       string               `json:"hostName"`
	ProviderID     string               `json:"providerId"`
	LXCBinaryPath  string               `json:"lxcBinaryPath"`
	SystemctlPath  string               `json:"systemctlPath"`
	SupportedTools []string             `json:"supportedTools"`
	Capacity       *VMInventoryCapacity `json:"capacity,omitempty"`
	System         map[string]any       `json:"system,omitempty"`
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
	// shared carries the identity, configuration, and execution handles that
	// every domain needs and none owns (plan sec. 9.2). Domain packages take a
	// `*hostruntime.Shared` directly; this service delegates to it so the move
	// can happen one domain at a time.
	shared               hostruntime.Shared
	toolsFn              func(providerID string) []string
	resetCheckpointPath  string
	ociStoragePolicyPath string
	sqliteDatabaseRoot   string
	ociStorageMu         sync.Mutex

	sqlSupervisor    *sqlConnectorSupervisor
	guestBridgeRelay *tcpRelayManager
	// llm is the llm domain, built lazily -- see llm_delegate.go. It owns live
	// relay listeners, so it is one instance per service.
	llmSvc                 *llm.Service
	llmOnce                sync.Once
	relayDirs              [2]string
	postgresqlServiceRelay *postgresqlServiceRelayManager
	// container command seams keep runtime adapter tests independent of an
	// installed host runtime. They are intentionally scoped to this service.
	containerCommandFn          func(context.Context, string, ...string) ([]byte, error)
	containerStreamingCommandFn func(context.Context, string, []string, func(string)) error
	// k8s is the kubernetes domain, built lazily -- see kubernetes_delegate.go.
	k8s     *kubernetes.Service
	k8sOnce sync.Once
}

type Options struct {
	ProviderID                hostruntime.ID
	ToolsForProvider          func(providerID string) []string
	InstanceID                string
	AgentID                   string
	OwnershipMode             string
	RelayConfigDir            string
	SharedHostResourceLockDir string
	ResetCheckpointPath       string
	OciStoragePolicyPath      string
	SQLiteDatabaseRoot        string
	SharedHostOwnerInstance   string
	TenantID                  string
	ResourceRegistry          ResourceRegistry
}

func NewHostOperationsService(opts Options) *HostOperationsService {
	cfg := hostruntime.ResolveConfig(opts.ProviderID)
	rt := hostruntime.NewRuntime(cfg)
	toolsFn := opts.ToolsForProvider
	if toolsFn == nil {
		toolsFn = func(string) []string { return nil }
	}
	ownershipMode := strings.TrimSpace(opts.OwnershipMode)
	if ownershipMode != "enforce" {
		ownershipMode = "audit"
	}
	postgresRelayConfigDir := strings.TrimSpace(opts.RelayConfigDir)
	if postgresRelayConfigDir != "" {
		postgresRelayConfigDir = filepath.Join(postgresRelayConfigDir, "postgresql-service-relays")
	}
	tenantID := strings.TrimSpace(opts.TenantID)
	if tenantID == "" {
		tenantID = "local"
	}
	registry := opts.ResourceRegistry
	if registry == nil {
		registry = hostruntime.NewInMemoryResourceRegistry()
	}
	return &HostOperationsService{
		shared: hostruntime.Shared{
			Runtime:                 rt,
			TenantID:                tenantID,
			ResourceRegistry:        registry,
			InstanceID:              strings.TrimSpace(opts.InstanceID),
			AgentID:                 strings.TrimSpace(opts.AgentID),
			OwnershipMode:           ownershipMode,
			SharedHostOwnerInstance: strings.TrimSpace(opts.SharedHostOwnerInstance),
		},
		toolsFn:                toolsFn,
		resetCheckpointPath:    resolveResetCheckpointPath(opts.ResetCheckpointPath, opts.RelayConfigDir),
		ociStoragePolicyPath:   strings.TrimSpace(opts.OciStoragePolicyPath),
		sqliteDatabaseRoot:     strings.TrimSpace(opts.SQLiteDatabaseRoot),
		sqlSupervisor:          newSQLConnectorSupervisor(),
		guestBridgeRelay:       newTCPRelayManager(),
		relayDirs:              [2]string{opts.RelayConfigDir, opts.SharedHostResourceLockDir},
		postgresqlServiceRelay: newPersistentPostgreSQLServiceRelayManagerAt(postgresRelayConfigDir),
	}
}

func (s *HostOperationsService) TenantID() string {
	if s == nil {
		return ""
	}
	return s.shared.TenantID
}

func (s *HostOperationsService) effectiveTenantID() string {
	if s == nil {
		return "local"
	}
	return s.shared.EffectiveTenantID()
}

func resolveResetCheckpointPath(explicitPath, relayConfigDir string) string {
	if path := strings.TrimSpace(explicitPath); path != "" {
		return path
	}
	if dir := strings.TrimSpace(relayConfigDir); dir != "" {
		return filepath.Join(dir, "incus-reset-checkpoint.json")
	}
	return ""
}

func (s *HostOperationsService) ReadProviderID() string {
	return string(s.shared.Runtime.ReadProviderID())
}

// SetResourceSnapshot connects host-local admission telemetry to direct
// diagnostics such as get_host_info and get_local_status.
func (s *HostOperationsService) SetResourceSnapshot(snapshot func() map[string]any) {
	s.shared.ResourceSnapshot = snapshot
}

func (s *HostOperationsService) DescribeHost() HostInfoResult {
	pid := s.ReadProviderID()
	host, _ := os.Hostname()
	result := HostInfoResult{
		HostName:       host,
		ProviderID:     pid,
		LXCBinaryPath:  s.shared.Runtime.ProviderBinary(),
		SystemctlPath:  hostruntime.DefaultSystemctlPath,
		SupportedTools: s.toolsFn(pid),
	}
	if uri, err := resourceid.HostURI(s.shared.TenantID, firstNonEmpty(s.shared.AgentID, host)); err == nil {
		result.URI = uri.String()
		if s.shared.ResourceRegistry != nil {
			_ = s.RegisterResource(result.URI, map[string]any{"agentId": s.shared.AgentID, "hostName": host})
		}
	}
	if capacity, err := s.VMInventoryCapacity(); err == nil {
		result.Capacity = &capacity
	}
	result.System = heartbeat.ReadHostSystemMetadata()
	if s.shared.ResourceSnapshot != nil {
		if result.System == nil {
			result.System = map[string]any{}
		}
		result.System["resourceAdmission"] = s.shared.ResourceSnapshot()
	}
	return result
}

func (s *HostOperationsService) waitForVMExecReady(vmName string, timeout time.Duration, onData func(string)) error {
	return s.waitForIncusAgent(vmName, timeout, onData)
}

func (s *HostOperationsService) RunAgentShell(command string, onData func(string)) (hostexec.Result, error) {
	return s.RunAgentShellWithTimeout(command, 0, onData)
}

// RunAgentShellWithTimeout runs a caller-declared host command with an
// explicit bounded execution budget. A zero timeout preserves the command
// runner's no-deadline behavior for internal lifecycle calls; externally
// dispatched commands should provide a positive timeout so the caller's
// lifecycle has a finite, observable boundary.
func (s *HostOperationsService) RunAgentShellWithTimeout(command string, timeout time.Duration, onData func(string)) (hostexec.Result, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return hostexec.Result{}, errors.New("command is required")
	}
	return s.shared.Runtime.RunHost([]string{"bash", "-lc", command}, onData, timeout)
}

// NewVMInteractiveCommand is the ownership-checked command factory for the
// host worker PTY stream. Interactive lifecycle is kept in console.Runtime;
// this service remains the single Incus ownership boundary.
func (s *HostOperationsService) NewVMInteractiveCommand(vmName string) (*exec.Cmd, error) {
	if err := s.assertIncusOwnership(vmName, "console"); err != nil {
		return nil, err
	}
	return s.shared.Runtime.NewVMInteractiveCommand(vmName)
}

func (s *HostOperationsService) commandRunner(args []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.shared.CommandRunner(args, onData, timeout)
}

func (s *HostOperationsService) hostCommandRunner(command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.shared.HostCommandRunner(command, onData, timeout)
}

func (s *HostOperationsService) hostCommandRunnerContext(ctx context.Context, command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.shared.HostCommandRunnerContext(ctx, command, onData, timeout)
}

func (s *HostOperationsService) vmExecArgv(vmName string, guestArgv []string) []string {
	return s.shared.VMExecArgv(vmName, guestArgv)
}

func (s *HostOperationsService) runVMExec(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if err := s.assertIncusOwnership(vmName, "exec"); err != nil {
		return hostexec.Result{}, err
	}
	return s.commandRunner(s.vmExecArgv(vmName, guestArgv), onData, timeout)
}

func (s *HostOperationsService) runVMExecContext(ctx context.Context, vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if err := s.assertIncusOwnership(vmName, "exec"); err != nil {
		return hostexec.Result{}, err
	}
	return s.shared.Runtime.RunVMExecContext(ctx, vmName, guestArgv, onData, timeout)
}

func (s *HostOperationsService) runVMExecWithStdinContext(ctx context.Context, vmName string, guestArgv []string, input []byte, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if err := s.assertIncusOwnership(vmName, "exec"); err != nil {
		return hostexec.Result{}, err
	}
	return s.shared.Runtime.RunVMExecWithStdinContext(ctx, vmName, guestArgv, input, onData, timeout)
}

type VMListResult struct {
	VMs []VMInfo `json:"vms"`
}

type VMInfo struct {
	URI        string         `json:"uri"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Type       string         `json:"type,omitempty"`
	Status     string         `json:"status"`
	State      map[string]any `json:"state"`
	IPv4       []string       `json:"ipv4"`
	Release    string         `json:"release"`
	ProviderID string         `json:"providerId"`
	CPUs       *int           `json:"cpus,omitempty"`
	Memory     string         `json:"memory,omitempty"`
	Disk       string         `json:"disk,omitempty"`
	AgentReady *bool          `json:"agentReady,omitempty"`
	// HostId is the owning host agent identity (durable execution owner).
	HostId string `json:"hostId,omitempty"`
}

// --- VM lifecycle ---

type ProvisionVMArgs struct {
	VMName       string `json:"vmName"`
	Image        string `json:"image,omitempty"`
	CPUs         int    `json:"cpus,omitempty"`
	Memory       string `json:"memory,omitempty"`
	Disk         string `json:"disk,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
}

type VMStatusResult struct {
	URI          string `json:"uri"`
	VMName       string `json:"vmName"`
	Image        string `json:"image,omitempty"`
	Status       string `json:"status"`
	InstanceType string `json:"instanceType,omitempty"`
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
		cpus = defaultIncusVMCPUs
	}
	memory := strings.TrimSpace(args.Memory)
	if memory == "" {
		memory = defaultIncusVMMemory
	}
	requestedDisk := strings.TrimSpace(args.Disk)
	disk := requestedDisk
	if disk == "" {
		disk = defaultIncusVMRootDisk
	}
	quota, err := s.admitRootDiskQuota(disk, requestedDisk != "")
	if err != nil {
		return VMStatusResult{}, err
	}
	disk = quota.Size
	if disk == "" && onData != nil {
		onData(fmt.Sprintf("Provisioning %q without a root disk quota: %s", vmName, quota.Reason))
	}

	instanceType := normalizeProvisionInstanceType(args.InstanceType)
	if exists, err := s.incusInstanceExists(vmName); err == nil && exists {
		if err := s.assertIncusOwnership(vmName, "provision_vm"); err != nil {
			return VMStatusResult{}, err
		}
	}
	if instanceType == "virtual-machine" {
		if err := s.launchIncusVMViaAPI(vmName, image, cpus, memory, disk, onData, provisionVMTimeout); err != nil {
			return VMStatusResult{}, err
		}
		if image == "" {
			image = "images:ubuntu/22.04"
		} else {
			image = normalizeIncusLaunchImage(image)
		}
		return s.vmStatusResult(vmName, image, "running", "virtual-machine"), nil
	}

	nesting := true
	container, err := s.ProvisionContainer(ProvisionContainerArgs{
		ContainerName: vmName,
		Image:         image,
		CPUs:          cpus,
		Memory:        memory,
		Disk:          disk,
		Nesting:       &nesting,
	}, onData)
	if err != nil {
		return VMStatusResult{}, err
	}
	return s.vmStatusResult(container.ContainerName, container.Image, container.Status, container.InstanceType), nil
}

func (s *HostOperationsService) vmStatusResult(name, image, status, instanceType string) VMStatusResult {
	resourceType := resourceid.TypeContainer
	if instanceType == "virtual-machine" {
		resourceType = resourceid.TypeVM
	}
	result := VMStatusResult{VMName: name, Image: image, Status: status, InstanceType: instanceType}
	if uri, err := resourceid.New(resourceType, s.shared.TenantID, name); err == nil {
		result.URI = uri.String()
		if s.shared.ResourceRegistry != nil {
			_ = s.RegisterResource(result.URI, map[string]any{"providerInstanceName": name, "instanceType": instanceType})
		}
	}
	return result
}

type VMScopedArgs struct {
	VMName string `json:"vmName"`
}

func (s *HostOperationsService) StartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	if err := s.assertIncusOwnership(vmName, "start_vm"); err != nil {
		return nil, err
	}
	res, err := s.commandRunner([]string{"start", vmName}, onData, 0)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to start VM"))
	}
	out := map[string]string{"vmName": vmName, "status": "running"}
	if uri := s.ResourceURIForProviderName(vmName); uri != "" {
		out["uri"] = uri
	}
	return out, nil
}

func (s *HostOperationsService) StopVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	if err := s.assertIncusOwnership(vmName, "stop_vm"); err != nil {
		return nil, err
	}
	cmd := s.stopVMArgs(vmName)
	res, err := s.commandRunner(cmd, onData, 0)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to stop VM"))
	}
	out := map[string]string{"vmName": vmName, "status": "stopped"}
	if uri := s.ResourceURIForProviderName(vmName); uri != "" {
		out["uri"] = uri
	}
	return out, nil
}

func (s *HostOperationsService) RestartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	if err := s.assertIncusOwnership(vmName, "restart_vm"); err != nil {
		return nil, err
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
	out := map[string]string{"vmName": vmName, "status": "running"}
	if uri := s.ResourceURIForProviderName(vmName); uri != "" {
		out["uri"] = uri
	}
	return out, nil
}

// UpdateVMResourcesArgs selects the instance and the limits to apply. At least
// one of CPUs or Memory must be set; both are optional so callers can adjust
// a single limit without touching the other.
type UpdateVMResourcesArgs struct {
	VMName string `json:"vmName"`
	CPUs   int    `json:"cpus,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// UpdateVMResources applies limits.cpu / limits.memory to an existing VM or
// system container. CPU and memory limits are live-applied by Incus for
// containers; QEMU guests pick them up on the next start.
func (s *HostOperationsService) UpdateVMResources(args UpdateVMResourcesArgs, onData func(string)) (map[string]string, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	memory := strings.TrimSpace(args.Memory)
	if args.CPUs <= 0 && memory == "" {
		return nil, errors.New("at least one of cpus or memory is required")
	}
	if err := s.assertIncusOwnership(vmName, "update_vm_resources"); err != nil {
		return nil, err
	}
	applied := map[string]string{"vmName": vmName, "status": "updated"}
	if uri := s.ResourceURIForProviderName(vmName); uri != "" {
		applied["uri"] = uri
	}
	if args.CPUs > 0 {
		if err := s.setIncusInstanceConfig(vmName, "limits.cpu", strconv.Itoa(args.CPUs)); err != nil {
			return nil, fmt.Errorf("set CPU limit: %w", err)
		}
		applied["cpus"] = strconv.Itoa(args.CPUs)
	}
	if memory != "" {
		if err := s.setIncusInstanceConfig(vmName, "limits.memory", memory); err != nil {
			return nil, fmt.Errorf("set memory limit: %w", err)
		}
		applied["memory"] = memory
	}
	return applied, nil
}

func (s *HostOperationsService) DeleteVM(args VMScopedArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	if err := s.assertIncusOwnership(vmName, "delete_vm"); err != nil {
		return nil, err
	}
	res, err := s.commandRunner(s.deleteVMArgs(vmName), onData, 0)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "failed to delete VM"))
	}
	uri := s.ResourceURIForProviderName(vmName)
	if uri != "" {
		_ = s.DeregisterResource(uri)
	}
	out := map[string]any{"vmName": vmName, "deleted": true}
	if uri != "" {
		out["uri"] = uri
	}
	return out, nil
}

func (s *HostOperationsService) stopVMArgs(vmName string) []string {
	return []string{"stop", vmName, "--force"}
}

func (s *HostOperationsService) deleteVMArgs(vmName string) []string {
	return []string{"delete", vmName, "--force"}
}

// --- Kubernetes inventory ---

type k8sMeta struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	UID               string `json:"uid"`
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

func (s *HostOperationsService) ListIngressClasses(vmName string) ([]string, error) {
	data, err := s.getKubernetesList(vmName, "ingressclasses", "")
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

func (s *HostOperationsService) ListServices(vmName, namespace string) ([]map[string]any, error) {
	data, err := s.getKubernetesList(vmName, "services", namespace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		row := map[string]any{
			"name":      meta["name"].(string),
			"namespace": meta["namespace"].(string),
		}
		if uri, err := resourceid.ServiceURI(s.shared.TenantID, vmName+"/"+meta["namespace"].(string)+"/"+meta["name"].(string)); err == nil {
			row["uri"] = uri.String()
			if s.shared.ResourceRegistry != nil {
				_ = s.RegisterResource(uri.String(), map[string]any{
					"providerInstanceName": vmName,
					"namespace":            meta["namespace"].(string),
					"serviceName":          meta["name"].(string),
				})
			}
		}
		out = append(out, row)
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
		if strings.TrimSpace(item.Metadata.UID) == "" {
			return nil, fmt.Errorf("Kubernetes pod %q/%q has no metadata.uid; refusing to issue an unstable pod URI", item.Metadata.Namespace, item.Metadata.Name)
		}
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
			"kind":      resourceid.TypePod,
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
		if item.Metadata.UID != "" {
			resourceID := vmName + "/" + item.Metadata.Namespace + "/" + item.Metadata.Name + "/" + item.Metadata.UID
			if uri, uriErr := resourceid.PodURI(s.shared.TenantID, resourceID); uriErr == nil {
				row["uri"] = uri.String()
				if s.shared.ResourceRegistry != nil {
					_ = s.RegisterResource(uri.String(), map[string]any{
						"providerInstanceName": vmName,
						"namespace":            item.Metadata.Namespace,
						"podName":              item.Metadata.Name,
						"uid":                  item.Metadata.UID,
						"clusterResource":      vmName,
					})
				}
			}
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

type SetHostServiceStateArgs struct {
	ServiceName string `json:"serviceName"`
	State       string `json:"state"`
	Scope       string `json:"scope,omitempty"`
}

// EnsureHostServiceSupervisorArgs describes the lifecycle contract required by
// a caller-owned host service. It is deliberately independent of any product,
// service name, URL, or runtime: user-scoped services need a persistent
// systemd user manager, while system-scoped services only need the system
// manager to be reachable.
type EnsureHostServiceSupervisorArgs struct {
	Scope string `json:"scope,omitempty"`
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
		// service: waiting for systemd to stop this process would close the MCP
		// operation before the caller can receive its result.
		return []string{hostruntime.DefaultSystemctlPath, "--user", "--no-block", "restart", serviceName}
	}
	return []string{hostruntime.DefaultSystemctlPath, "restart", serviceName}
}

func serviceStatusCommand(serviceName string) []string {
	if strings.HasPrefix(serviceName, "opute-") {
		return []string{hostruntime.DefaultSystemctlPath, "--user", "is-active", serviceName}
	}
	return []string{hostruntime.DefaultSystemctlPath, "is-active", serviceName}
}

func serviceStateUnit(serviceName string) string {
	return "host-service-state-" + strings.NewReplacer("/", "-", ":", "-", "@", "-", ".", "-").Replace(serviceName)
}

func serviceStateCommand(serviceName, state, scope string) []string {
	if state == "restart" && scope == "user" {
		// A user-systemd transient job is outside the target service's cgroup.
		// This matters when the target is the MCP process serving this request:
		// systemctl can enqueue the restart and return a truthful scheduled
		// result before the current agent is stopped.
		return []string{
			hostruntime.DefaultSystemdRunPath,
			"--user",
			"--unit=" + serviceStateUnit(serviceName),
			"--collect",
			"--no-block",
			hostruntime.DefaultSystemctlPath,
			"--user",
			"--no-block",
			"restart",
			serviceName,
		}
	}
	command := []string{hostruntime.DefaultSystemctlPath}
	if scope == "user" {
		command = append(command, "--user")
	} else {
		command = append([]string{"sudo", "-n"}, command...)
	}
	if state == "restart" {
		command = append(command, "--no-block")
	}
	return append(command, state, serviceName)
}

func (s *HostOperationsService) RestartHostService(args RestartHostServiceArgs, onData func(string)) (map[string]string, error) {
	if err := s.requireSharedHostOwner("restart_host_service"); err != nil {
		return nil, err
	}
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

// SetHostServiceState provides the generic, approval-gated service lifecycle
// primitive used by recovery workflows. User scope is the safe default; system
// scope is explicit and uses non-interactive sudo so MCP cannot hang on a
// password prompt.
func (s *HostOperationsService) SetHostServiceState(args SetHostServiceStateArgs, onData func(string)) (map[string]any, error) {
	if err := s.requireSharedHostOwner("set_host_service_state"); err != nil {
		return nil, err
	}
	serviceName := strings.TrimSpace(args.ServiceName)
	state := strings.ToLower(strings.TrimSpace(args.State))
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if serviceName == "" || !safeSystemdUnitName.MatchString(serviceName) {
		return nil, errors.New("serviceName is required and must be a valid systemd unit name")
	}
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return nil, errors.New("scope must be user or system")
	}
	if state != "start" && state != "stop" && state != "restart" && state != "enable" && state != "disable" {
		return nil, errors.New("state must be start, stop, restart, enable, or disable")
	}
	// Recipes commonly reconcile a unit file immediately before changing its
	// state. Reload the matching manager so systemd cannot act on a stale unit
	// definition (notably after an ExecStart or environment change).
	reloadCommand := []string{hostruntime.DefaultSystemctlPath}
	if scope == "user" {
		reloadCommand = append(reloadCommand, "--user", "daemon-reload")
	} else {
		reloadCommand = append([]string{"sudo", "-n"}, reloadCommand...)
		reloadCommand = append(reloadCommand, "daemon-reload")
	}
	if reload, reloadErr := s.hostCommandRunner(reloadCommand, onData, 0); reloadErr != nil || reload.ExitCode != 0 {
		return nil, fmt.Errorf("service manager reload failed: %s", firstNonEmpty(reload.Stderr, reload.Stdout, "command failed"))
	}
	command := serviceStateCommand(serviceName, state, scope)
	result, err := s.hostCommandRunner(command, onData, 0)
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("service state change failed: %s", firstNonEmpty(result.Stderr, result.Stdout, "command failed"))
	}
	status := "applied"
	if state == "restart" {
		status = "scheduled"
	}
	return map[string]any{"serviceName": serviceName, "state": state, "scope": scope, "status": status}, nil
}

// EnsureHostServiceSupervisor makes the host service lifecycle explicit. WSL
// and other session-based Linux environments otherwise terminate a user
// manager as soon as the last non-interactive session exits, taking every
// caller-owned service and its listeners with it. The operation is idempotent
// and reports observed supervisor state rather than claiming service health.
func (s *HostOperationsService) EnsureHostServiceSupervisor(args EnsureHostServiceSupervisorArgs, onData func(string)) (map[string]any, error) {
	if err := s.requireSharedHostOwner("ensure_host_service_supervisor"); err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return nil, errors.New("scope must be user or system")
	}
	if scope == "system" {
		result, err := s.hostCommandRunner([]string{hostruntime.DefaultSystemctlPath, "is-system-running"}, onData, 10*time.Second)
		if err != nil || (result.ExitCode != 0 && strings.TrimSpace(result.Stdout) == "") {
			return nil, fmt.Errorf("system service supervisor is unavailable: %s", firstNonEmpty(result.Stderr, result.Stdout, "systemctl failed"))
		}
		return map[string]any{"scope": scope, "status": "ready", "persistent": true, "state": strings.TrimSpace(result.Stdout)}, nil
	}
	user := strings.TrimSpace(os.Getenv("USER"))
	if user == "" {
		identity, err := osuser.Current()
		if err != nil {
			return nil, fmt.Errorf("resolve host service user: %w", err)
		}
		user = identity.Username
	}
	if user == "" || strings.ContainsAny(user, "\r\n") {
		return nil, errors.New("resolve host service user: invalid username")
	}
	command := []string{"loginctl", "enable-linger", user}
	result, err := s.hostCommandRunner(command, onData, 15*time.Second)
	if err != nil || result.ExitCode != 0 {
		// A non-root host agent may have a narrowly scoped sudo policy prepared
		// by the bootstrap installer. Never fall back to an interactive prompt.
		result, err = s.hostCommandRunner([]string{"sudo", "-n", "loginctl", "enable-linger", user}, onData, 15*time.Second)
	}
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("enable persistent user service supervisor: %s", firstNonEmpty(result.Stderr, result.Stdout, "loginctl failed"))
	}
	observed, err := s.hostCommandRunner([]string{"loginctl", "show-user", user, "-p", "Linger"}, onData, 15*time.Second)
	if err != nil || observed.ExitCode != 0 || !strings.Contains(observed.Stdout, "Linger=yes") {
		return nil, fmt.Errorf("verify persistent user service supervisor: %s", firstNonEmpty(observed.Stderr, observed.Stdout, "Linger=yes was not observed"))
	}
	bus, err := s.hostCommandRunner([]string{hostruntime.DefaultSystemctlPath, "--user", "show-environment"}, onData, 15*time.Second)
	if err != nil || bus.ExitCode != 0 {
		return nil, fmt.Errorf("user service supervisor bus is unavailable: %s", firstNonEmpty(bus.Stderr, bus.Stdout, "systemctl --user failed"))
	}
	return map[string]any{"scope": scope, "status": "ready", "persistent": true, "user": user, "linger": true, "userBus": true}, nil
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
	if p := strings.TrimSpace(os.Getenv("PLATFORM_MCP_PORT")); p != "" {
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
		res, err := s.hostCommandRunner([]string{hostruntime.DefaultSystemctlPath, "is-active", service}, onData, 0)
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
		res, err := s.runVMExec(vmName, []string{hostruntime.DefaultSystemctlPath, "is-active", service}, onData, 30*time.Second)
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "active" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("VM service '%s' on '%s' did not become active within %s", service, vmName, timeout)
}

func shellEscape(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// These forward to internal/textutil, which every domain shares. The local
// spellings stay so the ~76 call sites in this package do not churn while it is
// being dismantled.
var (
	firstNonEmpty = textutil.FirstNonEmpty
	defaultString = textutil.Default
	errString     = textutil.ErrString
)

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func resolveBridgeURLFromEnv() string {
	for _, key := range []string{"OPUTE_PLATFORM_PUBLIC_URL", "OPUTE_PLATFORM_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	port := envOr("PLATFORM_MCP_PORT", "9093")
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
	activeMu   *sync.Mutex
	active     map[net.Conn]struct{}
}

func (s *relaySession) track(conn net.Conn) {
	s.activeMu.Lock()
	s.active[conn] = struct{}{}
	s.activeMu.Unlock()
}

func (s *relaySession) untrack(conn net.Conn) {
	s.activeMu.Lock()
	delete(s.active, conn)
	s.activeMu.Unlock()
}

func (s *relaySession) activeConnections() []net.Conn {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	connections := make([]net.Conn, 0, len(s.active))
	for conn := range s.active {
		connections = append(connections, conn)
	}
	return connections
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
		activeMu:   &sync.Mutex{},
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
	session.track(client)
	session.track(upstream)

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
		session.untrack(client)
		session.untrack(upstream)
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

	for _, conn := range session.activeConnections() {
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
