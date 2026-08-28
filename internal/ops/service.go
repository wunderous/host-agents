package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/domain/llm"
	"github.com/wunderous/host-agents/internal/domain/oci"
	"github.com/wunderous/host-agents/internal/domain/postgres"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/tcprelay"
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

	// oci is the oci domain, built lazily -- see oci_delegate.go. It holds the
	// container storage policy lock, so it is one instance per service.
	ociSvc  *oci.Service
	ociOnce sync.Once

	guestBridgeRelay *tcprelay.Manager
	// llm is the llm domain, built lazily -- see llm_delegate.go. It owns live
	// relay listeners, so it is one instance per service.
	llmSvc    *llm.Service
	llmOnce   sync.Once
	relayDirs [2]string
	// postgres is the postgres domain, built lazily -- see postgres_delegate.go.
	// It owns live relay listeners, so it is one instance per service.
	postgresSvc            *postgres.Service
	postgresOnce           sync.Once
	postgresRelayConfigDir string
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
		guestBridgeRelay:       tcprelay.New(),
		relayDirs:              [2]string{opts.RelayConfigDir, opts.SharedHostResourceLockDir},
		postgresRelayConfigDir: postgresRelayConfigDir,
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

func (s *HostOperationsService) waitForVMExecReady(vmName string, timeout time.Duration, onData func(string)) error {
	return s.waitForIncusAgent(vmName, timeout, onData)
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

// --- Bridge diagnostics ---

// --- helpers ---

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

// These forward to internal/textutil, which every domain shares. The local
// spellings stay so the ~76 call sites in this package do not churn while it is
// being dismantled.
var (
	firstNonEmpty = textutil.FirstNonEmpty
	defaultString = textutil.Default
	errString     = textutil.ErrString
)

func resolveBridgeURLFromEnv() string {
	for _, key := range []string{"OPUTE_PLATFORM_PUBLIC_URL", "OPUTE_PLATFORM_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	port := hostruntime.EnvOr("PLATFORM_MCP_PORT", "9093")
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
