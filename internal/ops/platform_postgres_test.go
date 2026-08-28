package ops

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEnsurePostgreSQLServiceAppliesOperatorBeforeCluster(t *testing.T) {
	service := validResetService()
	applied := false
	var order []string
	service.kubectlRunner = func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error) {
		cmd := strings.Join(kubectlArgs, " ")
		order = append(order, cmd)
		switch {
		case kubectlArgs[0] == "get" && kubectlArgs[1] == "nodes":
			return `{"items":[{"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`, nil
		case kubectlArgs[0] == "get" && kubectlArgs[1] == "crd":
			if !applied {
				return "", fmt.Errorf("CRD not installed yet")
			}
			return "", nil
		case kubectlArgs[0] == "get" && kubectlArgs[1] == "endpoints":
			return `{"subsets":[{"addresses":[{"ip":"10.42.0.9"}]}]}`, nil
		case kubectlArgs[0] == "apply":
			if strings.Contains(string(input), "chart: cloudnative-pg") {
				applied = true
				return "", nil
			}
			if strings.Contains(string(input), "apiVersion: postgresql.cnpg.io/v1") {
				if !applied {
					return "", fmt.Errorf("cluster applied before operator")
				}
				return "", nil
			}
			return "", fmt.Errorf("unexpected manifest")
		default:
			return "", fmt.Errorf("unexpected call: %s", cmd)
		}
	}
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Databases: []string{"testdb"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.ensurePostgreSQLServiceOrdered(ctx, spec); err != nil {
		t.Fatal(err)
	}
	operatorIndex, clusterIndex, crdIndex := -1, -1, -1
	for index, cmd := range order {
		if strings.HasPrefix(cmd, "apply -f -") {
			if operatorIndex < 0 {
				operatorIndex = index
			} else if clusterIndex < 0 {
				clusterIndex = index
			}
		}
		if strings.HasPrefix(cmd, "get crd") && crdIndex < 0 {
			crdIndex = index
		}
	}
	if operatorIndex < 0 || clusterIndex < 0 || crdIndex < 0 {
		t.Fatalf("missing expected events: order=%v", order)
	}
	if !(operatorIndex < crdIndex && crdIndex < clusterIndex) {
		t.Fatalf("expected operator apply < CRD wait < cluster apply, got order=%v", order)
	}
}

func TestPostgreSQLServiceProbeReadyRequiresSQLAndBothStores(t *testing.T) {
	base := postgresqlServiceProbe{
		OperatorReady:      true,
		CRDPresent:         true,
		ClusterReady:       true,
		ServiceReady:       true,
		SecretReady:        true,
		PrimaryReady:       true,
		SQLReady:           true,
		TaskLedgerSQLReady: true,
	}
	if !postgresqlServiceProbeReady(base) {
		t.Fatal("expected a complete SQL-gated probe to be ready")
	}
	base.TaskLedgerSQLReady = false
	if postgresqlServiceProbeReady(base) {
		t.Fatal("task-ledger readiness must be part of the steady-state fast path")
	}
}

func TestPostgreSQLServiceInfrastructureReadyAllowsDatabaseCreation(t *testing.T) {
	base := postgresqlServiceProbe{
		OperatorReady: true,
		CRDPresent:    true,
		ClusterReady:  true,
		ServiceReady:  true,
		SecretReady:   true,
		PrimaryReady:  true,
		SQLReady:      true,
	}
	if !postgresqlServiceInfrastructureReady(base) {
		t.Fatal("system database readiness should allow configured database reconciliation")
	}
	if postgresqlServiceProbeReady(base) {
		t.Fatal("steady-state readiness must still require configured databases")
	}
}

