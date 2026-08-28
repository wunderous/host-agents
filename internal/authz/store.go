package authz

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

type issuedToken struct {
	Hash      string
	ClientID  string
	Resource  string
	Scope     string
	ExpiresAt int64
	Revoked   bool
}

type authCode struct {
	Code          string
	ClientID      string
	Resource      string
	CodeChallenge string
	RedirectURI   string
	ExpiresAt     int64
}

type registeredClient struct {
	ClientID     string
	SecretHash   string
	ClientType   string
	RedirectURIs []string
	MetadataURL  string
	Confidential bool
}

func OpenStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create authz state dir: %w", err)
	}
	path := filepath.Join(dir, "authz.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open authz store: %w", err)
	}
	if _, err := db.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
		CREATE TABLE IF NOT EXISTS clients (
			client_id TEXT PRIMARY KEY,
			secret_hash TEXT NOT NULL DEFAULT '',
			client_type TEXT NOT NULL,
			redirect_uris TEXT NOT NULL DEFAULT '[]',
			metadata_url TEXT NOT NULL DEFAULT '',
			confidential INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS codes (
			code TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			resource TEXT NOT NULL,
			code_challenge TEXT NOT NULL,
			redirect_uri TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			used INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS tokens (
			token_hash TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			resource TEXT NOT NULL,
			scope TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			revoked INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init authz store: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.db != nil {
			s.closeErr = s.db.Close()
		}
	})
	return s.closeErr
}

func (s *Store) upsertClient(client registeredClient) error {
	raw, _ := json.Marshal(client.RedirectURIs)
	confidential := 0
	if client.Confidential {
		confidential = 1
	}
	_, err := s.db.Exec(`INSERT INTO clients(client_id, secret_hash, client_type, redirect_uris, metadata_url, confidential, created_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(client_id) DO UPDATE SET secret_hash=excluded.secret_hash, client_type=excluded.client_type,
			redirect_uris=excluded.redirect_uris, metadata_url=excluded.metadata_url, confidential=excluded.confidential`,
		client.ClientID, client.SecretHash, client.ClientType, string(raw), client.MetadataURL, confidential, time.Now().Unix())
	return err
}

func (s *Store) client(clientID string) (registeredClient, bool, error) {
	var client registeredClient
	var raw string
	var confidential int
	err := s.db.QueryRow(`SELECT client_id, secret_hash, client_type, redirect_uris, metadata_url, confidential FROM clients WHERE client_id=?`, clientID).
		Scan(&client.ClientID, &client.SecretHash, &client.ClientType, &raw, &client.MetadataURL, &confidential)
	if err == sql.ErrNoRows {
		return registeredClient{}, false, nil
	}
	if err != nil {
		return registeredClient{}, false, err
	}
	_ = json.Unmarshal([]byte(raw), &client.RedirectURIs)
	client.Confidential = confidential == 1
	return client, true, nil
}

func (s *Store) saveCode(code authCode) error {
	_, err := s.db.Exec(`INSERT INTO codes(code, client_id, resource, code_challenge, redirect_uri, expires_at, used) VALUES(?,?,?,?,?,?,0)`,
		code.Code, code.ClientID, code.Resource, code.CodeChallenge, code.RedirectURI, code.ExpiresAt)
	return err
}

func (s *Store) consumeCode(value string) (authCode, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return authCode{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var code authCode
	var used int
	err = tx.QueryRow(`SELECT code, client_id, resource, code_challenge, redirect_uri, expires_at, used FROM codes WHERE code=?`, value).
		Scan(&code.Code, &code.ClientID, &code.Resource, &code.CodeChallenge, &code.RedirectURI, &code.ExpiresAt, &used)
	if err == sql.ErrNoRows {
		return authCode{}, fmt.Errorf("authorization code is invalid")
	}
	if err != nil {
		return authCode{}, err
	}
	if used != 0 || time.Now().Unix() > code.ExpiresAt {
		return authCode{}, fmt.Errorf("authorization code is expired")
	}
	if _, err := tx.Exec(`UPDATE codes SET used=1 WHERE code=?`, value); err != nil {
		return authCode{}, err
	}
	if err := tx.Commit(); err != nil {
		return authCode{}, err
	}
	return code, nil
}

func (s *Store) saveToken(token issuedToken) error {
	_, err := s.db.Exec(`INSERT INTO tokens(token_hash, client_id, resource, scope, expires_at, revoked, created_at) VALUES(?,?,?,?,?,?,?)`,
		token.Hash, token.ClientID, token.Resource, token.Scope, token.ExpiresAt, boolToInt(token.Revoked), time.Now().Unix())
	return err
}

func (s *Store) tokenByHash(hash string) (issuedToken, bool, error) {
	var token issuedToken
	var revoked int
	err := s.db.QueryRow(`SELECT token_hash, client_id, resource, scope, expires_at, revoked FROM tokens WHERE token_hash=?`, hash).
		Scan(&token.Hash, &token.ClientID, &token.Resource, &token.Scope, &token.ExpiresAt, &revoked)
	if err == sql.ErrNoRows {
		return issuedToken{}, false, nil
	}
	if err != nil {
		return issuedToken{}, false, err
	}
	token.Revoked = revoked == 1
	return token, true, nil
}

func (s *Store) revokeHash(hash string) error {
	_, err := s.db.Exec(`UPDATE tokens SET revoked=1 WHERE token_hash=?`, hash)
	return err
}

func (s *Store) revokeClient(clientID string) error {
	_, err := s.db.Exec(`UPDATE tokens SET revoked=1 WHERE client_id=?`, clientID)
	return err
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return HostTokenPrefix + hex.EncodeToString(raw[:]), nil
}

func randomCode() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func validNativeRedirect(uri string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme == "http" && IsLocalHostAddress(parsed.Host) {
		return true
	}
	return false
}
