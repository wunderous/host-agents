package ops

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type localLLMRelaySession struct {
	id             string
	server         *http.Server
	listener       net.Listener
	allowedSources []*net.IPNet
	incomingToken  string
	upstreamToken  string
	listenHost     string
	listenPort     int
}

type localLLMRelayManager struct {
	mu        sync.Mutex
	sessions  map[string]*localLLMRelaySession
	persist   bool
	configDir string
}

func newLocalLLMRelayManager() *localLLMRelayManager {
	return &localLLMRelayManager{sessions: map[string]*localLLMRelaySession{}}
}

func newPersistentLocalLLMRelayManager() *localLLMRelayManager {
	dir, err := localLLMRelayConfigDir()
	if err != nil {
		dir = ""
	}
	return newPersistentLocalLLMRelayManagerAt(dir)
}

func newPersistentLocalLLMRelayManagerAt(configDir string) *localLLMRelayManager {
	manager := &localLLMRelayManager{sessions: map[string]*localLLMRelaySession{}, persist: true, configDir: strings.TrimSpace(configDir)}
	manager.restore()
	return manager
}

// closeLocalLLMRelaySession frees the TCP port immediately. Shutdown alone can
// race Serve's exit and leave EADDRINUSE for a reclaiming bind on the same port.
func closeLocalLLMRelaySession(session *localLLMRelaySession) {
	if session == nil {
		return
	}
	if session.listener != nil {
		_ = session.listener.Close()
	}
	_ = session.server.Close()
}

func localLLMRelayCIDRsEqual(existing []*net.IPNet, desired []string) bool {
	if len(existing) != len(desired) {
		return false
	}
	normalized := make(map[string]struct{}, len(desired))
	for _, source := range desired {
		_, cidr, err := net.ParseCIDR(strings.TrimSpace(source))
		if err != nil {
			return false
		}
		normalized[cidr.String()] = struct{}{}
	}
	for _, cidr := range existing {
		if cidr == nil {
			return false
		}
		if _, ok := normalized[cidr.String()]; !ok {
			return false
		}
	}
	return true
}

