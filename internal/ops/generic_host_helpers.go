package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// upsertEnvFile persists caller-owned environment values without exposing
// their contents in the operation result. It is shared by generic connection
// configuration and keeps host-agent policy independent of any consumer.
func upsertEnvFile(path string, assignments map[string]string) error {
	values := map[string]string{}
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

func firstBridgeIPv4(ips []string) string {
	for _, ip := range ips {
		if strings.HasPrefix(ip, "10.123.") {
			return ip
		}
	}
	for _, ip := range ips {
		if ip != "" && !strings.HasPrefix(ip, "10.42.") && !strings.HasPrefix(ip, "127.") {
			return ip
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func loadBalancerFromService(service map[string]any) (ip, hostname string) {
	status, _ := service["status"].(map[string]any)
	lb, _ := status["loadBalancer"].(map[string]any)
	ingress, _ := lb["ingress"].([]any)
	if len(ingress) == 0 {
		return "", ""
	}
	first, _ := ingress[0].(map[string]any)
	if value, ok := first["ip"].(string); ok {
		ip = value
	}
	if value, ok := first["hostname"].(string); ok {
		hostname = value
	}
	return ip, hostname
}
