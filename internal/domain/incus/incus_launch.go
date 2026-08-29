package incus

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/textutil"
)

// normalizeProvisionInstanceType maps caller input to "container" (default) or
// "virtual-machine". Empty or unknown values provision a system container so
// automated paths do not create QEMU guests unless explicitly requested.
func normalizeProvisionInstanceType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "container", "system-container":
		return "container"
	case "vm", "virtual-machine", "virtual_machine", "virtual machine":
		return "virtual-machine"
	default:
		return "container"
	}
}

// parseProvisionInstanceType preserves the historical container default for an
// omitted value while making an explicitly supplied runtime kind a typed
// contract. Provisioning an existing instance must never turn a typo or a
// runtime-kind mismatch into a second launch attempt.
func parseProvisionInstanceType(raw string) (kind string, explicit bool, err error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "container", false, nil
	}
	switch normalized {
	case "container", "system-container":
		return "container", true, nil
	case "vm", "virtual-machine", "virtual_machine", "virtual machine":
		return "virtual-machine", true, nil
	default:
		return "", true, &IncusRuntimeKindError{
			Code:        "incus_runtime_kind_invalid",
			Requested:   strings.TrimSpace(raw),
			Remediation: "Use instanceType=container or instanceType=virtual-machine.",
		}
	}
}

func (s *Service) ReadInstanceType(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("instance name is required")
	}
	if err := s.assertIncusOwnership(name, "read_instance_info"); err != nil {
		return "", err
	}
	// The Incus CLI used by the WSL host does not expose --format for
	// `incus info`. Query the instance API directly so runtime-kind validation
	// remains stable across CLI versions and does not depend on human output.
	path := fmt.Sprintf("/1.0/instances/%s", urlPathEscape(name))
	res, err := s.commandRunner([]string{"query", path}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "incus instance query failed"))
	}
	var info struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &info); err != nil {
		return "", err
	}
	typeName := strings.TrimSpace(info.Type)
	if typeName == "" {
		return "", errors.New("incus instance type is missing")
	}
	return typeName, nil
}

// normalizeObservedInstanceType maps the only two Incus runtime kinds this
// provider owns into the provisioning vocabulary. Unknown kinds fail closed;
// they must not be reported as a container or VM by a compatibility default.
func normalizeObservedInstanceType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "container":
		return "container", nil
	case "virtual-machine", "virtual machine":
		return "virtual-machine", nil
	default:
		return "", &IncusRuntimeKindError{
			Code:        "incus_runtime_kind_unknown",
			Observed:    strings.TrimSpace(raw),
			Remediation: "Inspect the Incus instance runtime kind before provisioning it.",
		}
	}
}

type incusProfileDevice struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
	Pool string `json:"pool,omitempty"`
}