func TestEnsurePostgreSQLServiceNeverAppliesClusterWithoutCRD(t *testing.T) {
	service := validResetService()
	clusterApplied := false
	service.kubectlRunner = func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error) {
		switch {
		case kubectlArgs[0] == "get" && kubectlArgs[1] == "nodes":
			return `{"items":[{"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`, nil
		case kubectlArgs[0] == "get" && kubectlArgs[1] == "crd":
			return "", fmt.Errorf("CRD never appears")
		case kubectlArgs[0] == "apply":
			if strings.Contains(string(input), "apiVersion: postgresql.cnpg.io/v1") {
				clusterApplied = true
			}
			return "", nil
		default:
			return "", fmt.Errorf("unexpected call")
		}
	}
	spec, _ := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Databases: []string{"testdb"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.ensurePostgreSQLServiceOrdered(ctx, spec); err == nil {
		t.Fatal("expected an error when the CNPG CRD never appears")
	}
	if clusterApplied {
		t.Fatal("tenant Cluster must not be applied while the CNPG CRD is absent")
	}
}

func TestEnsurePostgreSQLServiceRequiresK3sReadyBeforeOperator(t *testing.T) {
	service := validResetService()
	applies := 0
	service.kubectlRunner = func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error) {
		switch {
		case kubectlArgs[0] == "get" && kubectlArgs[1] == "nodes":
			return "", fmt.Errorf("K3s API unavailable")
		case kubectlArgs[0] == "apply":
			applies++
			return "", nil
		default:
			return "", fmt.Errorf("unexpected call")
		}
	}
	spec, _ := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Databases: []string{"testdb"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.ensurePostgreSQLServiceOrdered(ctx, spec); err == nil {
		t.Fatal("expected an error when K3s has no Ready node")
	}
	if applies != 0 {
		t.Fatalf("no manifest may be applied before K3s readiness, applied %d times", applies)
	}
}