func (m *localLLMRelayManager) start(ctx context.Context, args LocalLLMRelayArgs) (map[string]any, error) {
	if err := ValidateLocalLLMRelayArgs(args); err != nil {
		return nil, err
	}
	if len(args.AllowedSourceCIDRs) == 0 && strings.TrimSpace(args.AllowedSourceIP) != "" {
		args.AllowedSourceCIDRs = []string{strings.TrimSpace(args.AllowedSourceIP) + "/32"}
	}
	var stale *localLLMRelaySession
	m.mu.Lock()
	if existing := m.sessions[args.SessionID]; existing != nil {
		incoming := args.IncomingToken
		if incoming == "" {
			incoming = args.RelayToken
		}
		sameCIDRs := localLLMRelayCIDRsEqual(existing.allowedSources, args.AllowedSourceCIDRs)
		if existing.incomingToken == incoming && existing.listenHost == args.ListenHost && (args.ListenPort == 0 || existing.listenPort == args.ListenPort) && sameCIDRs {
			m.mu.Unlock()
			return map[string]any{"sessionId": existing.id, "listenHost": existing.listenHost, "listenPort": existing.listenPort, "ready": true}, nil
		}
		// Credential / CIDR / bind change reuses the durable session id. Replace
		// the old listener so the next public request uses the new policy.
		delete(m.sessions, args.SessionID)
		m.mu.Unlock()
		closeLocalLLMRelaySession(existing)
		if m.persist {
			_ = m.removePersistedLocalLLMRelay(args.SessionID)
		}
	} else {
		// A previous generic exposure may have been removed from the control
		// plane without removing its persisted relay session. Reclaim a stale
		// tracked listener when the new desired binding explicitly requests the
		// same port; otherwise reconciliation fails with EADDRINUSE.
		for id, existing := range m.sessions {
			if args.ListenPort != 0 && existing.listenPort == args.ListenPort {
				stale = existing
				delete(m.sessions, id)
				break
			}
		}
		m.mu.Unlock()
	}
	if stale != nil {
		closeLocalLLMRelaySession(stale)
		if m.persist {
			_ = m.removePersistedLocalLLMRelay(stale.id)
		}
	}
	target, _ := url.Parse(fmt.Sprintf("http://%s:%d", args.TargetHost, args.TargetPort))
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Ollama chat streams NDJSON/SSE. Default ReverseProxy buffering holds the
	// first chunk until the upstream finishes, which looks like a hung socket
	// to ai-sdk-ollama (Bun: "socket connection was closed unexpectedly").
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
		remoteIP := net.ParseIP(remoteHost)
		allowed := false
		for _, source := range args.AllowedSourceCIDRs {
			_, cidr, parseErr := net.ParseCIDR(strings.TrimSpace(source))
			if parseErr == nil && remoteIP != nil && cidr.Contains(remoteIP) {
				allowed = true
				break
			}
		}
		if len(args.AllowedSourceCIDRs) == 0 && args.AllowedSourceIP != "" && remoteIP != nil {
			allowed = remoteIP.Equal(net.ParseIP(args.AllowedSourceIP))
		}
		if err != nil || !allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Forward OpenAI-compatible /v1 and native Ollama /api (required by ai-sdk-ollama in-cluster chat).
		allowedPath := strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/v1" ||
			strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api"
		if !allowedPath {
			http.NotFound(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		incoming := args.IncomingToken
		if incoming == "" {
			incoming = args.RelayToken
		}
		want := "Bearer " + incoming
		if len(auth) != len(want) || subtle.ConstantTimeCompare([]byte(auth), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Del("Authorization")
		if strings.TrimSpace(args.UpstreamToken) != "" {
			r.Header.Set("Authorization", "Bearer "+args.UpstreamToken)
		}
		// The public hostname is only for the edge route. Do not forward it as
		// Ollama's Host header; Ollama rejects requests for that foreign host.
		r.Host = target.Host
		proxy.ServeHTTP(w, r)
	})
	listenHost := args.ListenHost
	ln, err := net.Listen("tcp", net.JoinHostPort(listenHost, strconv.Itoa(args.ListenPort)))
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	allowedSources := make([]*net.IPNet, 0, len(args.AllowedSourceCIDRs))
	for _, source := range args.AllowedSourceCIDRs {
		if _, cidr, err := net.ParseCIDR(strings.TrimSpace(source)); err == nil {
			allowedSources = append(allowedSources, cidr)
		}
	}
	incoming := args.IncomingToken
	if incoming == "" {
		incoming = args.RelayToken
	}
	session := &localLLMRelaySession{id: args.SessionID, server: &http.Server{Handler: handler}, listener: ln, allowedSources: allowedSources, incomingToken: incoming, upstreamToken: args.UpstreamToken, listenHost: listenHost, listenPort: port}
	m.mu.Lock()
	m.sessions[args.SessionID] = session
	m.mu.Unlock()
	go func() { _ = session.server.Serve(ln) }()
	if m.persist {
		if err := m.persistLocalLLMRelayArgs(args); err != nil {
			closeLocalLLMRelaySession(session)
			m.mu.Lock()
			delete(m.sessions, args.SessionID)
			m.mu.Unlock()
			return nil, err
		}
	}
	return map[string]any{"sessionId": args.SessionID, "listenHost": listenHost, "listenPort": port, "ready": true}, nil
}

func (m *localLLMRelayManager) stop(id string) bool {
	m.mu.Lock()
	session := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if session == nil {
		if m.persist {
			_ = m.removePersistedLocalLLMRelay(id)
		}
		return false
	}
	closeLocalLLMRelaySession(session)
	if m.persist {
		_ = m.removePersistedLocalLLMRelay(id)
	}
	return true
}

func (m *localLLMRelayManager) stopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stop(id)
	}
}

func localLLMRelayConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "opute", "local-llm-relays"), nil
}

