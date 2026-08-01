package ops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const platformPostgresServiceName = "opute-platform-postgres.service"

// PlatformPostgresArgs describes the host-owned CPC PostgreSQL instance. The
// default is intentionally loopback-only; a dogfood deployment must opt into a
// host-reachable bind and configure its firewall/pg_hba policy separately.
type PlatformPostgresArgs struct {
	DataDir          string   `json:"dataDir,omitempty"`
	BindHost         string   `json:"bindHost,omitempty"`
	Port             int      `json:"port,omitempty"`
	AllowedCIDRs     []string `json:"allowedCidrs,omitempty"`
	InstallIfMissing *bool    `json:"installIfMissing,omitempty"`
}

type platformPostgresCredentials struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	CreatedAt string `json:"createdAt"`
}

type platformPostgresPaths struct {
	DataDir       string
	ConfigDir     string
	CredentialRef string
	PgPassFile    string
	UnitPath      string
	Username      string
	BindHost      string
	Port          int
	AllowedCIDRs  []string
}

// RenderPlatformPostgresSystemdUnit is kept pure so the lifecycle contract can
// be tested without starting a database or mutating the host.
func RenderPlatformPostgresSystemdUnit(postgresPath, pgCtlPath string, paths platformPostgresPaths) string {
	return fmt.Sprintf(`[Unit]
Description=Opute platform PostgreSQL (host-owned CPC store)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s -D %s -c config_file=%s
ExecStop=%s -D %s stop -m fast
Restart=on-failure
RestartSec=3
TimeoutStartSec=120
TimeoutStopSec=120
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
`, shellEscape(postgresPath), shellEscape(paths.DataDir), shellEscape(filepath.Join(paths.DataDir, "postgresql.conf")), shellEscape(pgCtlPath), shellEscape(paths.DataDir))
}

func defaultPlatformPostgresPaths(args PlatformPostgresArgs) (platformPostgresPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return platformPostgresPaths{}, fmt.Errorf("resolve host home: %w", err)
	}
	dataDir := strings.TrimSpace(args.DataDir)
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv("OPUTE_PLATFORM_POSTGRES_DATA_DIR"))
	}
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local", "share", "opute", "platform-postgres")
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil || dataDir == string(filepath.Separator) {
		return platformPostgresPaths{}, fmt.Errorf("invalid platform PostgreSQL data directory")
	}
	configDir := filepath.Join(home, ".config", "opute", "platform-postgres")
	port := args.Port
	if port == 0 {
		port = 5433
	}
	if port < 1024 || port > 65535 {
		return platformPostgresPaths{}, fmt.Errorf("platform PostgreSQL port must be between 1024 and 65535")
	}
	bindHost := strings.TrimSpace(args.BindHost)
	if bindHost == "" {
		bindHost = strings.TrimSpace(os.Getenv("OPUTE_PLATFORM_POSTGRES_BIND_HOST"))
	}
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	if strings.ContainsAny(bindHost, "\r\n\x00 ';") {
		return platformPostgresPaths{}, fmt.Errorf("invalid platform PostgreSQL bind host")
	}
	if net.ParseIP(bindHost) == nil && !isSafePlatformPostgresHostName(bindHost) {
		return platformPostgresPaths{}, fmt.Errorf("invalid platform PostgreSQL bind host")
	}
	allowedCIDRs := make([]string, 0, len(args.AllowedCIDRs))
	for _, cidr := range args.AllowedCIDRs {
		if strings.ContainsAny(cidr, "\r\n\x00'") {
			return platformPostgresPaths{}, fmt.Errorf("invalid platform PostgreSQL allowed CIDR")
		}
		cidr = strings.TrimSpace(cidr)
		if cidr == "" || strings.ContainsAny(cidr, "\x00 '") {
			return platformPostgresPaths{}, fmt.Errorf("invalid platform PostgreSQL allowed CIDR")
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return platformPostgresPaths{}, fmt.Errorf("invalid platform PostgreSQL allowed CIDR")
		}
		allowedCIDRs = append(allowedCIDRs, cidr)
	}
	return platformPostgresPaths{
		DataDir:       dataDir,
		ConfigDir:     configDir,
		CredentialRef: filepath.Join(configDir, "credentials.json"),
		PgPassFile:    filepath.Join(configDir, "pgpass"),
		UnitPath:      filepath.Join(home, ".config", "systemd", "user", platformPostgresServiceName),
		Username:      "opute",
		BindHost:      bindHost,
		Port:          port,
		AllowedCIDRs:  allowedCIDRs,
	}, nil
}

