package incus

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/fsutil"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/textutil"
)

const (
	wslGpuLibHostPath  = "/usr/lib/wsl/lib"
	wslGpuLibGuestPath = "/usr/lib/wsl/lib"
)

// ProvisionContainerArgs describes a persistent Incus system container.
type ProvisionContainerArgs struct {
	ContainerName string `json:"containerName"`
	Image         string `json:"image,omitempty"`
	CPUs          int    `json:"cpus,omitempty"`
	Memory        string `json:"memory,omitempty"`
	Disk          string `json:"disk,omitempty"`
	GPU           bool   `json:"gpu,omitempty"`
	WSLGpuLibs    bool   `json:"wslGpuLibs,omitempty"`
	Nesting       *bool  `json:"nesting,omitempty"`
	Port          int    `json:"port,omitempty"`
	ModelVolume   string `json:"modelVolume,omitempty"`
	// A cluster member's address is part of its identity: k3s pins --node-ip,
	// --advertise-address and the etcd peer URLs to it. A DHCP lease does not
	// survive the managed bridge being recreated, so callers that build
	// clusters pin the address here instead.
	IPv4Address string `json:"ipv4Address,omitempty"`
}

type ContainerStatusResult struct {
	URI           string `json:"uri"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image,omitempty"`
	Status        string `json:"status"`
	InstanceType  string `json:"instanceType"`
}

// Overridable so tests can exercise the polling loop without waiting on it.
var (
	containerAddressTimeout      = 3 * time.Minute
	containerAddressPollInterval = 3 * time.Second
)

