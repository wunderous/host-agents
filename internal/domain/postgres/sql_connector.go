package postgres

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wunderous/host-agents/internal/tcprelay"
)

const (
	sqlConnectorMaxPerHost = 32
	sqlConnectorIdleDrain  = 120 * time.Second
)

// --- SQL connector (TCP relay) ---

type EnsureSQLConnectorArgs struct {
	DatabaseID string `json:"databaseId"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
	ListenPort int    `json:"listenPort,omitempty"`
	ListenHost string `json:"listenHost,omitempty"`
}

type SQLConnectorResult struct {
	DatabaseID string `json:"databaseId"`
	SessionID  string `json:"sessionId"`
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort"`
	PathMode   string `json:"pathMode"`
	RefCount   int    `json:"refCount"`
}

func (s *Service) EnsureSQLConnector(args EnsureSQLConnectorArgs) (SQLConnectorResult, error) {
	return s.sqlConnector.ensureConnector(args)
}

func (s *Service) GetSQLConnectorStatus(databaseID string) (map[string]any, error) {
	return s.sqlConnector.getStatus(databaseID), nil
}

func (s *Service) ReleaseSQLConnector(databaseID string, force bool) (bool, error) {
	return s.sqlConnector.releaseConnector(databaseID, force)
}

func (s *Service) StopAllHostTCPRelays() error {
	return s.sqlConnector.stopAll()
}

// --- TCP relay + SQL connector supervisor ---

type sqlConnectorSupervisor struct {
	relay    *tcprelay.Manager
	mu       sync.Mutex
	sessions map[string]*sqlConnectorSession
}

type sqlConnectorSession struct {
	databaseID string
	sessionID  string
	listenHost string
	listenPort int
	targetHost string
	targetPort int
	refCount   int
	idleTimer  *time.Timer
}

func newSQLConnectorSupervisor() *sqlConnectorSupervisor {
	return &sqlConnectorSupervisor{
		relay:    tcprelay.New(),
		sessions: make(map[string]*sqlConnectorSession),
	}
}

func (s *sqlConnectorSupervisor) sessionIDForDatabase(databaseID string) string {
	return "sql-connector:" + strings.TrimSpace(databaseID)
}

func (s *sqlConnectorSupervisor) ensureConnector(args EnsureSQLConnectorArgs) (SQLConnectorResult, error) {
	databaseID := strings.TrimSpace(args.DatabaseID)
	if databaseID == "" {
		return SQLConnectorResult{}, errors.New("databaseId is required")
	}

	s.mu.Lock()
	if existing, ok := s.sessions[databaseID]; ok {
		if existing.idleTimer != nil {
			existing.idleTimer.Stop()
			existing.idleTimer = nil
		}
		existing.refCount++
		res := SQLConnectorResult{
			DatabaseID: databaseID,
			SessionID:  existing.sessionID,
			ListenHost: existing.listenHost,
			ListenPort: existing.listenPort,
			PathMode:   "host_tcp_relay",
			RefCount:   existing.refCount,
		}
		s.mu.Unlock()
		return res, nil
	}
	if len(s.sessions) >= sqlConnectorMaxPerHost {
		s.mu.Unlock()
		return SQLConnectorResult{}, fmt.Errorf("host SQL connector limit reached (%d)", sqlConnectorMaxPerHost)
	}
	s.mu.Unlock()

	sessionID := s.sessionIDForDatabase(databaseID)
	relay, err := s.relay.Start(sessionID, args.ListenHost, args.ListenPort, args.TargetHost, args.TargetPort)
	if err != nil {
		return SQLConnectorResult{}, err
	}

	s.mu.Lock()
	s.sessions[databaseID] = &sqlConnectorSession{
		databaseID: databaseID,
		sessionID:  sessionID,
		listenHost: relay.ListenHost,
		listenPort: relay.ListenPort,
		targetHost: relay.TargetHost,
		targetPort: relay.TargetPort,
		refCount:   1,
	}
	s.mu.Unlock()

	return SQLConnectorResult{
		DatabaseID: databaseID,
		SessionID:  sessionID,
		ListenHost: relay.ListenHost,
		ListenPort: relay.ListenPort,
		PathMode:   "host_tcp_relay",
		RefCount:   1,
	}, nil
}

func (s *sqlConnectorSupervisor) getStatus(databaseID string) map[string]any {
	databaseID = strings.TrimSpace(databaseID)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[databaseID]
	if !ok {
		return map[string]any{"databaseId": databaseID, "active": false, "refCount": 0}
	}
	return map[string]any{
		"databaseId": databaseID,
		"active":     true,
		"sessionId":  session.sessionID,
		"listenHost": session.listenHost,
		"listenPort": session.listenPort,
		"refCount":   session.refCount,
		"targetHost": session.targetHost,
		"targetPort": session.targetPort,
	}
}

func (s *sqlConnectorSupervisor) releaseConnector(databaseID string, force bool) (bool, error) {
	databaseID = strings.TrimSpace(databaseID)
	s.mu.Lock()
	session, ok := s.sessions[databaseID]
	if !ok {
		s.mu.Unlock()
		return false, nil
	}
	if force {
		s.mu.Unlock()
		s.drainSession(databaseID)
		return true, nil
	}
	session.refCount--
	if session.refCount > 0 {
		s.mu.Unlock()
		return true, nil
	}
	if session.idleTimer != nil {
		session.idleTimer.Stop()
	}
	dbID := databaseID
	session.idleTimer = time.AfterFunc(sqlConnectorIdleDrain, func() {
		s.mu.Lock()
		current, ok := s.sessions[dbID]
		if !ok || current.refCount > 0 {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		s.drainSession(dbID)
	})
	s.mu.Unlock()
	return true, nil
}

func (s *sqlConnectorSupervisor) drainSession(databaseID string) {
	s.mu.Lock()
	session, ok := s.sessions[databaseID]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.sessions, databaseID)
	if session.idleTimer != nil {
		session.idleTimer.Stop()
	}
	sessionID := session.sessionID
	s.mu.Unlock()
	s.relay.Stop(sessionID)
}

func (s *sqlConnectorSupervisor) stopAll() error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.sessions = make(map[string]*sqlConnectorSession)
	s.mu.Unlock()
	for _, id := range ids {
		s.drainSession(id)
	}
	s.relay.StopAll()
	return nil
}
