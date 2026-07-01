package ops

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type incusProfileDevice struct {
	Type string `json:"type"`
}

func (s *HostOperationsService) readDefaultProfileDevices() (map[string]incusProfileDevice, error) {
	res, err := s.commandRunner([]string{"query", "/1.0/profiles/default"}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "incus profile query failed"))
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

func (s *HostOperationsService) resolveDefaultStoragePool() (string, error) {
	res, err := s.commandRunner([]string{"query", "/1.0/storage-pools"}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "storage pool query failed"))
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

func (s *HostOperationsService) launchIncusVMViaAPI(vmName, image string, cpus int, memory, disk string, onData func(string), timeout time.Duration) error {
	normalizedImage := normalizeIncusLaunchImage(image)
	if normalizedImage == "" {
		normalizedImage = "images:ubuntu/22.04"
	}

	profileDevices, err := s.readDefaultProfileDevices()
	if err != nil {
		profileDevices = map[string]incusProfileDevice{}
	}

	if memory == "" {
		memory = "2GiB"
	}
	if disk == "" {
		disk = "10GiB"
	}

	payload := map[string]any{
		"name":     vmName,
		"type":     "virtual-machine",
		"profiles": []string{"default"},
		"source":   resolveIncusImageSource(normalizedImage),
		"config": map[string]string{
			"limits.cpu":    fmt.Sprintf("%d", cpus),
			"limits.memory": memory,
		},
	}

	instanceDevices := map[string]any{}
	if profileDevices["root"].Type != "disk" {
		pool, poolErr := s.resolveDefaultStoragePool()
		if poolErr != nil {
			return poolErr
		}
		instanceDevices["root"] = map[string]any{
			"type": "disk",
			"path": "/",
			"pool": pool,
			"size": disk,
		}
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
		return fmt.Errorf("incus create %q: %s", vmName, firstNonEmpty(create.Stderr, create.Stdout, "failed to create VM"))
	}

	startBody := `{"action":"start","force":false,"stateful":false}`
	statePath := fmt.Sprintf("/1.0/instances/%s/state", urlPathEscape(vmName))
	start, err := s.commandRunner([]string{"query", "-X", "PUT", "--wait", statePath, "-d", startBody}, onData, timeout)
	if err != nil {
		return err
	}
	if start.ExitCode != 0 {
		return fmt.Errorf("incus start %q: %s", vmName, firstNonEmpty(start.Stderr, start.Stdout, "failed to start VM"))
	}
	return nil
}