func (s *Service) ProvisionContainer(args ProvisionContainerArgs, onData func(string)) (ContainerStatusResult, error) {
	name := strings.TrimSpace(args.ContainerName)
	if name == "" {
		return ContainerStatusResult{}, errors.New("containerName is required")
	}
	if runtime.GOOS != "linux" {
		return ContainerStatusResult{}, fmt.Errorf("provision_container is unsupported on %s host agents", runtime.GOOS)
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return ContainerStatusResult{}, errors.New("containerName may not contain whitespace")
	}
	image := normalizeIncusLaunchImage(strings.TrimSpace(args.Image))
	if image == "" {
		image = "images:ubuntu/24.04"
	}
	nesting := true
	if args.Nesting != nil {
		nesting = *args.Nesting
	}
	existing, err := s.incusInstanceExists(name)
	if err != nil {
		return ContainerStatusResult{}, fmt.Errorf("inspect existing Incus instance: %w", err)
	}
	if existing {
		if err := s.assertIncusOwnership(name, "provision_container"); err != nil {
			return ContainerStatusResult{}, err
		}
		observed, err := s.ReadInstanceType(name)
		if err != nil {
			return ContainerStatusResult{}, fmt.Errorf("validate existing Incus runtime kind: %w", err)
		}
		actualKind, err := normalizeObservedInstanceType(observed)
		if err != nil {
			runtimeErr := err.(*IncusRuntimeKindError)
			runtimeErr.VMName = name
			return ContainerStatusResult{}, runtimeErr
		}
		if actualKind != "container" {
			return ContainerStatusResult{}, &IncusRuntimeKindError{
				Code: "incus_runtime_kind_mismatch", VMName: name,
				Requested: "container", Observed: actualKind,
				Remediation: "Use the VM provisioning operation for a virtual-machine instance.",
			}
		}
		if onData != nil {
			onData(fmt.Sprintf("Reusing existing Incus container %q", name))
		}
		// Pin the address before the start so the container claims it from
		// this boot rather than keeping whatever the bridge last leased.
		deferredPin, _, pinErr := s.ensureContainerStaticIPv4(name, args.IPv4Address, onData)
		if pinErr != nil {
			return ContainerStatusResult{}, pinErr
		}
		started, startErr := s.commandRunner([]string{"start", name}, onData, 2*time.Minute)
		if startErr != nil || (started.ExitCode != 0 && !strings.Contains(strings.ToLower(textutil.FirstNonEmpty(started.Stderr, started.Stdout)), "already running")) {
			return ContainerStatusResult{}, fmt.Errorf("start existing container: %s", textutil.FirstNonEmpty(started.Stderr, started.Stdout, textutil.ErrString(startErr, "incus start failed")))
		}
		if err := s.ensureIncusInstanceAutostart(name); err != nil {
			return ContainerStatusResult{}, err
		}
		if nesting {
			if err := s.ensureContainerRuntimeDevices(name); err != nil {
				return ContainerStatusResult{}, err
			}
		}
		if args.GPU {
			if err := s.attachContainerGPUDevices(name, args.WSLGpuLibs, onData); err != nil {
				return ContainerStatusResult{}, err
			}
		}
		if args.Port > 0 {
			if err := s.ensureContainerHTTPProxy(name, args.Port, onData); err != nil {
				return ContainerStatusResult{}, err
			}
		}
		if args.ModelVolume != "" {
			if err := s.attachContainerModelVolume(name, args.ModelVolume, onData); err != nil {
				return ContainerStatusResult{}, err
			}
		}
		if err := s.awaitContainerIPv4(name, onData); err != nil {
			return ContainerStatusResult{}, err
		}
		if deferredPin {
			if err := s.applyGuestStaticIPv4(name, args.IPv4Address, onData); err != nil {
				return ContainerStatusResult{}, err
			}
		}
		return s.containerStatusResult(name, image, "running"), nil
	}
	requestedDisk := strings.TrimSpace(args.Disk)
	disk := requestedDisk
	if disk == "" {
		disk = defaultIncusContainerRootDisk
	}
	quota, quotaErr := s.admitRootDiskQuota(disk, requestedDisk != "")
	if quotaErr != nil {
		return ContainerStatusResult{}, quotaErr
	}
	disk = quota.Size
	if disk == "" && onData != nil {
		onData(fmt.Sprintf("Provisioning %q without a root disk quota: %s", name, quota.Reason))
	}
	cpus := args.CPUs
	if cpus <= 0 {
		cpus = defaultIncusVMCPUs
	}
	memory := strings.TrimSpace(args.Memory)
	if memory == "" {
		memory = defaultIncusVMMemory
	}
	if err := s.enforceIncusAggregateBudget(name, cpus, memory, disk); err != nil {
		return ContainerStatusResult{}, err
	}
	if err := s.launchIncusContainer(name, image, disk, cpus, memory, nesting, onData, 10*time.Minute); err != nil {
		return ContainerStatusResult{}, err
	}
	// A member built on a lease loses its identity the first time the bridge is
	// recreated, so a fresh container is pinned as deliberately as a reused one.
	launchedPin, pinChanged, pinErr := s.ensureContainerStaticIPv4(name, args.IPv4Address, onData)
	if pinErr != nil {
		_, _ = s.commandRunner(s.deleteVMArgs(name), onData, 2*time.Minute)
		return ContainerStatusResult{}, pinErr
	}
	if pinChanged {
		// The NIC address is claimed at boot, and this container already booted.
		if err := s.restartIncusInstanceIfRunning(name, onData); err != nil {
			return ContainerStatusResult{}, err
		}
	}
	if args.GPU {
		if err := s.attachContainerGPUDevices(name, args.WSLGpuLibs, onData); err != nil {
			_, _ = s.commandRunner(s.deleteVMArgs(name), onData, 2*time.Minute)
			return ContainerStatusResult{}, err
		}
	}
	if args.ModelVolume != "" {
		if err := s.attachContainerModelVolume(name, args.ModelVolume, onData); err != nil {
			_, _ = s.commandRunner(s.deleteVMArgs(name), onData, 2*time.Minute)
			return ContainerStatusResult{}, err
		}
	}
	if args.Port > 0 {
		if err := s.ensureContainerHTTPProxy(name, args.Port, onData); err != nil {
			_, _ = s.commandRunner(s.deleteVMArgs(name), onData, 2*time.Minute)
			return ContainerStatusResult{}, err
		}
	}
	if err := s.awaitContainerIPv4(name, onData); err != nil {
		return ContainerStatusResult{}, err
	}
	if launchedPin {
		if err := s.applyGuestStaticIPv4(name, args.IPv4Address, onData); err != nil {
			return ContainerStatusResult{}, err
		}
	}
	return s.containerStatusResult(name, image, "running"), nil
}