func isSafePlatformPostgresHostName(value string) bool {
	if value == "localhost" {
		return true
	}
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func readPlatformPostgresCredentials(paths platformPostgresPaths) (platformPostgresCredentials, error) {
	data, err := os.ReadFile(paths.CredentialRef)
	if err != nil {
		return platformPostgresCredentials{}, fmt.Errorf("read platform PostgreSQL credential reference: %w", err)
	}
	var credentials platformPostgresCredentials
	if err := json.Unmarshal(data, &credentials); err != nil {
		return platformPostgresCredentials{}, fmt.Errorf("parse platform PostgreSQL credentials: %w", err)
	}
	if credentials.Username == "" || credentials.Password == "" {
		return platformPostgresCredentials{}, fmt.Errorf("platform PostgreSQL credential reference is incomplete")
	}
	return credentials, nil
}

func writePlatformPostgresCredentials(paths platformPostgresPaths) (platformPostgresCredentials, error) {
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		return platformPostgresCredentials{}, fmt.Errorf("create platform PostgreSQL config directory: %w", err)
	}
	credentials := platformPostgresCredentials{Username: paths.Username, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return platformPostgresCredentials{}, fmt.Errorf("generate platform PostgreSQL credential: %w", err)
	}
	credentials.Password = hex.EncodeToString(bytes)
	payload, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return platformPostgresCredentials{}, fmt.Errorf("encode platform PostgreSQL credential: %w", err)
	}
	if err := os.WriteFile(paths.CredentialRef, append(payload, '\n'), 0o600); err != nil {
		return platformPostgresCredentials{}, fmt.Errorf("write platform PostgreSQL credential: %w", err)
	}
	if err := os.Chmod(paths.CredentialRef, 0o600); err != nil {
		return platformPostgresCredentials{}, fmt.Errorf("lock down platform PostgreSQL credential: %w", err)
	}
	return credentials, nil
}

func ensurePlatformPostgresCredentials(paths platformPostgresPaths) (platformPostgresCredentials, error) {
	credentials, err := readPlatformPostgresCredentials(paths)
	if err == nil {
		return credentials, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return platformPostgresCredentials{}, err
	}
	return writePlatformPostgresCredentials(paths)
}

func (s *HostOperationsService) resolveHostBinary(ctx context.Context, name string) (string, error) {
	res, err := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", "command -v " + shellEscape(name) + " || find /usr/lib/postgresql -type f -name " + shellEscape(name) + " -perm -111 2>/dev/null | sort -V | tail -n 1"}, nil, 20*time.Second)
	if err != nil || res.ExitCode != 0 {
		return "", fmt.Errorf("resolve host binary %s: %s", name, firstNonEmpty(res.Stderr, res.Stdout, errString(err, "not found")))
	}
	path := strings.TrimSpace(res.Stdout)
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return "", fmt.Errorf("host binary %s was not found", name)
	}
	return path, nil
}

func (s *HostOperationsService) installPlatformPostgresPackages(ctx context.Context, onData func(string)) error {
	res, err := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", "sudo -n apt-get update && sudo -n env DEBIAN_FRONTEND=noninteractive apt-get install -y postgresql postgresql-client"}, onData, 10*time.Minute)
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("install host PostgreSQL packages: %s", firstNonEmpty(res.Stderr, res.Stdout, errString(err, "command failed")))
	}
	return nil
}

