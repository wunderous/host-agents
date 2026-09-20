package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAdmitPVCSizeChangeRefusesShrink(t *testing.T) {
	err := admitPVCSizeChange("20Gi", "10Gi", func() error {
		t.Fatal("expand check must not run on shrink")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "grow-only") {
		t.Fatalf("expected shrink refusal, got %v", err)
	}
}

func TestAdmitPVCSizeChangeAllowsCreateAndEqual(t *testing.T) {
	if err := admitPVCSizeChange("", "10Gi", func() error {
		t.Fatal("create must not require expansion")
		return nil
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := admitPVCSizeChange("10Gi", "10Gi", func() error {
		t.Fatal("equal size must not require expansion")
		return nil
	}); err != nil {
		t.Fatalf("equal: %v", err)
	}
}

func TestAdmitPVCSizeChangeRequiresExpansion(t *testing.T) {
	called := false
	if err := admitPVCSizeChange("10Gi", "20Gi", func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("grow: %v", err)
	}
	if !called {
		t.Fatal("grow must verify StorageClass expansion")
	}
}

func TestAdmitPostgreSQLStorageFailsClosedWithoutExpansion(t *testing.T) {
	service := validResetService()
	service.setKubectlRunner(func(_ context.Context, _ string, kubectlArgs []string, _ []byte, _ string, _ time.Duration) (string, error) {
		cmd := strings.Join(kubectlArgs, " ")
		switch {
		case strings.HasPrefix(cmd, "get cluster.postgresql.cnpg.io"):
			return `{"spec":{"storage":{"size":"10Gi","storageClass":"local-path"}}}`, nil
		case strings.HasPrefix(cmd, "get storageclass local-path"):
			return `{"allowVolumeExpansion":false}`, nil
		default:
			return "", nil
		}
	})
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{
		VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system",
		StorageSize: "20Gi", Databases: []string{"testdb"}, ConsumerSecretName: "test-db",
		ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service",
		ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.admitPostgreSQLStorage(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "allow volume expansion") {
		t.Fatalf("expected expansion refusal, got %v", err)
	}
}

func TestAdmitPostgreSQLStorageTreatsMissingClusterAsCreate(t *testing.T) {
	service := validResetService()
	service.setKubectlRunner(func(_ context.Context, _ string, _ []string, _ []byte, _ string, _ time.Duration) (string, error) {
		return "", fmt.Errorf(`clusters.postgresql.cnpg.io %q not found`, "test-postgres")
	})
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{
		VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system",
		StorageSize: "20Gi", Databases: []string{"testdb"}, ConsumerSecretName: "test-db",
		ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service",
		ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.admitPostgreSQLStorage(context.Background(), spec); err != nil {
		t.Fatalf("missing cluster must be a create, got %v", err)
	}
}

func TestAdmitPostgreSQLStorageFailsClosedOnReadError(t *testing.T) {
	service := validResetService()
	service.setKubectlRunner(func(_ context.Context, _ string, _ []string, _ []byte, _ string, _ time.Duration) (string, error) {
		return "", fmt.Errorf("connection refused")
	})
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{
		VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system",
		StorageSize: "20Gi", Databases: []string{"testdb"}, ConsumerSecretName: "test-db",
		ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service",
		ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.admitPostgreSQLStorage(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "cannot be admitted") {
		t.Fatalf("expected fail-closed read error, got %v", err)
	}
}

func TestPostgreSQLClusterConfigurationReadyIncludesStorageSize(t *testing.T) {
	service := validResetService()
	service.setKubectlRunner(func(_ context.Context, _ string, kubectlArgs []string, _ []byte, _ string, _ time.Duration) (string, error) {
		return `{"metadata":{"annotations":{"host-agent.io/resource-profile":"constrained-2GiB-v3"}},"spec":{"storage":{"size":"10Gi"}}}`, nil
	})
	spec := postgresqlServiceSpec{StorageSize: "20Gi", ClusterName: "db", Namespace: "ns"}
	ready, err := service.postgresqlServiceClusterConfigurationReady(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("a storage size mismatch must not count as configured")
	}
}