// awaitContainerIPv4 holds provisioning open until the guest has actually been
// given an address. incus reports a container as Running the moment its init
// starts, well before DHCP completes, so a caller that reads the instance
// straight after provisioning can legitimately observe an empty address list
// and only discover the gap much later, when whatever it wired that address
// into fails. Reporting a container as provisioned before it is addressable is
// what makes that failure remote from its cause.
func (s *Service) awaitContainerIPv4(name string, onData func(string)) error {
	deadline := time.Now().Add(containerAddressTimeout)
	var lastErr error
	for {
		addresses, err := s.readIncusInstanceIPv4(name)
		if err == nil && len(vminfo.NormalizeClusterIPv4(addresses)) > 0 {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		if onData != nil {
			onData(fmt.Sprintf("Waiting for container %q to obtain an IPv4 address...", name))
		}
		time.Sleep(containerAddressPollInterval)
	}
	if lastErr != nil {
		return fmt.Errorf("container %q did not obtain an IPv4 address within %s: %w", name, containerAddressTimeout, lastErr)
	}
	return fmt.Errorf("container %q did not obtain an IPv4 address within %s", name, containerAddressTimeout)
}

func (s *Service) containerStatusResult(name, image, status string) ContainerStatusResult {
	result := ContainerStatusResult{ContainerName: name, Image: image, Status: status, InstanceType: "container"}
	if uri, err := resourceid.ContainerURI(s.shared.TenantID, name); err == nil {
		result.URI = uri.String()
		if s.shared.ResourceRegistry != nil {
			_ = s.shared.RegisterResource(result.URI, map[string]any{
				"providerInstanceName": name,
				"displayName":          name,
				"instanceType":         "container",
			})
		}
	}
	return result
}

func (s *Service) incusInstanceExists(name string) (bool, error) {
	res, err := s.commandRunner([]string{"list", name, "--format", "csv"}, nil, 30*time.Second)
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("incus list %q: %s", name, textutil.FirstNonEmpty(res.Stderr, res.Stdout, "incus list failed"))
	}
	return strings.TrimSpace(res.Stdout) != "", nil
}

