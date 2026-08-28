// Package postgres owns the host's SQL surface: the CloudNativePG service
// reconciled onto a cluster, the authenticated relay that lets a client reach
// it, the reference-counted host TCP connector, and local SQLite databases.
package postgres

import (
	"context"
	"regexp"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

// defaultDiscoveryTimeout bounds a read-only kubectl call. The kubernetes
// domain has its own copy of this budget; S4.3 rule 1 forbids importing it.
const defaultDiscoveryTimeout = 45 * time.Second

// standaloneIdentifier bounds a namespace or database name in the standalone
// stack, which is stricter than postgresDatabaseIdentifier: no underscores.
var standaloneIdentifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

var postgresDatabaseIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// Deps are the cross-domain capabilities postgres requires. Both are the
// kubernetes domain: a PostgreSQL service is reconciled by running kubectl
// against a cluster, and manifests are fed to it on stdin.
type Deps struct {
	RunKubectlContext          func(ctx context.Context, vmName string, kubectlArgs []string, label string, timeout time.Duration) (string, error)
	RunKubectlTimed            func(vmName string, kubectlArgs []string, label string, timeout time.Duration) (string, error)
	RunKubectlWithStdinContext func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error)
}

// Service is the postgres domain's entry point.
type Service struct {
	shared *hostruntime.Shared
	deps   Deps

	// sqliteDatabaseRoot is the directory local SQLite databases live under.
	sqliteDatabaseRoot string

	// Both relay holders own live listeners, so a Service is constructed once
	// per host service, not per call.
	relay        *postgresqlServiceRelayManager
	sqlConnector *sqlConnectorSupervisor
}

// New builds the postgres domain over the shared runtime seam.
// relayConfigDir is where authenticated relay sessions are persisted so they
// survive an agent restart; an empty value keeps them in memory only.
func New(shared *hostruntime.Shared, deps Deps, relayConfigDir, sqliteDatabaseRoot string) *Service {
	return &Service{
		shared:             shared,
		deps:               deps,
		sqliteDatabaseRoot: sqliteDatabaseRoot,
		relay:              newPersistentPostgreSQLServiceRelayManagerAt(relayConfigDir),
		sqlConnector:       newSQLConnectorSupervisor(),
	}
}
