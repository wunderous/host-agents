package ops

import (
	"strings"
	"testing"
)

func TestStandalonePostgresManifestUsesCloudNativePG(t *testing.T) {
	manifest := standalonePostgresManifest("opute-local-db", "app")
	for _, expected := range []string{
		"kind: HelmChart",
		"chart: cloudnative-pg",
		"kind: Cluster",
		"apiVersion: postgresql.cnpg.io/v1",
		"owner: app",
		"size: 2Gi",
		"database: app",
	} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("CNPG standalone manifest missing %q:\n%s", expected, manifest)
		}
	}
	for _, forbidden := range []string{
		"postgres:16-alpine",
		"POSTGRES_PASSWORD",
		"postgres-secret",
	} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("standalone manifest contains retired host-password deployment marker %q:\n%s", forbidden, manifest)
		}
	}
}

func TestStandalonePostgresOperatorAndClusterAreAppliedSeparately(t *testing.T) {
	operator := standalonePostgresOperatorManifest("opute-local-db")
	if strings.Contains(operator, "apiVersion: postgresql.cnpg.io/v1") {
		t.Fatalf("operator manifest must not apply the Cluster before its CRD exists:\n%s", operator)
	}
	cluster := standalonePostgresClusterManifest("opute-local-db", "app")
	if !strings.Contains(cluster, "apiVersion: postgresql.cnpg.io/v1") {
		t.Fatalf("cluster manifest must contain the CNPG Cluster resource:\n%s", cluster)
	}
}

func TestStandalonePostgresSQLScriptReadsCredentialsFromStdin(t *testing.T) {
	script := standalonePostgresSQLScript("app", "app", "SELECT 1")
	if strings.Contains(script, "PGPASSWORD") || strings.Contains(script, "password=") {
		t.Fatalf("standalone SQL script must not put credentials in command environment:\n%s", script)
	}
	if !strings.Contains(script, `cat >"$pgpass"`) || !strings.Contains(script, `PGPASSFILE="$pgpass"`) {
		t.Fatalf("standalone SQL script must use an ephemeral pgpass file:\n%s", script)
	}
}