func (s *Service) launchIncusContainer(name, image, disk string, cpus int, memory string, nesting bool, onData func(string), timeout time.Duration) error {
	if onData != nil {
		onData(fmt.Sprintf("Launching Incus system container %q...", name))
	}
	if cpus <= 0 {
		cpus = defaultIncusVMCPUs
	}
	if strings.TrimSpace(memory) == "" {
		memory = defaultIncusVMMemory
	}
	launch := []string{"launch", image, name, "--config", "boot.autostart=true"}
	if owner := s.ownerConfigValue(); owner != "" {
		launch = append(launch, "--config", oputeIncusOwnerLabel+"="+owner)
		if agentID := s.ownerAgentConfigValue(); agentID != "" {
			launch = append(launch, "--config", oputeIncusAgentLabel+"="+agentID)
		}
	}
	if nesting {
		launch = append(launch, "--config", "security.nesting=true")
	}
	if cpus > 0 {
		launch = append(launch, "--config", fmt.Sprintf("limits.cpu=%d", cpus))
	}
	if memory != "" {
		launch = append(launch, "--config", "limits.memory="+memory)
	}
	if disk != "" {
		launch = append(launch, "--device", "root,size="+disk)
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	launched, err := s.commandRunner(launch, onData, timeout)
	if err != nil || launched.ExitCode != 0 {
		return fmt.Errorf("launch container: %s", textutil.FirstNonEmpty(launched.Stderr, launched.Stdout, textutil.ErrString(err, fmt.Sprintf("incus exited %d", launched.ExitCode))))
	}
	if nesting {
		if err := s.ensureContainerRuntimeDevices(name); err != nil {
			return err
		}
	}
	return s.ensureIncusInstanceAutostart(name)
}

func (s *Service) ensureIncusInstanceAutostart(name string) error {
	return s.setIncusInstanceConfig(name, "boot.autostart", "true")
}

func (s *Service) attachContainerGPUDevices(name string, wslLibs bool, onData func(string)) error {
	if onData != nil {
		onData("Attaching GPU devices to container...")
	}
	if fsutil.Exists("/dev/dxg") {
		if err := s.EnsureDevice(name, "dxg", []string{"config", "device", "add", name, "dxg", "unix-char", "source=/dev/dxg", "path=/dev/dxg"}); err != nil {
			return fmt.Errorf("attach /dev/dxg: %w", err)
		}
	}
	if wslLibs && fsutil.Exists(wslGpuLibHostPath) {
		if err := s.EnsureDevice(name, "wsl-gpu-libs", []string{
			"config", "device", "add", name, "wsl-gpu-libs", "disk",
			"source=" + wslGpuLibHostPath, "path=" + wslGpuLibGuestPath, "readonly=true",
		}); err != nil {
			return fmt.Errorf("attach WSL GPU libraries: %w", err)
		}
	}
	for _, candidate := range []struct{ host, guest string }{
		{"/usr/lib/wsl/lib/nvidia-smi", "/usr/local/bin/nvidia-smi"},
		{"/usr/lib/wsl/lib/nvidia-smi", "/usr/bin/nvidia-smi"},
	} {
		if fsutil.Exists(candidate.host) {
			_ = s.EnsureDevice(name, "nvidia-smi-bind", []string{
				"config", "device", "add", name, "nvidia-smi-bind", "disk",
				"source=" + candidate.host, "path=" + candidate.guest, "readonly=true",
			})
			break
		}
	}
	if err := s.EnsureDevice(name, "gpu", []string{"config", "device", "add", name, "gpu", "gpu", "gputype=physical"}); err != nil {
		return err
	}
	return s.restartIncusInstanceIfRunning(name, onData)
}

func evaluateSystemContainerGPUProbe(guestStdout string) (gpuOK bool, status string, blockers []string) {
	hasDxg := strings.Contains(guestStdout, "dxg=present")
	hasLibcuda := strings.Contains(guestStdout, "libcuda=present")
	cudaOK := strings.Contains(guestStdout, "cuda_init=ok")
	nvOK := strings.Contains(guestStdout, "nvidia_smi=ok")
	gpuOK = hasDxg && hasLibcuda && (cudaOK || nvOK)
	if gpuOK {
		return true, "ready_for_gpu_container", nil
	}
	if hasDxg && hasLibcuda && strings.Contains(guestStdout, "cuda_init=failed") &&
		(strings.Contains(guestStdout, "cuda_init_code=100") || strings.Contains(guestStdout, "cuInit returned 100")) {
		return false, "wsl_gpu_pv_not_nestable_in_lxc", []string{
			"WSL GPU-PV exposes /dev/dxg inside Incus system containers but CUDA init returns no device (cuInit 100). Run GPU-backed workloads on the WSL host namespace instead of nested LXC.",
		}
	}
	if hasDxg && hasLibcuda {
		return false, "system_container_gpu_blocked", []string{
			"WSL GPU devices are visible but neither CUDA init nor NVML succeeded inside the system container",
		}
	}
	return false, "system_container_gpu_blocked", []string{
		"GPU devices visible in WSL host but system container GPU init did not succeed",
	}
}

func (s *Service) EnsureDevice(instance, deviceName string, addArgs []string) error {
	if err := s.assertIncusOwnership(instance, "configure_instance_device"); err != nil {
		return err
	}
	show, err := s.commandRunner([]string{"config", "device", "show", instance}, nil, 30*time.Second)
	if err == nil && show.ExitCode == 0 && strings.Contains(show.Stdout, deviceName+":") {
		return nil
	}
	added, err := s.commandRunner(addArgs, nil, 2*time.Minute)
	if err != nil || added.ExitCode != 0 {
		detail := textutil.FirstNonEmpty(added.Stderr, added.Stdout, textutil.ErrString(err, "device add failed"))
		if strings.Contains(strings.ToLower(detail), "already exists") || strings.Contains(strings.ToLower(detail), "already configured") {
			return nil
		}
		return fmt.Errorf("%s", detail)
	}
	return nil
}

func (s *Service) attachContainerModelVolume(name, volume string, onData func(string)) error {
	volume = strings.TrimSpace(volume)
	if volume == "" {
		return nil
	}
	return s.EnsureDevice(name, "models", []string{
		"config", "device", "add", name, "models", "disk",
		"pool=default", "source=" + volume, "path=/models",
	})
}

// ensureContainerStaticIPv4 pins the instance's bridge address. The NIC comes
// from the default profile, so the instance needs its own device override
// before an address can belong to this container alone. A host that merely
// shares another host's bridge cannot pin there at all -- only the incusd that
// created the network hands out addresses on it -- so that case reports the
// pin as deferred: it has to be made inside the guest once it is running.
func (s *Service) ensureContainerStaticIPv4(name, address string, onData func(string)) (deferred bool, changed bool, err error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return false, false, nil
	}
	if net.ParseIP(address) == nil || strings.Contains(address, ":") {
		return false, false, fmt.Errorf("ipv4Address %q is not an IPv4 address", address)
	}
	current, readErr := s.commandRunner([]string{"config", "device", "get", name, "eth0", "ipv4.address"}, nil, 30*time.Second)
	if readErr == nil && current.ExitCode == 0 && strings.TrimSpace(current.Stdout) == address {
		return false, false, nil
	}
	overridden, overrideErr := s.commandRunner([]string{"config", "device", "override", name, "eth0", "ipv4.address=" + address}, onData, 2*time.Minute)
	if overrideErr == nil && overridden.ExitCode == 0 {
		return false, true, nil
	}
	detail := textutil.FirstNonEmpty(overridden.Stderr, overridden.Stdout, textutil.ErrString(overrideErr, "incus config device override failed"))
	if isUnmanagedParentBridge(detail) {
		return deferGuestIPv4Pin(name, address, onData), false, nil
	}
	if !strings.Contains(strings.ToLower(detail), "already exists") {
		return false, false, fmt.Errorf("pin container address: %s", detail)
	}
	// The override is already in place from an earlier provision; only the
	// address itself still has to move.
	updated, updateErr := s.commandRunner([]string{"config", "device", "set", name, "eth0", "ipv4.address", address}, onData, 2*time.Minute)
	if updateErr == nil && updated.ExitCode == 0 {
		return false, true, nil
	}
	detail = textutil.FirstNonEmpty(updated.Stderr, updated.Stdout, textutil.ErrString(updateErr, "incus config device set failed"))
	if isUnmanagedParentBridge(detail) {
		return deferGuestIPv4Pin(name, address, onData), false, nil
	}
	return false, false, fmt.Errorf("pin container address: %s", detail)
}

