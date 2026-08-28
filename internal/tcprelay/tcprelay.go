// Package tcprelay is a plain TCP forwarder: it accepts on a local listener and
// pipes each connection to a target address.
//
// It lives outside the domains because two of them run one. postgres forwards a
// SQL connector port, and cluster forwards a guest bridge port; the mechanism is
// identical and neither may import the other.
package tcprelay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
)

type Manager struct {
	mu            sync.Mutex
	sessions      map[string]*Session
	portToSession map[int]string
}

// Session is one live forwarder. The address fields are exported because the
// callers report them back to whoever asked for the relay.
type Session struct {
	SessionID  string
	ListenHost string
	ListenPort int
	TargetHost string
	TargetPort int

	listener net.Listener
	activeMu *sync.Mutex
	active   map[net.Conn]struct{}
}

func (s *Session) track(conn net.Conn) {
	s.activeMu.Lock()
	s.active[conn] = struct{}{}
	s.activeMu.Unlock()
}

func (s *Session) untrack(conn net.Conn) {
	s.activeMu.Lock()
	delete(s.active, conn)
	s.activeMu.Unlock()
}

func (s *Session) ActiveConnections() []net.Conn {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	connections := make([]net.Conn, 0, len(s.active))
	for conn := range s.active {
		connections = append(connections, conn)
	}
	return connections
}

func New() *Manager {
	return &Manager{
		sessions:      make(map[string]*Session),
		portToSession: make(map[int]string),
	}
}

func (m *Manager) Start(sessionID, listenHost string, listenPort int, targetHost string, targetPort int) (Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Session{}, errors.New("sessionId is required")
	}
	targetHost = strings.TrimSpace(targetHost)
	if targetHost == "" {
		return Session{}, errors.New("targetHost is required")
	}
	if listenHost == "" {
		listenHost = "0.0.0.0"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[sessionID]; exists {
		return Session{}, fmt.Errorf("TCP relay session '%s' is already active", sessionID)
	}
	if listenPort != 0 {
		if sid, inUse := m.portToSession[listenPort]; inUse {
			return Session{}, fmt.Errorf("TCP relay listen port %d is already in use by %s", listenPort, sid)
		}
	}

	var lc net.ListenConfig
	addr := fmt.Sprintf("%s:%d", listenHost, listenPort)
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return Session{}, err
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close()
		return Session{}, errors.New("TCP relay failed to bind listener")
	}

	session := &Session{
		SessionID:  sessionID,
		ListenHost: tcpAddr.IP.String(),
		ListenPort: tcpAddr.Port,
		TargetHost: targetHost,
		TargetPort: targetPort,
		listener:   ln,
		activeMu:   &sync.Mutex{},
		active:     make(map[net.Conn]struct{}),
	}
	m.sessions[sessionID] = session
	m.portToSession[session.ListenPort] = sessionID

	go m.acceptLoop(session)
	return *session, nil
}

func (m *Manager) acceptLoop(session *Session) {
	for {
		client, err := session.listener.Accept()
		if err != nil {
			return
		}
		go m.pipe(session, client)
	}
}

func (m *Manager) pipe(session *Session, client net.Conn) {
	upstream, err := net.Dial("tcp", net.JoinHostPort(session.TargetHost, strconv.Itoa(session.TargetPort)))
	if err != nil {
		client.Close()
		return
	}
	session.track(client)
	session.track(upstream)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, client)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	}()
	go func() {
		wg.Wait()
		upstream.Close()
		client.Close()
		session.untrack(client)
		session.untrack(upstream)
	}()
}

func closeWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

func (m *Manager) Stop(sessionID string) bool {
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	delete(m.sessions, sessionID)
	delete(m.portToSession, session.ListenPort)
	m.mu.Unlock()

	for _, conn := range session.ActiveConnections() {
		conn.Close()
	}
	session.listener.Close()
	return true
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}
