package incus

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/textutil"

	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/resourceid"
)

const provisionVMTimeout = 10 * time.Minute

func (s *Service) WaitForVMExecReady(vmName string, timeout time.Duration, onData func(string)) error {
	return s.waitForIncusAgent(vmName, timeout, onData)
}

// NewVMInteractiveCommand is the ownership-checked command factory for the
// host worker PTY stream. Interactive lifecycle is kept in console.Runtime;
// this service remains the single Incus ownership boundary.
func (s *Service) NewVMInteractiveCommand(vmName string) (*exec.Cmd, error) {
	if err := s.assertIncusOwnership(vmName, "console"); err != nil {
		return nil, err
	}
	return s.shared.Runtime.NewVMInteractiveCommand(vmName)
}

func (s *Service) commandRunner(args []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.shared.CommandRunner(args, onData, timeout)
}

func (s *Service) vmExecArgv(vmName string, guestArgv []string) []string {
	return s.shared.VMExecArgv(vmName, guestArgv)
}

func (s *Service) RunVMExec(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.RunVMExecContext(context.Background(), vmName, guestArgv, onData, timeout)
}

// RunVMExecContext is the ownership-checked, cancellation-aware guest
// execution boundary used by request-scoped host tools.
func (s *Service) RunVMExecContext(ctx context.Context, vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if err := s.assertIncusOwnership(vmName, "exec"); err != nil {
		return hostexec.Result{}, err
	}
	return s.shared.CommandRunnerContext(ctx, s.vmExecArgv(vmName, guestArgv), onData, timeout)
}

// RunVMExecWithStdin executes an ownership-checked guest command while keeping
// transient input off the provider argv. Providers use this for enrollment
// material that must not appear in process listings or task metadata.
func (s *Service) RunVMExecWithStdin(vmName string, guestArgv []string, input []byte, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.RunVMExecWithStdinContext(context.Background(), vmName, guestArgv, input, onData, timeout)
}

// RunVMExecWithStdinContext is the cancellation-aware secret-safe guest
// execution boundary.
func (s *Service) RunVMExecWithStdinContext(ctx context.Context, vmName string, guestArgv []string, input []byte, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if err := s.assertIncusOwnership(vmName, "exec"); err != nil {
		return hostexec.Result{}, err
	}
	return s.shared.Runtime.RunVMExecWithStdinContext(ctx, vmName, guestArgv, input, onData, timeout)
}

type VMListResult struct {
	VMs []vminfo.VMInfo `json:"vms"`
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

func (s *Service) CreateVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
	return s.provisionVM(args, onData)
}

func (s *Service) ProvisionVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
	return s.provisionVM(args, onData)
}