// isUnmanagedParentBridge reads incus's refusal to hand out an address on a
// network it does not own. It is the ordinary answer on every host but the one
// that created the bridge, so it is a routing decision rather than a failure.
func isUnmanagedParentBridge(detail string) bool {
	return strings.Contains(strings.ToLower(detail), "unmanaged parent bridge")
}

func deferGuestIPv4Pin(name, address string, onData func(string)) bool {
	if onData != nil {
		onData(fmt.Sprintf("The bridge is owned by another host; pinning %s inside %q instead", address, name))
	}
	return true
}

const guestStaticIPv4NetplanPath = "/etc/netplan/99-opute-static.yaml"

const guestStaticIPv4NetplanTemplate = `set -e
umask 022
cat > %s <<'NETPLAN'
network:
  version: 2
  ethernets:
    eth0:
      dhcp4: false
      addresses: [%s/%s]
      routes:
        - to: default
          via: %s
      nameservers:
        addresses: [%s]
NETPLAN
chmod 0600 %s
netplan apply
`

// applyGuestStaticIPv4 pins the address from inside the guest, for the hosts
// that are not allowed to pin it on the NIC. The prefix and gateway are read
// back from the lease the guest currently holds rather than assumed from the
// address: this host does not own the network, so the guest's own routing
// table is the only honest description of it available here.
func (s *Service) applyGuestStaticIPv4(name, address string, onData func(string)) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil
	}
	observed, prefix, gateway, err := s.readGuestIPv4Lease(name)
	if err != nil {
		return err
	}
	if observed == address && s.guestHoldsStaticIPv4(name, address) {
		return nil
	}
	if gateway == "" {
		return fmt.Errorf("pin %s inside %q: the guest holds no default route to copy", address, name)
	}
	if onData != nil {
		onData(fmt.Sprintf("Pinning %s/%s via %s inside %q", address, prefix, gateway, name))
	}
	script := fmt.Sprintf(guestStaticIPv4NetplanTemplate, guestStaticIPv4NetplanPath, address, prefix, gateway, gateway, guestStaticIPv4NetplanPath)
	applied, applyErr := s.commandRunner([]string{"exec", name, "--", "sh", "-c", script}, onData, 2*time.Minute)
	if applyErr != nil || applied.ExitCode != 0 {
		return fmt.Errorf("pin %s inside %q: %s", address, name, textutil.FirstNonEmpty(applied.Stderr, applied.Stdout, textutil.ErrString(applyErr, "netplan apply failed")))
	}
	deadline := time.Now().Add(containerAddressTimeout)
	for {
		held, _, _, readErr := s.readGuestIPv4Lease(name)
		if readErr == nil && held == address {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pin %s inside %q: the guest still reports %q", address, name, held)
		}
		time.Sleep(2 * time.Second)
	}
}

