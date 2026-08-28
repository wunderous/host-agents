package ops

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/domain/cluster"
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/domain/llm"
	"github.com/wunderous/host-agents/internal/domain/oci"
	"github.com/wunderous/host-agents/internal/domain/postgres"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/textutil"
)

const (
	provisionVMTimeout     = 10 * time.Minute
	sqlConnectorMaxPerHost = 32
	sqlConnectorIdleDrain  = 120 * time.Second
)

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

	// cluster is the cluster domain, built lazily -- see cluster_delegate.go. It
	// owns the guest bridge relay listener, so it is one instance per service.
	clusterSvc  *cluster.Service
	clusterOnce sync.Once
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
	errString     = textutil.ErrString
)

// VMInfo lives in the vminfo contract package: incus produces it and cluster
// and host read it. The alias keeps the dispatch layer unchanged.
type VMInfo = vminfo.VMInfo
