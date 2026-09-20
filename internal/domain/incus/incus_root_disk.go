package incus

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/textutil"
)

type instanceRootDevice struct {
	Type string
	Path string
	Pool string
	Size string
}

func (s *Service) readInstanceRootDevice(name string) (instanceRootDevice, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return instanceRootDevice{}, fmt.Errorf("instance name is required")
	}
	path := fmt.Sprintf("/1.0/instances/%s", urlPathEscape(name))
	res, err := s.commandRunner([]string{"query", path}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return instanceRootDevice{}, err
	}
	if res.ExitCode != 0 {
		return instanceRootDevice{}, fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "incus instance query failed"))
	}
	var payload struct {
		Devices         map[string]map[string]any `json:"devices"`
		ExpandedDevices map[string]map[string]any `json:"expanded_devices"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		return instanceRootDevice{}, err
	}
	root := payload.Devices["root"]
	if len(root) == 0 {
		root = payload.ExpandedDevices["root"]
	}
	if len(root) == 0 {
		return instanceRootDevice{}, fmt.Errorf("instance %q has no root disk device", name)
	}
	device := instanceRootDevice{
		Type: stringValue(root["type"]),
		Path: stringValue(root["path"]),
		Pool: stringValue(root["pool"]),
		Size: extractIncusRootDeviceSize(map[string]map[string]any{"root": root}),
	}
	if device.Type == "" {
		device.Type = "disk"
	}
	if device.Path == "" {
		device.Path = "/"
	}
	return device, nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func (s *Service) patchInstanceRootDisk(name string, device map[string]any, onData func(string), timeout time.Duration) error {
	resizePayload, err := json.Marshal(map[string]any{
		"devices": map[string]any{"root": device},
	})
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = defaultDiscoveryTimeout
	}
	instancePath := fmt.Sprintf("/1.0/instances/%s", urlPathEscape(name))
	resize, err := s.commandRunner([]string{"query", "-X", "PATCH", "--wait", instancePath, "-d", string(resizePayload)}, onData, timeout)
	if err != nil {
		return err
	}
	if resize.ExitCode != 0 {
		return fmt.Errorf("incus resize root disk %q: %s", name, textutil.FirstNonEmpty(resize.Stderr, resize.Stdout, "failed to resize root disk"))
	}
	return nil
}

func refuseRootDiskShrink(requested, current string) error {
	requested = strings.TrimSpace(requested)
	current = strings.TrimSpace(current)
	if requested == "" || current == "" {
		return nil
	}
	want, err := resource.ParseByteCapacity(requested)
	if err != nil {
		return fmt.Errorf("root disk size %q is invalid: %w", requested, err)
	}
	have, err := resource.ParseByteCapacity(current)
	if err != nil {
		return fmt.Errorf("observed root disk size %q is invalid: %w", current, err)
	}
	if want < have {
		return fmt.Errorf("root disk shrink from %s to %s is refused; Incus root disks are grow-only", current, requested)
	}
	return nil
}