func (s *Service) readDefaultProfileDevices() (map[string]incusProfileDevice, error) {
	res, err := s.commandRunner([]string{"query", "/1.0/profiles/default"}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "incus profile query failed"))
	}
	var profile struct {
		Devices map[string]incusProfileDevice `json:"devices"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &profile); err != nil {
		return nil, err
	}
	if profile.Devices == nil {
		return map[string]incusProfileDevice{}, nil
	}
	return profile.Devices, nil
}

func profileHasNIC(devices map[string]incusProfileDevice) bool {
	for _, device := range devices {
		if device.Type == "nic" {
			return true
		}
	}
	return false
}

func resolveIncusImageSource(normalizedLaunchImage string) map[string]any {
	image := strings.TrimSpace(normalizedLaunchImage)
	if strings.HasPrefix(image, "images:") {
		return map[string]any{
			"type":     "image",
			"mode":     "pull",
			"server":   "https://images.linuxcontainers.org",
			"protocol": "simplestreams",
			"alias":    strings.TrimPrefix(image, "images:"),
		}
	}
	if strings.HasPrefix(image, "local:") {
		return map[string]any{"type": "image", "alias": strings.TrimPrefix(image, "local:")}
	}
	colon := strings.Index(image, ":")
	if colon > 0 {
		remote := image[:colon]
		alias := image[colon+1:]
		if remote == "ubuntu" {
			if !strings.Contains(alias, "/") {
				alias = "ubuntu/" + alias
			}
			return map[string]any{
				"type":     "image",
				"mode":     "pull",
				"server":   "https://images.linuxcontainers.org",
				"protocol": "simplestreams",
				"alias":    alias,
			}
		}
	}
	return map[string]any{"type": "image", "alias": image}
}

func (s *Service) resolveDefaultStoragePool() (string, error) {
	res, err := s.commandRunner([]string{"query", "/1.0/storage-pools"}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "storage pool query failed"))
	}
	var entries []string
	if err := json.Unmarshal([]byte(res.Stdout), &entries); err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no storage pools configured")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimPrefix(entry, "/1.0/storage-pools/")
		name = strings.Trim(name, "/")
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no storage pools configured")
	}
	for _, name := range names {
		if name == "default" {
			return "default", nil
		}
	}
	return names[0], nil
}

func (s *Service) launchIncusVMViaAPI(vmName, image string, cpus int, memory, disk string, onData func(string), timeout time.Duration) error {
	normalizedImage := normalizeIncusLaunchImage(image)
	if normalizedImage == "" {
		normalizedImage = "images:ubuntu/22.04"
	}

	profileDevices, err := s.readDefaultProfileDevices()
	if err != nil {
		profileDevices = map[string]incusProfileDevice{}
	}

	if cpus <= 0 {
		cpus = defaultIncusVMCPUs
	}
	if memory == "" {
		memory = defaultIncusVMMemory
	}
	config := incusVMConfig(cpus, memory)
	if owner := s.ownerConfigValue(); owner != "" {
		config[oputeIncusOwnerLabel] = owner
		if agentID := s.ownerAgentConfigValue(); agentID != "" {
			config[oputeIncusAgentLabel] = agentID
		}
	}
	payload := map[string]any{
		"name":     vmName,
		"type":     "virtual-machine",
		"profiles": []string{"default"},
		"source":   resolveIncusImageSource(normalizedImage),
		"config":   config,
	}

	instanceDevices := map[string]any{}
	rootDevice := map[string]any{
		"type": "disk",
		"path": "/",
	}
	// An empty disk means admission found no enforceable quota. Omit the size
	// rather than writing a limit the pool will not honor.
	if disk != "" {
		rootDevice["size"] = disk
	}
	if profileRoot := profileDevices["root"]; profileRoot.Type == "disk" {
		// Override the profile root size as profiles commonly provide a small
		// default disk. Preserve an explicitly configured storage pool.
		if profileRoot.Path != "" {
			rootDevice["path"] = profileRoot.Path
		}
		if profileRoot.Pool != "" {
			rootDevice["pool"] = profileRoot.Pool
		}
		instanceDevices["root"] = rootDevice
	} else {
		pool, poolErr := s.resolveDefaultStoragePool()
		if poolErr != nil {
			return poolErr
		}
		rootDevice["pool"] = pool
		instanceDevices["root"] = rootDevice
	}
	if !profileHasNIC(profileDevices) {
		instanceDevices["eth0"] = map[string]any{
			"type":    "nic",
			"name":    "eth0",
			"nictype": "bridged",
			"parent":  "incusbr0",
		}
	}
	if len(instanceDevices) > 0 {
		payload["devices"] = instanceDevices
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	create, err := s.commandRunner([]string{"query", "-X", "POST", "--wait", "/1.0/instances", "-d", string(data)}, onData, timeout)
	if err != nil {
		return err
	}
	if create.ExitCode != 0 {
		return fmt.Errorf("incus create %q: %s", vmName, textutil.FirstNonEmpty(create.Stderr, create.Stdout, "failed to create VM"))
	}
	// Incus profiles may provide a small root disk for VMs. Apply the caller's
	// requested size explicitly after creation so the provisioning contract is
	// reflected by the guest-visible block device rather than only inventory.
	// There is no /device/{name} endpoint — patch the instance devices map.
	// Skipped when admission produced no enforceable quota: there is nothing to
	// resize to, and patching a sizeless device would only restate the profile.
	if disk != "" {
		resizePayload, resizeMarshalErr := json.Marshal(map[string]any{
			"devices": map[string]any{
				"root": rootDevice,
			},
		})
		if resizeMarshalErr != nil {
			return resizeMarshalErr
		}
		instancePath := fmt.Sprintf("/1.0/instances/%s", urlPathEscape(vmName))
		resize, err := s.commandRunner([]string{"query", "-X", "PATCH", "--wait", instancePath, "-d", string(resizePayload)}, onData, timeout)
		if err != nil {
			return err
		}
		if resize.ExitCode != 0 {
			return fmt.Errorf("incus resize root disk %q: %s", vmName, textutil.FirstNonEmpty(resize.Stderr, resize.Stdout, "failed to resize root disk"))
		}
	}

	startBody := `{"action":"start","force":false,"stateful":false}`
	statePath := fmt.Sprintf("/1.0/instances/%s/state", urlPathEscape(vmName))
	start, err := s.commandRunner([]string{"query", "-X", "PUT", "--wait", statePath, "-d", startBody}, onData, timeout)
	if err != nil {
		return err
	}
	if start.ExitCode != 0 {
		return fmt.Errorf("incus start %q: %s", vmName, textutil.FirstNonEmpty(start.Stderr, start.Stdout, "failed to start VM"))
	}
	return nil
}

func incusVMConfig(cpus int, memory string) map[string]string {
	return map[string]string{
		"limits.cpu":    fmt.Sprintf("%d", cpus),
		"limits.memory": memory,
		// K3s workloads are durable Kubernetes resources, but they cannot
		// recover after a host restart if the Incus VM remains stopped.
		// Persist the VM's boot policy as part of provisioning rather than
		// relying on an operator to start it manually after every reboot.
		"boot.autostart": "true",
	}
}
