package heartbeat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wunderous/host-agents/internal/fingerprint"
)

type Service struct {
	AgentID              string
	MCPURL               string
	BridgeToken          string
	RemoteAgentAuthToken string
	OnboardingToken      string
	OnboardingSessionID  string
	EnvFile              string
	HostMCPEndpoint      string
	HostName             string
	AgentVersion         string
	ProviderID           string
	Fingerprint          fingerprint.Identity
	Interval             time.Duration
	Logger               *slog.Logger
	CollectVMStats       CollectVMStats
	HostCapabilities     []string

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type Options struct {
	AgentID              string
	MCPURL               string
	BridgeToken          string
	RemoteAgentAuthToken string
	OnboardingToken      string
	OnboardingSessionID  string
	EnvFile              string
	HostMCPEndpoint      string
	HostName             string
	AgentVersion         string
	ProviderID           string
	Fingerprint          fingerprint.Identity
	TestMode             bool
	Logger               *slog.Logger
	CollectVMStats       CollectVMStats
	HostCapabilities     []string
}

func Start(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	interval := 60 * time.Second
	if opts.TestMode {
		interval = 10 * time.Second
	}
	s := &Service{
		AgentID:              opts.AgentID,
		MCPURL:               opts.MCPURL,
		BridgeToken:          opts.BridgeToken,
		RemoteAgentAuthToken: opts.RemoteAgentAuthToken,
		OnboardingToken:      opts.OnboardingToken,
		OnboardingSessionID:  opts.OnboardingSessionID,
		EnvFile:              opts.EnvFile,
		HostMCPEndpoint:      opts.HostMCPEndpoint,
		HostName:             opts.HostName,
		AgentVersion:         opts.AgentVersion,
		ProviderID:           opts.ProviderID,
		Fingerprint:          opts.Fingerprint,
		Interval:             interval,
		Logger:               logger,
		CollectVMStats:       opts.CollectVMStats,
		HostCapabilities:     opts.HostCapabilities,
		stopCh:               make(chan struct{}),
	}
	s.wg.Add(1)
	go s.loop()
	return s
}

func (s *Service) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

func (s *Service) loop() {
	defer s.wg.Done()
	backoff := time.Second
	for {
		if err := s.register(); err != nil {
			s.Logger.Warn("register_host_agent failed", "err", err)
			select {
			case <-s.stopCh:
				return
			case <-time.After(backoff):
				if backoff < 30*time.Second {
					backoff *= 2
				}
				continue
			}
		}
		backoff = time.Second
		if err := s.heartbeat(); err != nil {
			s.Logger.Warn("host_agent_heartbeat failed", "err", err)
			if isAuthorizationError(err) {
				continue
			}
		}
		ticker := time.NewTicker(s.Interval)
	heartbeatLoop:
		for {
			select {
			case <-s.stopCh:
				ticker.Stop()
				return
			case <-ticker.C:
				if err := s.heartbeat(); err != nil {
					s.Logger.Warn("host_agent_heartbeat failed", "err", err)
					if isAuthorizationError(err) {
						ticker.Stop()
						break heartbeatLoop
					}
				}
			}
		}
	}
}

func (s *Service) register() error {
	registration := map[string]any{
		"agentId":            s.AgentID,
		"hostName":           s.HostName,
		"fingerprint":        s.Fingerprint.Fingerprint,
		"fingerprintVersion": s.Fingerprint.FingerprintVersion,
		"fingerprintSource":  string(s.Fingerprint.FingerprintSource),
		"endpoint":           s.HostMCPEndpoint,
		"agentVersion":       s.AgentVersion,
	}
	if s.ProviderID != "" {
		registration["providerId"] = s.ProviderID
	}
	if len(s.HostCapabilities) > 0 {
		registration["capabilities"] = s.HostCapabilities
	}
	metadata := map[string]any{}
	if s.OnboardingSessionID != "" {
		metadata["onboardingSessionId"] = s.OnboardingSessionID
	}
	if system := systemMetadata(ReadHostSystemStats()); system != nil {
		metadata["system"] = system
	}
	if len(metadata) > 0 {
		registration["metadata"] = metadata
	}
	result, err := s.callTool("register_host_agent", map[string]any{
		"registration": registration,
	})
	if err == nil {
		s.reconcileAuthToken(result)
		return nil
	}
	if isAuthorizationError(err) && s.RemoteAgentAuthToken != "" && s.RemoteAgentAuthToken != s.BridgeToken {
		result, fallbackErr := s.callToolWithToken("register_host_agent", map[string]any{
			"registration": registration,
		}, s.RemoteAgentAuthToken)
		if fallbackErr == nil {
			s.reconcileAuthToken(result)
			return nil
		}
	}
	return err
}

func (s *Service) heartbeat() error {
	metadata := map[string]any{}
	if publicIp := PrimaryLANIPv4(); publicIp != "" {
		metadata["publicIp"] = publicIp
	}
	if system := systemMetadata(ReadHostSystemStats()); system != nil {
		metadata["system"] = system
	}
	heartbeatPayload := map[string]any{
		"agentId":            s.AgentID,
		"sentAt":             time.Now().UTC().Format(time.RFC3339),
		"fingerprint":        s.Fingerprint.Fingerprint,
		"fingerprintVersion": s.Fingerprint.FingerprintVersion,
		"fingerprintSource":  string(s.Fingerprint.FingerprintSource),
		"connectionState":    "connected",
	}
	if len(metadata) > 0 {
		heartbeatPayload["metadata"] = metadata
	}
	if metrics := s.collectHeartbeatMetrics(); metrics != nil {
		heartbeatPayload["metrics"] = metrics
	}
	result, err := s.callTool("host_agent_heartbeat", map[string]any{
		"heartbeat": heartbeatPayload,
	})
	if err == nil {
		s.reconcileAuthToken(result)
	}
	return err
}

func (s *Service) collectHeartbeatMetrics() map[string]any {
	if s.CollectVMStats == nil {
		return nil
	}
	vmStats, err := s.CollectVMStats()
	if err != nil {
		s.Logger.Warn("vm capacity collection failed", "err", err)
		return nil
	}
	return vmMetrics(vmStats)
}

func (s *Service) bearerTokenForTool(name string) string {
	// Initial registration is scoped to the one-time onboarding install token (opit_*).
	// Heartbeats and later calls use the per-host MCP bearer (opha_*).
	if name == "register_host_agent" && s.OnboardingToken != "" {
		return s.OnboardingToken
	}
	return s.BridgeToken
}

func isAuthorizationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unauthorized") ||
		strings.Contains(message, "http 401") ||
		strings.Contains(message, "http 404")
}

