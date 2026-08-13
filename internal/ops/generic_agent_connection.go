package ops

import (
	"errors"
	"strings"
)

// ConfigureAgentConnectionArgs is product-neutral configuration for a host
// agent that connects to an arbitrary control plane. The caller owns the
// environment variable names and values; the host agent only persists them
// with restrictive permissions and optionally restarts the declared service.
type ConfigureAgentConnectionArgs struct {
	EnvFile     string            `json:"envFile"`
	Environment map[string]string `json:"environment"`
	ServiceName string            `json:"serviceName,omitempty"`
	Restart     *bool             `json:"restart,omitempty"`
	Scope       string            `json:"scope,omitempty"`
}

func (s *HostOperationsService) ConfigureAgentConnection(args ConfigureAgentConnectionArgs, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(args.EnvFile) == "" {
		return nil, errors.New("envFile is required")
	}
	if len(args.Environment) == 0 {
		return nil, errors.New("environment is required")
	}
	if err := upsertEnvFile(args.EnvFile, args.Environment); err != nil {
		return nil, err
	}
	restart := args.Restart != nil && *args.Restart
	result := map[string]any{"envFile": args.EnvFile, "restart": restart, "status": "env_written"}
	if strings.TrimSpace(args.ServiceName) == "" || !restart {
		return result, nil
	}
	state, err := s.SetHostServiceState(SetHostServiceStateArgs{ServiceName: args.ServiceName, State: "restart", Scope: args.Scope}, onData)
	if err != nil {
		return nil, err
	}
	result["status"] = "restart_scheduled"
	result["service"] = state
	return result, nil
}