// guestHoldsStaticIPv4 reports whether the pin already came from this managed
// file, so that a guest that merely happens to hold the right lease is still
// pinned rather than left on DHCP.
func (s *Service) guestHoldsStaticIPv4(name, address string) bool {
	shown, err := s.commandRunner([]string{"exec", name, "--", "cat", guestStaticIPv4NetplanPath}, nil, 30*time.Second)
	if err != nil || shown.ExitCode != 0 {
		return false
	}
	return strings.Contains(shown.Stdout, address+"/")
}

// readGuestIPv4Lease returns the address, prefix length and default gateway
// eth0 currently holds in the guest.
func (s *Service) readGuestIPv4Lease(name string) (address string, prefix string, gateway string, err error) {
	addr, addrErr := s.commandRunner([]string{"exec", name, "--", "ip", "-4", "-o", "addr", "show", "dev", "eth0"}, nil, 30*time.Second)
	if addrErr != nil || addr.ExitCode != 0 {
		return "", "", "", fmt.Errorf("read guest address of %q: %s", name, textutil.FirstNonEmpty(addr.Stderr, addr.Stdout, textutil.ErrString(addrErr, "ip addr show failed")))
	}
	address, prefix = parseGuestIPv4Address(addr.Stdout)
	route, routeErr := s.commandRunner([]string{"exec", name, "--", "ip", "-4", "route", "show", "default"}, nil, 30*time.Second)
	if routeErr == nil && route.ExitCode == 0 {
		gateway = parseGuestIPv4Gateway(route.Stdout)
	}
	return address, prefix, gateway, nil
}

func parseGuestIPv4Address(output string) (address string, prefix string) {
	prefix = "24"
	fields := strings.Fields(output)
	for index, field := range fields {
		if field != "inet" || index+1 >= len(fields) {
			continue
		}
		value := fields[index+1]
		if head, tail, ok := strings.Cut(value, "/"); ok {
			return head, tail
		}
		return value, prefix
	}
	return "", prefix
}

