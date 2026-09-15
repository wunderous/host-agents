package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The Kubernetes provider refuses an exec argument that is empty or carries a
// newline, so every kubectl argument the PostgreSQL service emits has to stay
// single-line. The database-creation path once handed the provider its raw
// multiline shell script and failed every reconcile with
// "kubectlArgs must contain non-empty safe strings".
func TestEnsurePostgreSQLServiceDatabaseKeepsExecArgsSingleLine(t *testing.T) {
	service := validResetService()
	spec := postgresqlServiceSpec{
		VMName:      "opute-ha-a",
		ClusterName: "opute-platform-postgres",
		Namespace:   "opute-platform",
	}
	credentials := postgresqlServiceSecret{Username: "opute", Password: "secret"}

	var calls [][]string
	service.setKubectlRunner(func(_ context.Context, _ string, args []string, _ []byte, _ string, _ time.Duration) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		// An empty check result is what drives the creation path.
		return "", nil
	})

	if err := service.ensurePostgreSQLServiceDatabase(context.Background(), spec, credentials, "opute-platform-postgres-1", "opute_task_ledger"); err != nil {
		t.Fatalf("ensurePostgreSQLServiceDatabase: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected a check and a create call, got %d", len(calls))
	}
	for callIndex, args := range calls {
		for argIndex, arg := range args {
			if strings.TrimSpace(arg) == "" || strings.ContainsAny(arg, "\x00\r\n") {
				t.Fatalf("call %d argument %d is not a safe single-line string: %q", callIndex, argIndex, arg)
			}
		}
	}
}
