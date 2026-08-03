package ops

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type platformPostgresRelaySession struct {
	id          string
	tokenHash   [sha256.Size]byte
	listener    net.Listener
	targetHost  string
	targetPort  int
	expiresAt   time.Time
	connections map[net.Conn]struct{}
	connMu      sync.Mutex
	closed      bool
}

type platformPostgresRelayManager struct {
	mu       sync.Mutex
	sessions map[string]*platformPostgresRelaySession
}

func newPlatformPostgresRelayManager() *platformPostgresRelayManager {
	return &platformPostgresRelayManager{sessions: make(map[string]*platformPostgresRelaySession)}
}

func (m *platformPostgresRelayManager) start(args PlatformPostgresRelayArgs, targetHost string, targetPort int) (map[string]any, error) {
	sessionID := strings.TrimSpace(args.SessionID)
	if sessionID == "" {
		return nil, errors.New("localRelay.sessionId is required")
	}
	token := strings.TrimSpace(args.RelayToken)
	if len(token) < 32 {
		return nil, errors.New("localRelay.relayToken must contain at least 32 characters")
	}
	listenHost := strings.TrimSpace(args.ListenHost)
	if listenHost == "" {
		listenHost = "127.0.0.1"
	}
	if ip := net.ParseIP(listenHost); ip == nil || !ip.IsLoopback() {
		return nil, errors.New("localRelay.listenHost must be a loopback IP")
	}
	if args.ListenPort < 0 || args.ListenPort > 65535 {
		return nil, errors.New("localRelay.listenPort must be between 0 and 65535")
	}
	if targetHost = strings.TrimSpace(targetHost); targetHost == "" {
		return nil, errors.New("localRelay.targetHost is required")
	}
	if targetPort <= 0 || targetPort > 65535 {
		return nil, errors.New("localRelay.targetPort must be between 1 and 65535")
	}
	ttl := time.Duration(args.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	if ttl < 10*time.Second || ttl > time.Hour {
		return nil, errors.New("localRelay.ttlSeconds must be between 10 and 3600")
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(listenHost, strconv.Itoa(args.ListenPort)))
	if err != nil {
		return nil, fmt.Errorf("listen for platform PostgreSQL relay: %w", err)
	}
	hash := sha256.Sum256([]byte(token))
	session := &platformPostgresRelaySession{
		id:          sessionID,
		tokenHash:   hash,
		listener:    listener,
		targetHost:  targetHost,
		targetPort:  targetPort,
		expiresAt:   time.Now().Add(ttl),
		connections: make(map[net.Conn]struct{}),
	}

	m.mu.Lock()
	old := m.sessions[sessionID]
	m.sessions[sessionID] = session
	m.mu.Unlock()
	if old != nil {
		old.close()
	}

	go m.acceptLoop(session)
	go func() {
		<-time.After(ttl)
		m.stopIfCurrent(sessionID, session)
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	return map[string]any{
		"sessionId":  sessionID,
		"listenHost": listenHost,
		"listenPort": port,
		"targetPort": targetPort,
		"authMode":   "token-line",
		"expiresAt":  session.expiresAt.UTC().Format(time.RFC3339Nano),
		"persistent": false,
		"ready":      true,
	}, nil
}

func (m *platformPostgresRelayManager) acceptLoop(session *platformPostgresRelaySession) {
	for {
		conn, err := session.listener.Accept()
		if err != nil {
			return
		}
		go m.handleConnection(session, conn)
	}
}

func (m *platformPostgresRelayManager) handleConnection(session *platformPostgresRelaySession, client net.Conn) {
	defer client.Close()
	if !session.addConnection(client) || time.Now().After(session.expiresAt) {
		return
	}
	defer session.removeConnection(client)
	reader := bufio.NewReader(client)
	line, err := readPlatformPostgresRelayTokenLine(reader)
	if err != nil {
		return
	}
	presented := strings.TrimSuffix(line, "\r")
	presentedHash := sha256.Sum256([]byte(presented))
	if subtle.ConstantTimeCompare(presentedHash[:], session.tokenHash[:]) != 1 {
		return
	}
	if !session.isOpen() || time.Now().After(session.expiresAt) {
		return
	}
	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(session.targetHost, strconv.Itoa(session.targetPort)), 10*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	// The authentication line is deliberately consumed and is never forwarded
	// to PostgreSQL. The caller must use the relay-aware connection wrapper.
	errCh := make(chan error, 2)
	go func() { _, err := io.Copy(upstream, reader); errCh <- err }()
	go func() { _, err := io.Copy(client, upstream); errCh <- err }()
	<-errCh
}

func readPlatformPostgresRelayTokenLine(reader *bufio.Reader) (string, error) {
	line := make([]byte, 0, 64)
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if value == '\n' {
			return string(line), nil
		}
		if len(line) >= 512 {
			return "", errors.New("platform PostgreSQL relay token line is too long")
		}
		line = append(line, value)
	}
}

func (s *platformPostgresRelaySession) addConnection(conn net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.closed {
		return false
	}
	s.connections[conn] = struct{}{}
	return true
}

func (s *platformPostgresRelaySession) removeConnection(conn net.Conn) {
	s.connMu.Lock()
	delete(s.connections, conn)
	s.connMu.Unlock()
}

func (s *platformPostgresRelaySession) isOpen() bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return !s.closed
}