func parseGuestIPv4Gateway(output string) string {
	fields := strings.Fields(output)
	for index, field := range fields {
		if field == "via" && index+1 < len(fields) {
			return fields[index+1]
		}
	}
	return ""
}

func (s *Service) ensureContainerHTTPProxy(name string, port int, onData func(string)) error {
	deviceName := "http-proxy"
	show, err := s.commandRunner([]string{"config", "device", "show", name}, nil, 30*time.Second)
	if err == nil && show.ExitCode == 0 && strings.Contains(show.Stdout, deviceName+":") {
		return nil
	}
	proxy := fmt.Sprintf("listen=tcp:0.0.0.0:%d,connect=tcp:127.0.0.1:%d", port, port)
	added, err := s.commandRunner([]string{"config", "device", "add", name, deviceName, "proxy", proxy}, onData, 2*time.Minute)
	if err != nil || added.ExitCode != 0 {
		detail := textutil.FirstNonEmpty(added.Stderr, added.Stdout, textutil.ErrString(err, "proxy add failed"))
		if strings.Contains(strings.ToLower(detail), "already exists") {
			return nil
		}
		return fmt.Errorf("attach HTTP proxy: %s", detail)
	}
	return nil
}

const gpuContainerGuestProbeScript = `set -eu
export LD_LIBRARY_PATH=/usr/lib/wsl/lib:${LD_LIBRARY_PATH:-}
test -e /dev/dxg && echo dxg=present || echo dxg=missing
test -e /usr/lib/wsl/lib/libcuda.so.1 && echo libcuda=present || echo libcuda=missing
test -e /dev/dri/renderD128 && echo dri_render=present || echo dri_render=missing
if command -v nvidia-smi >/dev/null 2>&1; then
  if nvidia-smi --query-gpu=name,memory.total --format=csv,noheader 2>/dev/null; then
    echo nvidia_smi=ok
  else
    echo nvidia_smi=nvml_failed
  fi
elif [ -x /usr/local/bin/nvidia-smi ]; then
  if /usr/local/bin/nvidia-smi --query-gpu=name,memory.total --format=csv,noheader 2>/dev/null; then
    echo nvidia_smi=ok
  else
    echo nvidia_smi=nvml_failed
  fi
else
  echo nvidia_smi=missing
fi
if command -v python3 >/dev/null 2>&1; then
  if python3 - <<'PY'
import ctypes
import os
import sys
if not os.path.exists("/dev/dxg"):
    sys.exit("dxg missing")
lib = ctypes.CDLL("/usr/lib/wsl/lib/libcuda.so.1")
ret = lib.cuInit(0)
if ret != 0:
    print(f"cuda_init_code={ret}")
    sys.exit(f"cuInit returned {ret}")
print("cuda_init=ok")
PY
  then
    echo cuda_init=ok
  else
    echo cuda_init=failed
  fi
else
  echo cuda_init=python_missing
fi
`