func TestEnsurePostgreSQLServiceCompletesSQLGatedResult(t *testing.T) {
	service := validResetService()
	operatorApplied := false
	consumerSecret := fmt.Sprintf(`{"data":{"testDatabaseUrl":"%s","testLedgerDatabaseUrl":"%s"}}`,
		base64.StdEncoding.EncodeToString([]byte("postgresql://opute:p@svc/opute")),
		base64.StdEncoding.EncodeToString([]byte("postgresql://opute:p@svc/opute_task_ledger")))
	service.kubectlRunner = func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error) {
		command := kubectlArgs[0]
		switch {
		case command == "get" && len(kubectlArgs) > 1 && kubectlArgs[1] == "nodes":
			return `{"items":[{"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`, nil
		case command == "get" && kubectlArgs[1] == "namespace":
			return "", nil
		case command == "get" && kubectlArgs[1] == "crd":
			if !operatorApplied {
				return "", fmt.Errorf("operator HelmChart not applied")
			}
			return "", nil
		case command == "apply":
			manifest := string(input)
			if strings.Contains(manifest, "chart: cloudnative-pg") {
				operatorApplied = true
				return "", nil
			}
			if strings.Contains(manifest, "apiVersion: postgresql.cnpg.io/v1") {
				return "", nil
			}
			return "", nil
		case command == "get" && kubectlArgs[1] == "deployments":
			if strings.Contains(strings.Join(kubectlArgs, " "), "app.kubernetes.io/name=cloudnative-pg") {
				return `{"items":[{"spec":{"replicas":1},"status":{"availableReplicas":1}}]}`, nil
			}
			return `{"items":[]}`, nil
		case command == "get" && kubectlArgs[1] == "cluster.postgresql.cnpg.io":
			return `{"status":{"phase":"Cluster is healthy","readyInstances":1},"spec":{"instances":1}}`, nil
		case command == "get" && kubectlArgs[1] == "service":
			return `{"spec":{"clusterIP":"10.43.0.5"}}`, nil
		case command == "get" && kubectlArgs[1] == "endpoints":
			return `{"subsets":[{"addresses":[{"ip":"10.42.0.7"}]}]}`, nil
		case command == "get" && kubectlArgs[1] == "secret":
			if kubectlArgs[2] == "test-db" {
				return consumerSecret, nil
			}
			return fmt.Sprintf(`{"data":{"username":"%s","password":"%s"}}`,
				base64.StdEncoding.EncodeToString([]byte("opute")),
				base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32)))), nil
		case command == "get" && kubectlArgs[1] == "pods":
			return `{"items":[{"metadata":{"name":"opute-platform-postgres-1"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"ready":true}]}}]}`, nil
		case command == "delete" && kubectlArgs[1] == "secret":
			return "", nil
		case command == "exec":
			return "1\n", nil
		default:
			return "", fmt.Errorf("unexpected kubectl call: %s", strings.Join(kubectlArgs, " "))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := service.ReconcilePostgreSQLService(ctx, PostgreSQLServiceArgs{VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Databases: []string{"testdb", "test_ledger"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["ready"] != true || result["sqlReady"] != true || result["taskLedgerSqlReady"] != true {
		t.Fatalf("expected SQL-gated readiness for both databases: %#v", result)
	}
	if result["consumerSecretReady"] != true {
		t.Fatalf("expected consumer Secret readiness: %#v", result)
	}
	if result["credentialState"] != "cnpg-owned" {
		t.Fatalf("expected cnpg-owned credential state: %#v", result)
	}
	if strings.Contains(fmt.Sprintf("%v", result), strings.Repeat("p", 32)) {
		t.Fatal("result must not contain the CNPG password")
	}
}

func TestValidatePostgreSQLServiceSpecRequiresCallerIdentity(t *testing.T) {
	_, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{VMName: "opute-local"})
	if err == nil {
		t.Fatal("expected caller identity to be required")
	}
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Databases: []string{"testdb"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl", "test_ledger": "testLedgerDatabaseUrl"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.ClusterName != "test-postgres" || spec.Namespace != "test-system" {
		t.Fatalf("unexpected target: %#v", spec)
	}
	if spec.Instances != 1 || spec.StorageClass != "local-path" || spec.RetentionPolicy != "delete" {
		t.Fatalf("unexpected CNPG defaults: %#v", spec)
	}
}

func TestValidatePostgreSQLServiceSpecRejectsUnsafeOrMismatchedInputs(t *testing.T) {
	tests := []PostgreSQLServiceArgs{
		{ClusterName: "missing-vm"},
		{VMName: "vm", ClusterName: "Bad_Name"},
		{VMName: "vm", Instances: 0, RetentionPolicy: "unknown"},
		{VMName: "vm", Instances: 6},
	}
	for _, input := range tests {
		if _, err := validatePostgreSQLServiceSpec(input); err == nil {
			t.Fatalf("expected validation failure for %#v", input)
		}
	}
}

func TestPostgreSQLServiceManifestsUseCloudNativePGAndNoHostService(t *testing.T) {
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{
		VMName: "opute-local", ClusterName: "test-postgres", Namespace: "test-system", Instances: 3, StorageClass: "fast-local", Databases: []string{"testdb"}, ConsumerSecretName: "test-db", ConsumerSecretLabel: "host-agent.io/test", ServiceOwner: "test-owner", ServicePartOf: "test-service", ConsumerDatabaseKeys: map[string]string{"testdb": "testDatabaseUrl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	operator := renderPostgreSQLServiceOperatorManifest()
	cluster := renderPostgreSQLServiceClusterManifest(spec)
	for _, value := range []string{
		"kind: HelmChart",
		"chart: cloudnative-pg",
		"targetNamespace: cnpg-system",
		"kind: Cluster",
		"apiVersion: postgresql.cnpg.io/v1",
		"instances: 3",
		"storageClass: fast-local",
		"host-agent.io/retention-policy: delete",
	} {
		if !strings.Contains(operator+cluster, value) {
			t.Fatalf("manifest missing %q:\n%s\n%s", value, operator, cluster)
		}
	}
	if strings.Contains(operator+cluster, "systemctl") || strings.Contains(operator+cluster, "postgresql.service") {
		t.Fatalf("CNPG manifest contains a host PostgreSQL service reference:\n%s\n%s", operator, cluster)
	}
	if !strings.Contains(cluster, "createdb: true") {
		t.Fatalf("CNPG application role must be allowed to create the secondary database:\n%s", cluster)
	}
}

func TestPostgreSQLServiceCreateDatabaseSQLUsesIdentifierQuoting(t *testing.T) {
	sql := postgresqlServiceCreateDatabaseSQL("opute_task_ledger")
	if sql != `CREATE DATABASE "opute_task_ledger"` {
		t.Fatalf("unexpected CREATE DATABASE SQL: %q", sql)
	}
}

func TestPostgreSQLServiceSQLScriptKeepsPasswordOffCommandLine(t *testing.T) {
	script := postgresqlServiceSQLScript("opute-platform-postgres-rw.opute-system.svc", "opute", "postgres", "SELECT 1")
	if strings.Contains(script, "password") || strings.Contains(script, "PGPASSWORD") {
		t.Fatalf("SQL script should read pgpass from stdin, not expose a password:\n%s", script)
	}
	if !strings.Contains(script, "cat >\"$pgpass\"") || !strings.Contains(script, "PGPASSFILE=\"$pgpass\"") {
		t.Fatalf("SQL script does not use an ephemeral stdin-backed pgpass file:\n%s", script)
	}
	if !strings.Contains(script, `pgpass_dir="${TMPDIR:-/controller/tmp}"`) || !strings.Contains(script, `mktemp "$pgpass_dir/opute-pgpass.XXXXXX"`) {
		t.Fatalf("SQL script should select a writable CNPG temp directory:\n%s", script)
	}
}

func TestKubectlShellScriptArgumentKeepsProviderArgumentsSingleLine(t *testing.T) {
	script := "set -eu\nprintf '%s' 'SELECT 1'\n"
	argument := kubectlShellScriptArgument(script)
	if strings.ContainsAny(argument, "\r\n") {
		t.Fatalf("encoded kubectl shell argument contains a line break: %q", argument)
	}
	const prefix = "eval \"$(printf '%s' '"
	const suffix = "' | base64 -d)\""
	if !strings.HasPrefix(argument, prefix) || !strings.HasSuffix(argument, suffix) {
		t.Fatalf("unexpected encoded shell argument: %q", argument)
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(argument, prefix), suffix)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("encoded shell argument is not base64: %v", err)
	}
	if string(decoded) != script {
		t.Fatalf("decoded shell script = %q, want %q", decoded, script)
	}
}

func TestPostgreSQLServiceResultDoesNotContainCredentials(t *testing.T) {
	relay := PostgreSQLServiceRelayArgs{
		SessionID: "session-1", ListenHost: "127.0.0.1",
		RelayToken: strings.Repeat("s", 32),
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(relay.RelayToken)), "password") {
		t.Fatal("test setup must use a non-password relay token")
	}
}

func TestPostgreSQLServiceConsumerSecretUsesCNPGNamespaceAndBothURLs(t *testing.T) {
	spec, err := validatePostgreSQLServiceSpec(PostgreSQLServiceArgs{
		VMName:               "opute-local",
		Namespace:            "opute-system",
		ClusterName:          "opute-platform-postgres",
		Databases:            []string{"opute", "opute_task_ledger"},
		ConsumerSecretName:   "opute-platform-db",
		ConsumerSecretLabel:  "host-agent.io/managed-postgres",
		ServiceOwner:         "opute",
		ServicePartOf:        "opute-platform",
		ConsumerDatabaseKeys: map[string]string{"opute": "platformDatabaseUrl", "opute_task_ledger": "taskLedgerDatabaseUrl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := renderPostgreSQLServiceConsumerSecret(spec, postgresqlServiceSecret{
		Username: "opute",
		Password: strings.Repeat("p", 32),
	})
	for _, expected := range []string{
		"name: opute-platform-db",
		"namespace: opute-system",
		"platformDatabaseUrl:",
		"taskLedgerDatabaseUrl:",
	} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("consumer Secret missing %q:\n%s", expected, manifest)
		}
	}
	if strings.Contains(manifest, strings.Repeat("p", 32)) {
		t.Fatal("consumer Secret must encode credentials rather than render them in plaintext")
	}
	for _, key := range []string{"platformDatabaseUrl:", "taskLedgerDatabaseUrl:"} {
		line := ""
		for _, candidate := range strings.Split(manifest, "\n") {
			if strings.HasPrefix(candidate, "  "+key) {
				line = strings.TrimSpace(strings.TrimPrefix(candidate, "  "+key))
				break
			}
		}
		if line == "" {
			t.Fatalf("missing encoded value for %s", key)
		}
		if _, err := base64.StdEncoding.DecodeString(line); err != nil {
			t.Fatalf("Secret value for %s is not base64: %v", key, err)
		}
	}
}
