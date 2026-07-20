package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("state directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		return nil, fmt.Errorf("open standalone state: %w", err)
	}
	store := &Store{db: db}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure standalone state: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS operations (
        operation_id TEXT PRIMARY KEY,
        tool_name TEXT NOT NULL,
        status TEXT NOT NULL,
        description TEXT,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        result_json TEXT,
        error_message TEXT
    ); UPDATE operations SET status = 'unknown', updated_at = datetime('now') WHERE status = 'working';`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize standalone state: %w", err)
	}
	return store, nil
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

func (s *Store) Create(operationID, toolName, description string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO operations(operation_id, tool_name, status, description, created_at, updated_at) VALUES (?, ?, 'working', ?, ?, ?)`, operationID, toolName, description, now, now)
	return err
}

func (s *Store) Complete(operationID string, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE operations SET status = 'completed', updated_at = ?, result_json = ?, error_message = NULL WHERE operation_id = ?`, time.Now().UTC().Format(time.RFC3339Nano), string(encoded), operationID)
	return err
}

func (s *Store) Fail(operationID, message string) error {
	_, err := s.db.Exec(`UPDATE operations SET status = 'failed', updated_at = ?, error_message = ? WHERE operation_id = ?`, time.Now().UTC().Format(time.RFC3339Nano), message, operationID)
	return err
}

func (s *Store) Cancel(operationID string) error {
	_, err := s.db.Exec(`UPDATE operations SET status = 'cancelled', updated_at = ? WHERE operation_id = ? AND status IN ('working', 'unknown')`, time.Now().UTC().Format(time.RFC3339Nano), operationID)
	return err
}

func (s *Store) List(limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT operation_id, tool_name, status, description, created_at, updated_at, result_json, error_message FROM operations ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		item, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) Get(operationID string) (map[string]any, bool, error) {
	row := s.db.QueryRow(`SELECT operation_id, tool_name, status, description, created_at, updated_at, result_json, error_message FROM operations WHERE operation_id = ?`, operationID)
	item, err := scanOperation(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return item, true, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanOperation(row rowScanner) (map[string]any, error) {
	var id, tool, status, description, created, updated string
	var result, message sql.NullString
	if err := row.Scan(&id, &tool, &status, &description, &created, &updated, &result, &message); err != nil {
		return nil, err
	}
	item := map[string]any{"operationId": id, "toolName": tool, "status": status, "description": description, "createdAt": created, "updatedAt": updated}
	if result.Valid && result.String != "" {
		var parsed any
		if json.Unmarshal([]byte(result.String), &parsed) == nil {
			item["result"] = parsed
		}
	}
	if message.Valid && message.String != "" {
		item["error"] = message.String
	}
	return item, nil
}
