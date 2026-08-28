package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func upsertEnvFile(path string, assignments map[string]string, removals []string) error {
	values := map[string]string{}
	removed := map[string]bool{}
	for _, key := range removals {
		key = strings.TrimSpace(key)
		if key != "" {
			removed[key] = true
		}
	}
	var comments []string
	var keyOrder []string
	if raw, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				if trimmed != "" {
					comments = append(comments, line)
				}
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, "export ")
			key, value, ok := strings.Cut(trimmed, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				continue
			}
			if removed[key] {
				continue
			}
			if _, exists := values[key]; !exists {
				keyOrder = append(keyOrder, key)
			}
			values[key] = value
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read env file: %w", err)
	}
	for key, value := range assignments {
		if _, exists := values[key]; !exists {
			keyOrder = append(keyOrder, key)
		}
		values[key] = value
	}

	var b strings.Builder
	for _, comment := range comments {
		b.WriteString(comment)
		b.WriteByte('\n')
	}
	seen := map[string]bool{}
	for _, key := range keyOrder {
		value, ok := values[key]
		if !ok || seen[key] {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
		seen[key] = true
	}
	for key, value := range values {
		if seen[key] {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create env directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".host-agent.env.*")
	if err != nil {
		return fmt.Errorf("create temp env file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod env file: %w", err)
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write env file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close env file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace env file: %w", err)
	}
	return nil
}

// ConfigureAgentConnectionArgs is product-neutral configuration for a host
// agent that connects to an arbitrary control plane. The caller owns the
// environment variable names and values; the host agent only persists them
// with restrictive permissions and optionally restarts the declared service.
type ConfigureAgentConnectionArgs struct {
	EnvFile     string            `json:"envFile"`
	Environment map[string]string `json:"environment"`
	Remove      []string          `json:"remove,omitempty"`
	ServiceName string            `json:"serviceName,omitempty"`
	Restart     *bool             `json:"restart,omitempty"`
	Scope       string            `json:"scope,omitempty"`
}

func (s *Service) ConfigureAgentConnection(args ConfigureAgentConnectionArgs, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(args.EnvFile) == "" {
		return nil, errors.New("envFile is required")
	}
	if len(args.Environment) == 0 {
		return nil, errors.New("environment is required")
	}
	if err := upsertEnvFile(args.EnvFile, args.Environment, args.Remove); err != nil {
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