func (s *Service) reconcileAuthToken(result map[string]any) {
	token := findHostAuthToken(result)
	if token == "" || token == s.BridgeToken {
		return
	}
	s.BridgeToken = token
	if s.EnvFile == "" {
		return
	}
	if err := persistAuthToken(s.EnvFile, token); err != nil {
		s.Logger.Warn("persisted reconciled host token failed", "err", err)
	}
}

func findHostAuthToken(value any) string {
	switch current := value.(type) {
	case map[string]any:
		if token, ok := current["authToken"].(string); ok && strings.HasPrefix(token, "opha_") {
			return token
		}
		for _, child := range current {
			if token := findHostAuthToken(child); token != "" {
				return token
			}
		}
	case []any:
		for _, child := range current {
			if token := findHostAuthToken(child); token != "" {
				return token
			}
		}
	case string:
		var decoded any
		if json.Unmarshal([]byte(current), &decoded) == nil {
			return findHostAuthToken(decoded)
		}
	}
	return ""
}

func persistAuthToken(path, token string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "MCP_AUTH_TOKEN=") || strings.HasPrefix(line, "OPUTE_BRIDGE_TOKEN=") || strings.HasPrefix(line, "BRIDGE_TOKEN=") {
			key := strings.SplitN(line, "=", 2)[0]
			lines[i] = key + "=" + token
			found = true
		}
	}
	if !found {
		lines = append(lines, "MCP_AUTH_TOKEN="+token, "OPUTE_BRIDGE_TOKEN="+token, "BRIDGE_TOKEN="+token)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".host-agent.env.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(strings.Join(lines, "\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (s *Service) callTool(name string, args map[string]any) (map[string]any, error) {
	return s.callToolWithToken(name, args, s.bearerTokenForTool(name))
}

func (s *Service) callToolWithToken(name string, args map[string]any, token string) (map[string]any, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.MCPURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("401 unauthorized")
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("404 not found")
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, string(raw))
	}
	return parseMcpResponseBody(raw)
}

func parseMcpResponseBody(raw []byte) (map[string]any, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, fmt.Errorf("empty MCP response")
	}
	if strings.HasPrefix(text, "{") {
		return parseJsonRpcEnvelope([]byte(text))
	}

	var lastResult map[string]any
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if chunk == "" || chunk == "[DONE]" {
			continue
		}
		result, err := parseJsonRpcEnvelope([]byte(chunk))
		if err != nil {
			return nil, err
		}
		if result != nil {
			lastResult = result
		}
	}
	if lastResult == nil {
		return nil, fmt.Errorf("MCP response missing result payload")
	}
	return lastResult, nil
}

func parseJsonRpcEnvelope(raw []byte) (map[string]any, error) {
	var envelope struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("%s", envelope.Error.Message)
	}
	return envelope.Result, nil
}