func writePlatformPostgresConfig(paths platformPostgresPaths) error {
	if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
		return fmt.Errorf("create platform PostgreSQL data directory: %w", err)
	}
	listenAddresses := paths.BindHost
	if paths.BindHost != "127.0.0.1" && paths.BindHost != "localhost" && paths.BindHost != "0.0.0.0" {
		listenAddresses += ",127.0.0.1"
	}
	config := fmt.Sprintf("listen_addresses = '%s'\nport = %d\nunix_socket_directories = '%s'\npassword_encryption = 'scram-sha-256'\n",
		strings.ReplaceAll(listenAddresses, "'", "''"), paths.Port, strings.ReplaceAll(paths.ConfigDir, "'", "''"))
	if err := os.WriteFile(filepath.Join(paths.DataDir, "postgresql.conf"), []byte(config), 0o600); err != nil {
		return fmt.Errorf("write platform PostgreSQL config: %w", err)
	}
	allowed := []string{"127.0.0.1/32", "::1/128"}
	if bindIP := net.ParseIP(paths.BindHost); bindIP != nil && !bindIP.IsLoopback() && !bindIP.IsUnspecified() {
		mask := "/128"
		if bindIP.To4() != nil {
			mask = "/32"
		}
		allowed = append(allowed, paths.BindHost+mask)
	}
	for _, cidr := range paths.AllowedCIDRs {
		if strings.TrimSpace(cidr) != "" {
			allowed = append(allowed, cidr)
		}
	}
	hba := "local all all trust\n"
	for _, cidr := range allowed {
		hba += fmt.Sprintf("host all all %s scram-sha-256\n", cidr)
	}
	if err := os.WriteFile(filepath.Join(paths.DataDir, "pg_hba.conf"), []byte(hba), 0o600); err != nil {
		return fmt.Errorf("write platform PostgreSQL pg_hba.conf: %w", err)
	}
	return nil
}

func (s *HostOperationsService) runPlatformPostgresSQL(ctx context.Context, paths platformPostgresPaths, credentials platformPostgresCredentials, database, sql string) error {
	psql, err := s.resolveHostBinary(ctx, "psql")
	if err != nil {
		return err
	}
	command := fmt.Sprintf("PGPASSFILE=%s %s -h 127.0.0.1 -p %d -U %s -d %s -v ON_ERROR_STOP=1 -Atqc %s", shellEscape(paths.PgPassFile), shellEscape(psql), paths.Port, shellEscape(credentials.Username), shellEscape(database), shellEscape(sql))
	res, runErr := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", command}, nil, 30*time.Second)
	if runErr != nil || res.ExitCode != 0 {
		return fmt.Errorf("platform PostgreSQL query failed: %s", firstNonEmpty(res.Stderr, res.Stdout, errString(runErr, "command failed")))
	}
	return nil
}

func (s *HostOperationsService) writePlatformPostgresPgPass(paths platformPostgresPaths, credentials platformPostgresCredentials) error {
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("create platform PostgreSQL credential directory: %w", err)
	}
	line := fmt.Sprintf("*:%d:*:%s:%s\n", paths.Port, credentials.Username, credentials.Password)
	if err := os.WriteFile(paths.PgPassFile, []byte(line), 0o600); err != nil {
		return fmt.Errorf("write platform PostgreSQL pgpass file: %w", err)
	}
	return nil
}

