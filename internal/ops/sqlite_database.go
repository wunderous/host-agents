package ops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

var sqliteDatabaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// SQLiteDatabaseArgs identifies one caller-owned database. The host agent
// treats ConsumerID and DatabaseName as opaque, validated path components;
// schema, migrations, and application semantics remain caller-owned.
type SQLiteDatabaseArgs struct {
	ConsumerID   string `json:"consumerId"`
	DatabaseName string `json:"databaseName"`
}

type SQLiteDatabaseResult struct {
	Provider     string `json:"provider"`
	ConsumerID   string `json:"consumerId"`
	DatabaseName string `json:"databaseName"`
	Path         string `json:"path"`
	Exists       bool   `json:"exists"`
	Created      bool   `json:"created"`
}

func (s *HostOperationsService) sqliteDatabasePath(args SQLiteDatabaseArgs) (string, error) {
	consumerID := strings.TrimSpace(args.ConsumerID)
	databaseName := strings.TrimSpace(args.DatabaseName)
	if !sqliteDatabaseNamePattern.MatchString(consumerID) {
		return "", fmt.Errorf("invalid SQLite consumerId")
	}
	if !sqliteDatabaseNamePattern.MatchString(databaseName) {
		return "", fmt.Errorf("invalid SQLite databaseName")
	}
	root := strings.TrimSpace(s.sqliteDatabaseRoot)
	if root == "" {
		return "", errors.New("SQLite database root is not configured")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve SQLite database root: %w", err)
	}
	path := filepath.Join(root, consumerID, databaseName+".sqlite")
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", errors.New("SQLite database path escapes the managed root")
	}
	return path, nil
}

func (s *HostOperationsService) EnsureSQLiteDatabase(ctx context.Context, args SQLiteDatabaseArgs) (SQLiteDatabaseResult, error) {
	path, err := s.sqliteDatabasePath(args)
	if err != nil {
		return SQLiteDatabaseResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return SQLiteDatabaseResult{}, fmt.Errorf("create SQLite database directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)
	if statErr != nil && !created {
		return SQLiteDatabaseResult{}, fmt.Errorf("inspect SQLite database: %w", statErr)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return SQLiteDatabaseResult{}, fmt.Errorf("open SQLite database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return SQLiteDatabaseResult{}, fmt.Errorf("ping SQLite database: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;"); err != nil {
		return SQLiteDatabaseResult{}, fmt.Errorf("configure SQLite database: %w", err)
	}
	return SQLiteDatabaseResult{Provider: "sqlite", ConsumerID: strings.TrimSpace(args.ConsumerID), DatabaseName: strings.TrimSpace(args.DatabaseName), Path: path, Exists: true, Created: created}, nil
}

func (s *HostOperationsService) GetSQLiteDatabaseStatus(ctx context.Context, args SQLiteDatabaseArgs) (SQLiteDatabaseResult, error) {
	path, err := s.sqliteDatabasePath(args)
	if err != nil {
		return SQLiteDatabaseResult{}, err
	}
	_, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return SQLiteDatabaseResult{}, fmt.Errorf("inspect SQLite database: %w", statErr)
	}
	return SQLiteDatabaseResult{Provider: "sqlite", ConsumerID: strings.TrimSpace(args.ConsumerID), DatabaseName: strings.TrimSpace(args.DatabaseName), Path: path, Exists: statErr == nil}, nil
}

func (s *HostOperationsService) RemoveSQLiteDatabase(ctx context.Context, args SQLiteDatabaseArgs, confirm bool) (SQLiteDatabaseResult, error) {
	if !confirm {
		return SQLiteDatabaseResult{}, errors.New("remove SQLite database requires confirm=true")
	}
	path, err := s.sqliteDatabasePath(args)
	if err != nil {
		return SQLiteDatabaseResult{}, err
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return SQLiteDatabaseResult{}, fmt.Errorf("remove SQLite database file: %w", err)
		}
	}
	return SQLiteDatabaseResult{Provider: "sqlite", ConsumerID: strings.TrimSpace(args.ConsumerID), DatabaseName: strings.TrimSpace(args.DatabaseName), Path: path, Exists: false}, nil
}
