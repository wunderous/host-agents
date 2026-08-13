package ops

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestValidatePgVectorSpecDefaultsAndDatabaseValidation(t *testing.T) {
	spec, err := validatePgVectorSpec(PgVectorArgs{VMName: "opute-public-k3s", ClusterName: "opute-platform-postgres", Namespace: "opute-system"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.ClusterName != "opute-platform-postgres" || spec.Namespace != "opute-system" {
		t.Fatalf("unexpected defaults: %+v", spec)
	}
	if len(spec.Databases) != 1 || spec.Databases[0] != "opute" {
		t.Fatalf("unexpected database defaults: %+v", spec.Databases)
	}
	if _, err := validatePgVectorSpec(PgVectorArgs{VMName: "opute-public-k3s", ClusterName: "opute-platform-postgres", Namespace: "opute-system", Databases: []string{"Not-DNS-Safe"}}); err == nil {
		t.Fatal("expected invalid database identifier to fail")
	}
}

func pgVectorTestClusterJSON(image string) string {
	return fmt.Sprintf(`{"spec":{"instances":1,"imageName":%q},"status":{"phase":"Cluster is healthy","readyInstances":1}}`, image)
}

func pgVectorTestSecret() string {
	return fmt.Sprintf(`{"data":{"username":%q,"password":%q}}`,
		base64.StdEncoding.EncodeToString([]byte("opute")),
		base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
}

func pgVectorTestRunner(t *testing.T, allowMutation bool, calls *[]string) func(context.Context, string, []string, []byte, string, time.Duration) (string, error) {
	t.Helper()
	clusterImage := pgVectorImageName
	if allowMutation {
		clusterImage = "ghcr.io/cloudnative-pg/postgresql:16"
	}
	return func(_ context.Context, _ string, args []string, _ []byte, _ string, _ time.Duration) (string, error) {
		command := strings.Join(args, " ")
		*calls = append(*calls, command)
		if len(args) == 0 {
			return "", fmt.Errorf("empty kubectl command")
		}
		switch args[0] {
		case "get":
			if len(args) > 1 {
				switch args[1] {
				case "cluster.postgresql.cnpg.io":
					if len(args) > 2 && args[2] == "opute-platform-postgres" {
						return pgVectorTestClusterJSON(clusterImage), nil
					}
				case "service":
					return `{"spec":{"clusterIP":"10.43.0.5"}}`, nil
				case "endpoints":
					return `{"subsets":[{"addresses":[{"ip":"10.42.0.7"}]}]}`, nil
				case "secret":
					return pgVectorTestSecret(), nil
				case "pods":
					return `{"items":[{"metadata":{"name":"opute-platform-postgres-1"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"ready":true}]}}]}`, nil
				}
			}
		case "patch":
			if !allowMutation {
				return "", fmt.Errorf("unexpected mutation: %s", command)
			}
			if !strings.Contains(command, `"imageName":"`+pgVectorImageName+`"`) || strings.Contains(command, "shared_preload_libraries") {
				return "", fmt.Errorf("unexpected pgvector patch: %s", command)
			}
			clusterImage = pgVectorImageName
			return "", nil
		case "exec":
			if !allowMutation && strings.Contains(command, "CREATE EXTENSION") {
				return "", fmt.Errorf("status attempted extension creation")
			}
			if strings.Contains(command, "SELECT 1 FROM pg_extension") {
				return "1\n", nil
			}
			return "", nil
		}
		return "", fmt.Errorf("unexpected kubectl command: %s", command)
	}
}

func TestEnsurePgVectorReconcilesClusterAndDatabase(t *testing.T) {
	service := validResetService()
	var calls []string
	service.kubectlRunner = pgVectorTestRunner(t, true, &calls)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := service.EnsurePgVector(ctx, PgVectorArgs{VMName: "opute-public-k3s", ClusterName: "opute-platform-postgres", Namespace: "opute-system", Databases: []string{"opute"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ready, _ := result["ready"].(bool); !ready {
		t.Fatalf("expected ready result: %+v", result)
	}
	patched := false
	for _, call := range calls {
		if strings.HasPrefix(call, "patch cluster.postgresql.cnpg.io") {
			patched = true
			break
		}
	}
	if !patched {
		t.Fatalf("expected cluster image patch, calls=%v", calls)
	}
}

func TestGetPgVectorStatusDoesNotMutate(t *testing.T) {
	service := validResetService()
	var calls []string
	service.kubectlRunner = pgVectorTestRunner(t, false, &calls)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := service.GetPgVectorStatus(ctx, PgVectorArgs{VMName: "opute-public-k3s", ClusterName: "opute-platform-postgres", Namespace: "opute-system", Databases: []string{"opute"}})
	if err != nil {
		t.Fatal(err)
	}
	if ready, _ := result["ready"].(bool); !ready {
		t.Fatalf("expected ready status: %+v", result)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "patch ") || strings.Contains(call, "CREATE EXTENSION") {
			t.Fatalf("status performed mutation: %s", call)
		}
	}
}
