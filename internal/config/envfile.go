package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile loads KEY=VALUE assignments without overriding variables already
// supplied by the process environment. This keeps VS Code's `env` block and
// explicit --env flags authoritative over a shared env file.
func LoadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open env file %q: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("invalid env assignment at %s:%d", path, lineNumber)
		}
		if strings.TrimSpace(os.Getenv(key)) != "" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
				value = value[1 : len(value)-1]
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from %s:%d: %w", key, path, lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read env file %q: %w", path, err)
	}
	return nil
}