func (m *localLLMRelayManager) relayConfigDir() (string, error) {
	if strings.TrimSpace(m.configDir) != "" {
		return m.configDir, nil
	}
	return localLLMRelayConfigDir()
}

func localLLMRelayConfigPathInDir(dir, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" || strings.ContainsAny(sessionID, "/\\\r\n\x00") {
		return "", fmt.Errorf("sessionId is invalid")
	}
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("relay config directory is required")
	}
	return filepath.Join(dir, sessionID+".json"), nil
}

func localLLMRelayConfigPath(sessionID string) (string, error) {
	dir, err := localLLMRelayConfigDir()
	if err != nil {
		return "", err
	}
	return localLLMRelayConfigPathInDir(dir, sessionID)
}

func (m *localLLMRelayManager) persistLocalLLMRelayArgs(args LocalLLMRelayArgs) error {
	dir, err := m.relayConfigDir()
	if err != nil {
		return err
	}
	path, err := localLLMRelayConfigPathInDir(dir, args.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	content, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0600)
}

func (m *localLLMRelayManager) removePersistedLocalLLMRelay(sessionID string) error {
	dir, err := m.relayConfigDir()
	if err != nil {
		return err
	}
	path, err := localLLMRelayConfigPathInDir(dir, sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *localLLMRelayManager) restore() {
	dir, err := m.relayConfigDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			continue
		}
		var args LocalLLMRelayArgs
		if json.Unmarshal(content, &args) != nil {
			continue
		}
		_, _ = m.start(context.Background(), args)
	}
}

func (s *HostOperationsService) EnsureLocalLLMRelay(ctx context.Context, args LocalLLMRelayArgs) (map[string]any, error) {
	return s.localLLMRelay.start(ctx, args)
}

func (s *HostOperationsService) RemoveLocalLLMRelay(id string) (map[string]any, error) {
	return map[string]any{"sessionId": id, "removed": s.localLLMRelay.stop(strings.TrimSpace(id))}, nil
}

func (s *HostOperationsService) EnsureLocalLLMK3sProxy(args LocalLLMK3sProxyArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, fmt.Errorf("vmName is invalid")
	}
	manifest, err := RenderLocalLLMK3sProxyManifestWithSecrets(args)
	if err != nil {
		return nil, err
	}
	tmpFile := fmt.Sprintf("/tmp/opute-llm-proxy-%d.yaml", time.Now().UnixNano())
	writeScript := fmt.Sprintf("cat <<'EOF' > %s\n%s\nEOF", tmpFile, manifest)
	writeRes, err := s.runVMExec(vmName, []string{"bash", "-lc", writeScript}, onData, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if writeRes.ExitCode != 0 {
		return nil, fmt.Errorf("failed to write local LLM proxy manifest")
	}
	defer func() { _, _ = s.runVMExec(vmName, []string{"rm", "-f", tmpFile}, nil, 30*time.Second) }()
	if _, err := s.runKubernetesKubectlTimed(vmName, []string{"apply", "-f", tmpFile}, "apply local LLM proxy", 3*time.Minute); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "nodePort": args.NodePort, "namespace": args.Namespace, "serviceName": args.ServiceName, "ready": true}, nil
}

func (s *HostOperationsService) RemoveLocalLLMK3sProxy(vmName, namespace string) (map[string]any, error) {
	vmName = strings.TrimSpace(vmName)
	if !safeGatewayIdentifier.MatchString(vmName) {
		return nil, fmt.Errorf("vmName is invalid")
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		// Legacy one-release default for callers that still omit namespace.
		namespace = "opute-llm"
	}
	if !safeGatewayIdentifier.MatchString(strings.ToLower(namespace)) {
		return nil, fmt.Errorf("namespace is invalid")
	}
	if _, err := s.runKubernetesKubectlTimed(vmName, []string{"delete", "namespace", namespace, "--ignore-not-found=true"}, "remove local LLM proxy", 2*time.Minute); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "namespace": namespace, "removed": true}, nil
}
