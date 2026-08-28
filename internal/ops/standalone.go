package ops

import (
	"os/exec"
	"time"

	"github.com/wunderous/host-agents/internal/heartbeat"
)

type LocalPrerequisitesResult struct {
	Provider       string            `json:"provider"`
	ProviderBinary string            `json:"providerBinary"`
	ProviderReady  bool              `json:"providerReady"`
	Commands       map[string]bool   `json:"commands"`
	Checks         map[string]string `json:"checks,omitempty"`
}

func (s *HostOperationsService) CheckLocalPrerequisites() (*LocalPrerequisitesResult, error) {
	commands := map[string]bool{}
	for _, name := range []string{"incus", "bash", "curl", "base64"} {
		_, err := exec.LookPath(name)
		commands[name] = err == nil
	}
	providerReady := false
	checks := map[string]string{}
	res, err := s.commandRunner([]string{"version"}, nil, 10*time.Second)
	if err != nil {
		checks["incus"] = err.Error()
	} else if res.ExitCode == 0 {
		providerReady = true
		checks["incus"] = "ready"
	} else {
		checks["incus"] = firstNonEmpty(res.Stderr, res.Stdout, "incus check failed")
	}
	return &LocalPrerequisitesResult{
		Provider:       string(s.shared.Runtime.ReadProviderID()),
		ProviderBinary: s.shared.Runtime.ProviderBinary(),
		ProviderReady:  providerReady,
		Commands:       commands,
		Checks:         checks,
	}, nil
}

func (s *HostOperationsService) GetLocalStatus() (map[string]any, error) {
	vms, err := s.ListVMs(true)
	if err != nil {
		return nil, err
	}
	capacity, err := s.VMInventoryCapacity()
	if err != nil {
		return nil, err
	}
	system := heartbeat.ReadHostSystemMetadata()
	if s.shared.ResourceSnapshot != nil {
		if system == nil {
			system = map[string]any{}
		}
		system["resourceAdmission"] = s.shared.ResourceSnapshot()
	}
	return map[string]any{
		"provider":       s.shared.Runtime.ReadProviderID(),
		"providerBinary": s.shared.Runtime.ProviderBinary(),
		"vmCount":        len(vms.VMs),
		"vms":            vms.VMs,
		"capacity":       capacity,
		"system":         system,
		"checkedAt":      time.Now().UTC().Format(time.RFC3339),
	}, nil
}