func (s *Service) provisionVM(args ProvisionVMArgs, onData func(string)) (VMStatusResult, error) {
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
	instanceType, explicitType, err := parseProvisionInstanceType(args.InstanceType)
	if err != nil {
		return VMStatusResult{}, err
	}
	existing, err := s.incusInstanceExists(vmName)
	if err != nil {
		return VMStatusResult{}, fmt.Errorf("inspect existing Incus instance: %w", err)
	}
	if existing {
		if err := s.assertIncusOwnership(vmName, "provision_vm"); err != nil {
			return VMStatusResult{}, err
		}
		observed, err := s.ReadInstanceType(vmName)
		if err != nil {
			return VMStatusResult{}, fmt.Errorf("validate existing Incus runtime kind: %w", err)
		}
		actualKind, err := normalizeObservedInstanceType(observed)
		if err != nil {
			runtimeErr := err.(*IncusRuntimeKindError)
			runtimeErr.VMName = vmName
			return VMStatusResult{}, runtimeErr
		}
		if explicitType && actualKind != instanceType {
			return VMStatusResult{}, &IncusRuntimeKindError{
				Code: "incus_runtime_kind_mismatch", VMName: vmName,
				Requested: instanceType, Observed: actualKind,
				Remediation: "Use the existing instance runtime kind or delete it through the approved lifecycle operation before reprovisioning.",
			}
		}
		// An omitted kind reuses the observed runtime kind. This is an
		// idempotent status/start operation, never a create or resize.
		instanceType = actualKind
		if instanceType == "virtual-machine" {
			if err := s.startExistingInstance(vmName, onData); err != nil {
				return VMStatusResult{}, err
			}
			return s.vmStatusResult(vmName, image, "running", instanceType), nil
		}
		container, err := s.ProvisionContainer(ProvisionContainerArgs{
			ContainerName: vmName,
			Image:         image,
			CPUs:          cpus,
			Memory:        memory,
			Disk:          args.Disk,
			Nesting:       boolPointer(true),
		}, onData)
		if err != nil {
			return VMStatusResult{}, err
		}
		return s.vmStatusResult(container.ContainerName, container.Image, container.Status, container.InstanceType), nil
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
	if instanceType == "virtual-machine" {
		if err := s.enforceIncusAggregateBudget(vmName, cpus, memory, disk); err != nil {
			return VMStatusResult{}, err
		}
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

func boolPointer(value bool) *bool { return &value }

func (s *Service) startExistingInstance(name string, onData func(string)) error {
	started, err := s.commandRunner([]string{"start", name}, onData, 2*time.Minute)
	if err != nil || (started.ExitCode != 0 && !strings.Contains(strings.ToLower(textutil.FirstNonEmpty(started.Stderr, started.Stdout)), "already running")) {
		return fmt.Errorf("start existing Incus instance: %s", textutil.FirstNonEmpty(started.Stderr, started.Stdout, textutil.ErrString(err, "incus start failed")))
	}
	if err := s.ensureIncusInstanceAutostart(name); err != nil {
		return err
	}
	return nil
}

// enforceIncusAggregateBudget rejects a new allocation when declared Incus
// limits already consume the effective host budget. Existing instances are
// observations here; this check never resizes or mutates them.
func (s *Service) enforceIncusAggregateBudget(vmName string, cpus int, memory, disk string) error {
	if s.resourceService == nil {
		return nil
	}
	snapshot := s.resourceService.Snapshot()
	if snapshot.Enforcement != resource.EnforcementEnforced {
		return &resource.AdmissionError{
			Code:         "host_resource_enforcement_unknown",
			Class:        resource.ClassHeavy,
			Pressure:     snapshot.Pressure,
			Reason:       "Incus workload launch requires verified host cgroup enforcement",
			RetryAfterMs: 1000,
		}
	}
	capacity, err := s.VMInventoryCapacity()
	if err != nil {
		return fmt.Errorf("observe Incus allocation before launch: %w", err)
	}
	limits := snapshot.EffectiveLimits
	if limits.CPUCores > 0 && float64(capacity.TotalVMCPULimitCores+cpus) > limits.CPUCores {
		return incusAggregateAdmissionError(vmName, "CPU allocation exceeds effective host capacity")
	}
	requestedMemory := parseCapacityBytes(memory)
	if limits.MemoryBytes > 0 && requestedMemory > 0 && capacity.TotalVMMemoryLimitBytes+requestedMemory > limits.MemoryBytes {
		return incusAggregateAdmissionError(vmName, "memory allocation exceeds effective host capacity")
	}
	requestedDisk := parseCapacityBytes(disk)
	if limits.DiskBytes > 0 && requestedDisk > 0 && capacity.TotalVMDiskLimitBytes+requestedDisk > limits.DiskBytes {
		return incusAggregateAdmissionError(vmName, "disk allocation exceeds effective host capacity")
	}
	return nil
}

func incusAggregateAdmissionError(vmName, reason string) error {
	return &resource.AdmissionError{
		Code:         "host_capacity_saturated",
		Class:        resource.ClassHeavy,
		Pressure:     "normal",
		Reason:       fmt.Sprintf("cannot launch Incus instance %q: %s", vmName, reason),
		RetryAfterMs: 1000,
	}
}

func (s *Service) vmStatusResult(name, image, status, instanceType string) VMStatusResult {
	resourceType := resourceid.TypeContainer
	if instanceType == "virtual-machine" {
		resourceType = resourceid.TypeVM
	}
	result := VMStatusResult{VMName: name, Image: image, Status: status, InstanceType: instanceType}
	if uri, err := resourceid.New(resourceType, s.shared.TenantID, name); err == nil {
		result.URI = uri.String()
		if s.shared.ResourceRegistry != nil {
			_ = s.shared.RegisterResource(result.URI, map[string]any{"providerInstanceName": name, "instanceType": instanceType})
		}
	}
	return result
}

type VMScopedArgs struct {
	VMName string `json:"vmName"`
}

func (s *Service) StartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
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
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "failed to start VM"))
	}
	out := map[string]string{"vmName": vmName, "status": "running"}
	if uri := s.shared.ResourceURIForProviderName(vmName); uri != "" {
		out["uri"] = uri
	}
	return out, nil
}

func (s *Service) StopVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
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
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "failed to stop VM"))
	}
	out := map[string]string{"vmName": vmName, "status": "stopped"}
	if uri := s.shared.ResourceURIForProviderName(vmName); uri != "" {
		out["uri"] = uri
	}
	return out, nil
}

func (s *Service) RestartVM(args VMScopedArgs, onData func(string)) (map[string]string, error) {
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
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(stop.Stderr, stop.Stdout, "failed to stop VM during restart"))
	}
	start, err := s.commandRunner([]string{"start", vmName}, onData, 0)
	if err != nil {
		return nil, err
	}
	if start.ExitCode != 0 {
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(start.Stderr, start.Stdout, "failed to start VM during restart"))
	}
	out := map[string]string{"vmName": vmName, "status": "running"}
	if uri := s.shared.ResourceURIForProviderName(vmName); uri != "" {
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
func (s *Service) UpdateVMResources(args UpdateVMResourcesArgs, onData func(string)) (map[string]string, error) {
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
	if uri := s.shared.ResourceURIForProviderName(vmName); uri != "" {
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

func (s *Service) DeleteVM(args VMScopedArgs, onData func(string)) (map[string]any, error) {
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
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "failed to delete VM"))
	}
	uri := s.shared.ResourceURIForProviderName(vmName)
	if uri != "" {
		_ = s.shared.DeregisterResource(uri)
	}
	out := map[string]any{"vmName": vmName, "deleted": true}
	if uri != "" {
		out["uri"] = uri
	}
	return out, nil
}

func (s *Service) stopVMArgs(vmName string) []string {
	return []string{"stop", vmName, "--force"}
}

func (s *Service) deleteVMArgs(vmName string) []string {
	return []string{"delete", vmName, "--force"}
}

// --- Kubernetes inventory ---

func (s *Service) WaitForVMServiceActive(vmName, service string, onData func(string), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := s.RunVMExec(vmName, []string{hostruntime.DefaultSystemctlPath, "is-active", service}, onData, 30*time.Second)
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "active" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("VM service '%s' on '%s' did not become active within %s", service, vmName, timeout)
}
