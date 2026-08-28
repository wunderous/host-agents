package hostagent

import (
	"github.com/wunderous/host-agents/internal/domain/postgres"
)

// These aliases name the postgres domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
type (
	PostgreSQLServiceArgs      = postgres.PostgreSQLServiceArgs
	PostgreSQLServiceRelayArgs = postgres.PostgreSQLServiceRelayArgs
	InstallPostgreSQLArgs      = postgres.InstallPostgreSQLArgs
	SQLiteDatabaseArgs         = postgres.SQLiteDatabaseArgs
	SQLiteDatabaseResult       = postgres.SQLiteDatabaseResult
	EnsureSQLConnectorArgs     = postgres.EnsureSQLConnectorArgs
	SQLConnectorResult         = postgres.SQLConnectorResult
)

func (s *Service) Postgres() *postgres.Service {
	s.postgresOnce.Do(func() {
		s.postgresSvc = postgres.New(&s.shared, postgres.Deps{
			RunKubectlContext:          s.Kubernetes().RunKubectlContext,
			RunKubectlTimed:            s.Kubernetes().RunKubectlTimed,
			RunKubectlWithStdinContext: s.Kubernetes().RunKubectlWithStdinContext,
		}, s.postgresRelayConfigDir, s.sqliteDatabaseRoot)
	})
	return s.postgresSvc
}