// ProbeGPUContainer launches a disposable system container, probes GPU visibility, and deletes it.
func (s *Service) ProbeGPUContainer(onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("probe_gpu_container is unsupported on %s host agents", runtime.GOOS)
	}
	hostTier, err := s.deps.ProbeIncusGPU(map[string]any{"qemuRequired": false})
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"wslHost": hostTier,
		"status":  "blocked",
	}
	if status, _ := hostTier["status"].(string); status != "ready_for_host_probe" {
		result["status"] = "blocked_host_gpu"
		result["blockers"] = []string{"WSL host GPU prerequisites are not ready"}
		return result, nil
	}
	probeName := fmt.Sprintf("opute-gpu-probe-%d", time.Now().Unix())
	defer func() {
		_, _ = s.commandRunner(s.deleteVMArgs(probeName), onData, 2*time.Minute)
	}()
	if onData != nil {
		onData(fmt.Sprintf("Launching disposable GPU probe container %q...", probeName))
	}
	if err := s.launchIncusContainer(probeName, "images:ubuntu/24.04", "4GiB", defaultIncusVMCPUs, defaultIncusVMMemory, false, onData, 10*time.Minute); err != nil {
		result["status"] = "probe_launch_failed"
		result["error"] = err.Error()
		return result, nil
	}
	if err := s.attachContainerGPUDevices(probeName, true, onData); err != nil {
		result["status"] = "gpu_attach_failed"
		result["error"] = err.Error()
		return result, nil
	}
	if err := s.WaitForVMExecReady(probeName, 3*time.Minute, onData); err != nil {
		result["status"] = "probe_exec_unavailable"
		result["error"] = err.Error()
		return result, nil
	}
	prep, prepErr := s.commandRunner([]string{"exec", probeName, "--", "sh", "-lc", "command -v python3 >/dev/null 2>&1 || (export DEBIAN_FRONTEND=noninteractive && apt-get update -qq && apt-get install -y -qq python3)"}, onData, 3*time.Minute)
	if prepErr != nil || prep.ExitCode != 0 {
		result["status"] = "probe_prepare_failed"
		result["error"] = textutil.FirstNonEmpty(prep.Stderr, prep.Stdout, textutil.ErrString(prepErr, "failed to prepare GPU probe container"))
		return result, nil
	}
	probe, execErr := s.commandRunner([]string{"exec", probeName, "--", "sh", "-lc", gpuContainerGuestProbeScript}, onData, 90*time.Second)
	guestStdout := strings.TrimSpace(probe.Stdout)
	result["systemContainer"] = map[string]any{
		"instanceName": probeName,
		"guestProbe":   guestStdout,
		"exitCode":     probe.ExitCode,
	}
	if execErr != nil || probe.ExitCode != 0 {
		result["status"] = "guest_probe_failed"
		result["guestProbeError"] = textutil.FirstNonEmpty(probe.Stderr, guestStdout, textutil.ErrString(execErr, "guest probe failed"))
		return result, nil
	}
	gpuOK, status, blockers := evaluateSystemContainerGPUProbe(guestStdout)
	result["gpuAvailableInSystemContainer"] = gpuOK
	result["status"] = status
	if len(blockers) > 0 {
		result["blockers"] = blockers
	}
	return result, nil
}

// ensureContainerRuntimeDevices attaches generic runtime devices required by
// nested workloads inside an Incus system container.
func (s *Service) ensureContainerRuntimeDevices(name string) error {
	if fsutil.Exists("/dev/kmsg") {
		if err := s.EnsureDevice(name, "kmsg", []string{
			"config", "device", "add", name, "kmsg", "unix-char",
			"source=/dev/kmsg", "path=/dev/kmsg",
		}); err != nil {
			return fmt.Errorf("attach /dev/kmsg: %w", err)
		}
	}
	return nil
}

func (s *Service) restartIncusInstanceIfRunning(name string, onData func(string)) error {
	info, err := s.commandRunner([]string{"info", name, "--format", "json"}, onData, 30*time.Second)
	if err != nil || info.ExitCode != 0 {
		return nil
	}
	if !strings.Contains(info.Stdout, `"status":"Running"`) && !strings.Contains(info.Stdout, `"status": "Running"`) {
		return nil
	}
	if onData != nil {
		onData(fmt.Sprintf("Restarting %q to apply container device changes...", name))
	}
	stopped, stopErr := s.commandRunner([]string{"stop", name, "--force"}, onData, 2*time.Minute)
	if stopErr != nil || stopped.ExitCode != 0 {
		return fmt.Errorf("stop container for device reload: %s", textutil.FirstNonEmpty(stopped.Stderr, stopped.Stdout, textutil.ErrString(stopErr, "incus stop failed")))
	}
	started, startErr := s.commandRunner([]string{"start", name}, onData, 2*time.Minute)
	if startErr != nil || started.ExitCode != 0 {
		return fmt.Errorf("start container after device reload: %s", textutil.FirstNonEmpty(started.Stderr, started.Stdout, textutil.ErrString(startErr, "incus start failed")))
	}
	return nil
}
