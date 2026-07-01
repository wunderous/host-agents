package heartbeat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opute-io/host-agents/internal/fingerprint"
)

type Service struct {
	AgentID             string
	MCPURL              string
	BridgeToken         string
	OnboardingToken     string
	OnboardingSessionID string
	HostMCPEndpoint     string
	HostName            string
	AgentVersion        string
	ProviderID          string
	Fingerprint         fingerprint.Identity
	Interval            time.Duration
	Logger              *slog.Logger

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type Options struct {
	AgentID             string
	MCPURL              string
	BridgeToken         string
	OnboardingToken     string
	OnboardingSessionID string
	HostMCPEndpoint     string
	HostName            string
	AgentVersion        string
	ProviderID          string
	Fingerprint         fingerprint.Identity
	TestMode            bool
	Logger              *slog.Logger
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
		AgentID:             opts.AgentID,
		MCPURL:              opts.MCPURL,
		BridgeToken:         opts.BridgeToken,
		OnboardingToken:     opts.OnboardingToken,
		OnboardingSessionID: opts.OnboardingSessionID,
		HostMCPEndpoint:     opts.HostMCPEndpoint,
		HostName:            opts.HostName,
		AgentVersion:        opts.AgentVersion,
		ProviderID:          opts.ProviderID,
		Fingerprint:         opts.Fingerprint,
		Interval:            interval,
		Logger:              logger,
		stopCh:              make(chan struct{}),
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
		ticker := time.NewTicker(s.Interval)
		for {
			select {
			case <-s.stopCh:
				ticker.Stop()
				return
			case <-ticker.C:
				if err := s.heartbeat(); err != nil {
					s.Logger.Warn("host_agent_heartbeat failed", "err", err)
					if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "404") {
						ticker.Stop()
						break
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
	metadata := map[string]any{}
	if s.OnboardingSessionID != "" {
		metadata["onboardingSessionId"] = s.OnboardingSessionID
	}
	if len(metadata) > 0 {
		registration["metadata"] = metadata
	}
	_, err := s.callTool("register_host_agent", map[string]any{
		"registration": registration,
	})
	return err
}

func (s *Service) heartbeat() error {
	_, err := s.callTool("host_agent_heartbeat", map[string]any{
		"heartbeat": map[string]any{
			"agentId":            s.AgentID,
			"sentAt":             time.Now().UTC().Format(time.RFC3339),
			"fingerprint":        s.Fingerprint.Fingerprint,
			"fingerprintVersion": s.Fingerprint.FingerprintVersion,
			"fingerprintSource":  string(s.Fingerprint.FingerprintSource),
			"connectionState":    "connected",
		},
	})
	return err
}

func (s *Service) callTool(name string, args map[string]any) (map[string]any, error) {
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
	req.Header.Set("Authorization", "Bearer "+s.BridgeToken)
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