func (s *HostOperationsService) ensurePlatformPostgresService(ctx context.Context, paths platformPostgresPaths, postgresPath, pgCtlPath string) error {
	unit := RenderPlatformPostgresSystemdUnit(postgresPath, pgCtlPath, paths)
	if err := os.MkdirAll(filepath.Dir(paths.UnitPath), 0o700); err != nil {
		return fmt.Errorf("create user systemd directory: %w", err)
	}
	if err := os.WriteFile(paths.UnitPath, []byte(unit), 0o600); err != nil {
		return fmt.Errorf("write platform PostgreSQL systemd unit: %w", err)
	}
	res, err := s.hostCommandRunnerContext(ctx, []string{"systemctl", "--user", "daemon-reload"}, nil, 20*time.Second)
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("reload user systemd: %s", firstNonEmpty(res.Stderr, res.Stdout, errString(err, "command failed")))
	}
	res, err = s.hostCommandRunnerContext(ctx, []string{"systemctl", "--user", "enable", "--now", platformPostgresServiceName}, nil, 90*time.Second)
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("start platform PostgreSQL service: %s", firstNonEmpty(res.Stderr, res.Stdout, errString(err, "command failed")))
	}
	return nil
}

func (s *HostOperationsService) EnsurePlatformPostgres(ctx context.Context, args PlatformPostgresArgs, onData func(string)) (map[string]any, error) {
	paths, err := defaultPlatformPostgresPaths(args)
	if err != nil {
		return nil, err
	}
	postgresPath, postgresErr := s.resolveHostBinary(ctx, "postgres")
	pgCtlPath, pgCtlErr := s.resolveHostBinary(ctx, "pg_ctl")
	initdbPath, initdbErr := s.resolveHostBinary(ctx, "initdb")
	if postgresErr != nil || pgCtlErr != nil || initdbErr != nil {
		install := args.InstallIfMissing == nil || *args.InstallIfMissing
		if !install {
			return nil, fmt.Errorf("host PostgreSQL is not installed; install postgresql or set installIfMissing=true")
		}
		if err := s.installPlatformPostgresPackages(ctx, onData); err != nil {
			return nil, err
		}
		postgresPath, err = s.resolveHostBinary(ctx, "postgres")
		if err != nil {
			return nil, err
		}
		pgCtlPath, err = s.resolveHostBinary(ctx, "pg_ctl")
		if err != nil {
			return nil, err
		}
		initdbPath, err = s.resolveHostBinary(ctx, "initdb")
		if err != nil {
			return nil, err
		}
	}
	credentials, err := ensurePlatformPostgresCredentials(paths)
	if err != nil {
		return nil, err
	}
	if err := s.writePlatformPostgresPgPass(paths, credentials); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(paths.DataDir, "PG_VERSION")); os.IsNotExist(err) {
		pwFile := filepath.Join(paths.ConfigDir, "initdb-password")
		if writeErr := os.WriteFile(pwFile, []byte(credentials.Password+"\n"), 0o600); writeErr != nil {
			return nil, fmt.Errorf("write initdb password file: %w", writeErr)
		}
		defer os.Remove(pwFile)
		command := fmt.Sprintf("%s -D %s -U %s --pwfile=%s --auth-local=trust --auth-host=scram-sha-256", shellEscape(initdbPath), shellEscape(paths.DataDir), shellEscape(credentials.Username), shellEscape(pwFile))
		res, runErr := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", command}, onData, 2*time.Minute)
		if runErr != nil || res.ExitCode != 0 {
			return nil, fmt.Errorf("initialize host PostgreSQL: %s", firstNonEmpty(res.Stderr, res.Stdout, errString(runErr, "command failed")))
		}
	} else if err != nil {
		return nil, fmt.Errorf("inspect platform PostgreSQL data directory: %w", err)
	}
	if err := writePlatformPostgresConfig(paths); err != nil {
		return nil, err
	}
	if err := s.ensurePlatformPostgresService(ctx, paths, postgresPath, pgCtlPath); err != nil {
		return nil, err
	}
	ready := false
	for attempt := 0; attempt < 30; attempt++ {
		if err := s.runPlatformPostgresSQL(ctx, paths, credentials, "postgres", "SELECT 1"); err == nil {
			ready = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		return nil, fmt.Errorf("host PostgreSQL service did not become queryable")
	}
	for _, database := range []string{"opute", "opute_task_ledger"} {
		check := fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s", shellEscape(database))
		res, runErr := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", fmt.Sprintf("PGPASSFILE=%s %s -h 127.0.0.1 -p %d -U %s -d postgres -Atqc %s", shellEscape(paths.PgPassFile), shellEscape(mustResolveBinary(s, ctx, "psql")), paths.Port, shellEscape(credentials.Username), shellEscape(check))}, nil, 30*time.Second)
		if runErr != nil || res.ExitCode != 0 {
			return nil, fmt.Errorf("inspect platform PostgreSQL database %s: %s", database, firstNonEmpty(res.Stderr, res.Stdout, errString(runErr, "command failed")))
		}
		if strings.TrimSpace(res.Stdout) == "" {
			if err := s.runPlatformPostgresSQL(ctx, paths, credentials, "postgres", "CREATE DATABASE "+database); err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{
		"ready":           true,
		"serviceName":     platformPostgresServiceName,
		"dataDir":         paths.DataDir,
		"bindHost":        paths.BindHost,
		"port":            paths.Port,
		"username":        credentials.Username,
		"databases":       []string{"opute", "opute_task_ledger"},
		"postgresVersion": "managed-by-host-postgres",
	}, nil
}

func mustResolveBinary(s *HostOperationsService, ctx context.Context, name string) string {
	path, err := s.resolveHostBinary(ctx, name)
	if err != nil {
		return name
	}
	return path
}

func (s *HostOperationsService) GetPlatformPostgresStatus(ctx context.Context, args PlatformPostgresArgs) (map[string]any, error) {
	paths, err := defaultPlatformPostgresPaths(args)
	if err != nil {
		return nil, err
	}
	active := false
	res, runErr := s.hostCommandRunnerContext(ctx, []string{"systemctl", "--user", "is-active", platformPostgresServiceName}, nil, 20*time.Second)
	if runErr == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "active" {
		active = true
	}
	credentials, credentialErr := readPlatformPostgresCredentials(paths)
	ready := false
	if credentialErr == nil && active {
		ready = s.runPlatformPostgresSQL(ctx, paths, credentials, "postgres", "SELECT 1") == nil
	}
	return map[string]any{
		"ready":       ready,
		"active":      active,
		"serviceName": platformPostgresServiceName,
		"dataDir":     paths.DataDir,
		"bindHost":    paths.BindHost,
		"port":        paths.Port,
		"username":    paths.Username,
		"credentialState": func() string {
			if credentialErr != nil {
				return "missing-or-invalid"
			}
			return "present"
		}(),
	}, nil
}

func (s *HostOperationsService) RemovePlatformPostgres(ctx context.Context, args PlatformPostgresArgs, confirm bool) (map[string]any, error) {
	if !confirm {
		return nil, fmt.Errorf("remove_platform_postgres requires confirm=true")
	}
	paths, err := defaultPlatformPostgresPaths(args)
	if err != nil {
		return nil, err
	}
	res, runErr := s.hostCommandRunnerContext(ctx, []string{"systemctl", "--user", "disable", "--now", platformPostgresServiceName}, nil, 90*time.Second)
	if runErr != nil || (res.ExitCode != 0 && !strings.Contains(strings.ToLower(res.Stderr+res.Stdout), "not found")) {
		return nil, fmt.Errorf("stop platform PostgreSQL service: %s", firstNonEmpty(res.Stderr, res.Stdout, errString(runErr, "command failed")))
	}
	if err := os.Remove(paths.UnitPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove platform PostgreSQL systemd unit: %w", err)
	}
	if err := os.RemoveAll(paths.DataDir); err != nil {
		return nil, fmt.Errorf("remove platform PostgreSQL data directory: %w", err)
	}
	if err := os.RemoveAll(paths.ConfigDir); err != nil {
		return nil, fmt.Errorf("remove platform PostgreSQL credentials: %w", err)
	}
	return map[string]any{"removed": true, "serviceName": platformPostgresServiceName, "dataDir": paths.DataDir}, nil
}