func (s *platformPostgresRelaySession) close() {
	_ = s.listener.Close()
	s.connMu.Lock()
	s.closed = true
	connections := make([]net.Conn, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	s.connections = make(map[net.Conn]struct{})
	s.connMu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

func (m *platformPostgresRelayManager) stop(sessionID string) bool {
	return m.stopIfCurrent(sessionID, nil)
}

func (m *platformPostgresRelayManager) stopIfCurrent(sessionID string, expected *platformPostgresRelaySession) bool {
	m.mu.Lock()
	session := m.sessions[sessionID]
	if session == nil || (expected != nil && session != expected) {
		m.mu.Unlock()
		return false
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	session.close()
	return true
}

func (m *platformPostgresRelayManager) stopAll() {
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

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve loopback port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port, nil
}

// ensurePlatformPostgresHostForward creates an Incus proxy device on the K3s
// guest so the CNPG read/write Service is reachable on the host loopback. The
// connect target is dialed from the container network namespace, where the
// ClusterIP is routable through flannel; the host itself cannot resolve the
// in-cluster Service DNS. The device is idempotent: an existing device whose
// connect target matches the current Service ClusterIP is reused so concurrent
// relays keep working; only a stale target triggers a recreate.
func (s *HostOperationsService) ensurePlatformPostgresHostForward(ctx context.Context, spec platformPostgresSpec) (int, error) {
	service, err := s.platformPostgresJSON(ctx, spec, []string{"get", "service", spec.ClusterName + "-rw", "-n", spec.Namespace}, "get platform PostgreSQL read/write service")
	if err != nil {
		return 0, err
	}
	clusterIP := nestedString(service, "spec", "clusterIP")
	if clusterIP == "" {
		return 0, errors.New("platform PostgreSQL read/write Service has no clusterIP")
	}
	deviceName := "opute-platform-postgres-rw"
	existing, showErr := s.commandRunner([]string{"config", "device", "show", spec.VMName}, nil, 30*time.Second)
	if showErr == nil && existing.ExitCode == 0 {
		if strings.Contains(existing.Stdout, "connect: tcp:"+clusterIP+":"+strconv.Itoa(platformPostgresServicePort)) {
			if port := extractProxyDeviceListenPort(existing.Stdout); port > 0 {
				return port, nil
			}
		}
	}
	_, _ = s.commandRunner([]string{"config", "device", "remove", spec.VMName, deviceName}, nil, 2*time.Minute)
	port, err := freeLoopbackPort()
	if err != nil {
		return 0, err
	}
	added, err := s.commandRunner([]string{
		"config", "device", "add", spec.VMName, deviceName, "proxy",
		fmt.Sprintf("listen=tcp:127.0.0.1:%d", port),
		fmt.Sprintf("connect=tcp:%s:%d", clusterIP, platformPostgresServicePort),
	}, nil, 2*time.Minute)
	if err != nil || added.ExitCode != 0 {
		return 0, errors.New(firstNonEmpty(added.Stderr, added.Stdout, errString(err, "add platform PostgreSQL loopback forward failed")))
	}
	return port, nil
}

func extractProxyDeviceListenPort(output string) int {
	index := strings.Index(output, "listen: tcp:127.0.0.1:")
	if index < 0 {
		return 0
	}
	rest := output[index+len("listen: tcp:127.0.0.1:"):]
	end := strings.IndexAny(rest, "\n\r")
	if end < 0 {
		end = len(rest)
	}
	port, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

func (s *HostOperationsService) ensurePlatformPostgresRelay(ctx context.Context, spec platformPostgresSpec, args PlatformPostgresRelayArgs) (map[string]any, error) {
	targetHost := strings.TrimSpace(args.TargetHost)
	targetPort := args.TargetPort
	if targetHost == "" {
		// Host-agent-managed forward: create the loopback proxy device and
		// relay to it. Callers that already hold their own loopback
		// port-forward may pass it explicitly.
		forwardPort, err := s.ensurePlatformPostgresHostForward(ctx, spec)
		if err != nil {
			return nil, err
		}
		targetHost = "127.0.0.1"
		targetPort = forwardPort
	}
	if targetPort == 0 {
		targetPort = platformPostgresServicePort
	}
	expectedServiceHost := spec.ClusterName + "-rw." + spec.Namespace + ".svc"
	if targetHost != expectedServiceHost && targetHost != "127.0.0.1" && targetHost != "::1" {
		return nil, errors.New("localRelay.targetHost must be the CNPG read/write Service or a loopback port-forward")
	}
	if targetHost == expectedServiceHost && targetPort != platformPostgresServicePort {
		return nil, fmt.Errorf("localRelay.targetPort must be %d when targeting the read/write Service", platformPostgresServicePort)
	}
	return s.platformPostgresRelay.start(args, targetHost, targetPort)
}

func (s *HostOperationsService) revokeAllPlatformPostgresRelays() {
	if s.platformPostgresRelay != nil {
		s.platformPostgresRelay.stopAll()
	}
}
