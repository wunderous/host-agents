package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ollamaContextConfigRelativePath = ".config/opute/ollama-runtime.json"
	ollamaContextMinimum            = 512
	ollamaContextMaximum            = 32768
)

type contextSettingArgs struct {
	ContextSize int `json:"contextSize"`
}

type contextSettingObservation struct {
	ContractVersion string   `json:"contractVersion"`
	Capability      string   `json:"capability"`
	Setting         string   `json:"setting"`
	ContextSize     int      `json:"contextSize"`
	Persisted       bool     `json:"persisted"`
	Applied         bool     `json:"applied"`
	Ready           bool     `json:"ready"`
	Changed         bool     `json:"changed"`
	ServiceNames    []string `json:"serviceNames,omitempty"`
}

func addContextTools(server *mcp.Server) {
	server.AddTool(&mcp.Tool{
		Name:         "opute.capability.llm-serving.get-context-size",
		Description:  "Read the durable host-wide context size used by the active language-model service.",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "capability", "setting", "contextSize", "persisted"}},
	}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		contextSize, err := readContextSize()
		if err != nil {
			return nil, err
		}
		return structured(contextSettingObservation{
			ContractVersion: "opute.capability.llm-serving.context-size.v1",
			Capability:      "llm-serving",
			Setting:         "context-size",
			ContextSize:     contextSize,
			Persisted:       contextSize > 0,
			Applied:         contextSize > 0,
			Ready:           waitForOllamaAPI(ctx),
		})
	})

	server.AddTool(&mcp.Tool{
		Name:         "opute.capability.llm-serving.set-context-size",
		Description:  "Persist and apply a host-wide context size for the active language-model service.",
		InputSchema:  map[string]any{"type": "object", "required": []string{"contextSize"}, "properties": map[string]any{"contextSize": map[string]any{"type": "integer", "minimum": ollamaContextMinimum, "maximum": ollamaContextMaximum}}},
		OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "capability", "setting", "contextSize", "persisted", "applied"}},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args contextSettingArgs
		if request != nil && request.Params != nil {
			encoded, _ := json.Marshal(request.Params.Arguments)
			if err := json.Unmarshal(encoded, &args); err != nil {
				return nil, err
			}
		}
		result, err := writeContextSize(ctx, args.ContextSize)
		if err != nil {
			return nil, err
		}
		return structured(result)
	})
}

func readContextSize() (int, error) {
	path, err := contextConfigPath()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var raw map[string]any
		if json.Unmarshal(data, &raw) == nil {
			if value, ok := raw["contextSize"].(float64); ok && int(value) >= ollamaContextMinimum && int(value) <= ollamaContextMaximum {
				return int(value), nil
			}
		}
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("read shared Ollama configuration: %w", err)
	}

	for _, candidate := range contextServiceCandidates() {
		data, readErr := os.ReadFile(candidate.path)
		if readErr != nil {
			continue
		}
		if value := contextSizeFromUnit(string(data)); value > 0 {
			return value, nil
		}
	}
	return 0, nil
}

func writeContextSize(ctx context.Context, contextSize int) (contextSettingObservation, error) {
	if contextSize < ollamaContextMinimum || contextSize > ollamaContextMaximum {
		return contextSettingObservation{}, fmt.Errorf("contextSize must be between %d and %d tokens", ollamaContextMinimum, ollamaContextMaximum)
	}

	current, err := readContextSize()
	if err != nil {
		return contextSettingObservation{}, err
	}
	path, err := contextConfigPath()
	if err != nil {
		return contextSettingObservation{}, err
	}
	config, err := readJSONMap(path)
	if err != nil {
		return contextSettingObservation{}, err
	}
	config["contextSize"] = contextSize
	if err := writeJSONMap(path, config); err != nil {
		return contextSettingObservation{}, err
	}

	serviceNames := make([]string, 0, 2)
	changedUnit := false
	for _, candidate := range contextServiceCandidates() {
		data, readErr := os.ReadFile(candidate.path)
		if readErr != nil {
			continue
		}
		updated, changed := renderContextEnvironment(string(data), contextSize)
		if changed {
			if err := writeFileAtomically(candidate.path, []byte(updated), 0600); err != nil {
				return contextSettingObservation{}, fmt.Errorf("update Ollama service unit: %w", err)
			}
			changedUnit = true
		}
		serviceNames = append(serviceNames, candidate.name)
	}

	applied := len(serviceNames) == 0
	ready := false
	if len(serviceNames) > 0 {
		if changedUnit {
			_ = runSystemctl(ctx, "daemon-reload")
		}
		for _, serviceName := range serviceNames {
			active := systemdServiceActive(ctx, serviceName)
			if changedUnit && active {
				if err := runSystemctl(ctx, "restart", serviceName); err != nil {
					return contextSettingObservation{}, fmt.Errorf("restart Ollama service %q: %w", serviceName, err)
				}
				active = waitForOllamaAPI(ctx)
			}
			if active {
				applied = true
				ready = waitForOllamaAPI(ctx)
			}
		}
	}
	if len(serviceNames) == 0 {
		// The durable file is still useful for a service created later, but a
		// running unmanaged process cannot consume a changed environment until
		// its owner restarts it. Report that honestly to the caller.
		ready = waitForOllamaAPI(ctx)
		applied = !ready
	}

	return contextSettingObservation{
		ContractVersion: "opute.capability.llm-serving.context-size.v1",
		Capability:      "llm-serving",
		Setting:         "context-size",
		ContextSize:     contextSize,
		Persisted:       true,
		Applied:         applied,
		Ready:           ready,
		Changed:         current != contextSize || changedUnit,
		ServiceNames:    serviceNames,
	}, nil
}

func contextConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ollamaContextConfigRelativePath), nil
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read shared Ollama configuration: %w", err)
	}
	result := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("decode shared Ollama configuration: %w", err)
		}
	}
	return result, nil
}

func writeJSONMap(path string, value map[string]any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(path, append(data, '\n'), 0600)
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".opute-ollama-context-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

type contextServiceCandidate struct {
	name string
	path string
}

func contextServiceCandidates() []contextServiceCandidate {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	base := filepath.Join(home, ".config", "systemd", "user")
	return []contextServiceCandidate{
		{name: "ollama.service", path: filepath.Join(base, "ollama.service")},
		{name: "opute-ollama.service", path: filepath.Join(base, "opute-ollama.service")},
	}
}

func contextSizeFromUnit(unit string) int {
	for _, line := range strings.Split(unit, "\n") {
		if !strings.Contains(line, "OLLAMA_CONTEXT_LENGTH=") {
			continue
		}
		value := strings.TrimSpace(strings.SplitN(line, "OLLAMA_CONTEXT_LENGTH=", 2)[1])
		value = strings.Trim(value, "\"'")
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= ollamaContextMinimum && parsed <= ollamaContextMaximum {
			return parsed
		}
	}
	return 0
}

func renderContextEnvironment(unit string, contextSize int) (string, bool) {
	wanted := "Environment=OLLAMA_CONTEXT_LENGTH=" + strconv.Itoa(contextSize)
	lines := strings.Split(unit, "\n")
	changed := false
	found := false
	for index, line := range lines {
		if !strings.Contains(line, "OLLAMA_CONTEXT_LENGTH=") {
			continue
		}
		found = true
		if strings.TrimSpace(line) != wanted {
			lines[index] = wanted
			changed = true
		}
	}
	if !found {
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Environment=OLLAMA_HOST=") || strings.HasPrefix(trimmed, "Environment=\"OLLAMA_HOST=") {
				lines = append(lines[:index+1], append([]string{wanted}, lines[index+1:]...)...)
				changed = true
				break
			}
		}
	}
	return strings.Join(lines, "\n"), changed
}

func runSystemctl(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func systemdServiceActive(ctx context.Context, serviceName string) bool {
	command := exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", serviceName)
	return command.Run() == nil
}

func waitForOllamaAPI(ctx context.Context) bool {
	port := contextPort()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/api/tags", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					return true
				}
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
	return false
}

func contextPort() int {
	if path, err := contextConfigPath(); err == nil {
		if config, readErr := readJSONMap(path); readErr == nil {
			if raw, ok := config["port"].(float64); ok && int(raw) > 0 && int(raw) < 65536 {
				return int(raw)
			}
		}
	}
	for _, candidate := range contextServiceCandidates() {
		data, err := os.ReadFile(candidate.path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "OLLAMA_HOST=") {
				continue
			}
			value := strings.TrimSpace(strings.SplitN(line, "OLLAMA_HOST=", 2)[1])
			value = strings.Trim(value, "\"'")
			_, port, err := net.SplitHostPort(value)
			if err == nil {
				if parsed, parseErr := strconv.Atoi(port); parseErr == nil && parsed > 0 && parsed < 65536 {
					return parsed
				}
			}
		}
	}
	return 11434
}
