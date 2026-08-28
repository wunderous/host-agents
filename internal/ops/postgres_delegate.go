package ops

import (
	"context"

	"github.com/wunderous/host-agents/internal/domain/postgres"
)

// The postgres domain owns these types and operations. HostOperationsService
// keeps delegating methods so the dispatch registry is unaffected; this file
// disappears with internal/ops itself.
type (
	PostgreSQLServiceArgs      = postgres.PostgreSQLServiceArgs
	PostgreSQLServiceRelayArgs = postgres.PostgreSQLServiceRelayArgs
	InstallPostgreSQLArgs      = postgres.InstallPostgreSQLArgs
	SQLiteDatabaseArgs         = postgres.SQLiteDatabaseArgs
	SQLiteDatabaseResult       = postgres.SQLiteDatabaseResult
	EnsureSQLConnectorArgs     = postgres.EnsureSQLConnectorArgs
	SQLConnectorResult         = postgres.SQLConnectorResult
)

func (s *HostOperationsService) postgres() *postgres.Service {
	s.postgresOnce.Do(func() {
		s.postgresSvc = postgres.New(&s.shared, postgres.Deps{
			RunKubectlContext:          s.kubernetes().RunKubectlContext,
			RunKubectlTimed:            s.kubernetes().RunKubectlTimed,
			RunKubectlWithStdinContext: s.kubernetes().RunKubectlWithStdinContext,
		}, s.postgresRelayConfigDir, s.sqliteDatabaseRoot)
	})
	return s.postgresSvc
}

func (s *HostOperationsService) ReconcilePostgreSQLService(ctx context.Context, args PostgreSQLServiceArgs, onData func(string)) (map[string]any, error) {
	return s.postgres().ReconcilePostgreSQLService(ctx, args, onData)
}

func (s *HostOperationsService) GetPostgreSQLServiceStatus(ctx context.Context, args PostgreSQLServiceArgs) (map[string]any, error) {
	return s.postgres().GetPostgreSQLServiceStatus(ctx, args)
}

func (s *HostOperationsService) RemovePostgreSQLService(ctx context.Context, args PostgreSQLServiceArgs, confirm bool) (map[string]any, error) {
	return s.postgres().RemovePostgreSQLService(ctx, args, confirm)
}

func (s *HostOperationsService) ReleasePostgreSQLServiceRelay(sessionID, relayToken string) (map[string]any, error) {
	return s.postgres().ReleasePostgreSQLServiceRelay(sessionID, relayToken)
}

func (s *HostOperationsService) EnsureSQLiteDatabase(ctx context.Context, args SQLiteDatabaseArgs) (SQLiteDatabaseResult, error) {
	return s.postgres().EnsureSQLiteDatabase(ctx, args)
}

func (s *HostOperationsService) GetSQLiteDatabaseStatus(ctx context.Context, args SQLiteDatabaseArgs) (SQLiteDatabaseResult, error) {
	return s.postgres().GetSQLiteDatabaseStatus(ctx, args)
}

func (s *HostOperationsService) RemoveSQLiteDatabase(ctx context.Context, args SQLiteDatabaseArgs, confirm bool) (SQLiteDatabaseResult, error) {
	return s.postgres().RemoveSQLiteDatabase(ctx, args, confirm)
}

func (s *HostOperationsService) EnsureSQLConnector(args EnsureSQLConnectorArgs) (SQLConnectorResult, error) {
	return s.postgres().EnsureSQLConnector(args)
}

func (s *HostOperationsService) GetSQLConnectorStatus(databaseID string) (map[string]any, error) {
	return s.postgres().GetSQLConnectorStatus(databaseID)
}

func (s *HostOperationsService) ReleaseSQLConnector(databaseID string, force bool) (bool, error) {
	return s.postgres().ReleaseSQLConnector(databaseID, force)
}

func (s *HostOperationsService) StopAllHostTCPRelays() error {
	return s.postgres().StopAllHostTCPRelays()
}

func (s *HostOperationsService) InstallPostgreSQL(args InstallPostgreSQLArgs, onData func(string)) (map[string]any, error) {
	return s.postgres().InstallPostgreSQL(args, onData)
}

func (s *HostOperationsService) GetPostgreSQLStatus(vmName, namespace string) (map[string]any, error) {
	return s.postgres().GetPostgreSQLStatus(vmName, namespace)
}

func (s *HostOperationsService) DeletePostgreSQL(vmName, namespace string, onData func(string)) (map[string]any, error) {
	return s.postgres().DeletePostgreSQL(vmName, namespace, onData)
}

func (s *HostOperationsService) RunSQL(vmName, namespace, database, sql string) (map[string]any, error) {
	return s.postgres().RunSQL(vmName, namespace, database, sql)
}
