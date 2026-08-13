package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// CloudNativePG's standard operand image includes pgvector and uses the
	// standard PostgreSQL-major image tag format accepted by CNPG validation.
	pgVectorImageName     = "ghcr.io/cloudnative-pg/postgresql:16.10-standard-trixie"
	pgVectorExtensionName = "vector"
)

// PgVectorArgs is the versioned host-agent input for reconciling pgvector on
// an existing CloudNativePG Cluster. The cluster image and CNPG-owned
// credentials are controlled by the host agent; callers can only select the
// target and databases.
type PgVectorArgs struct {
	VMName      string   `json:"vmName,omitempty"`
	ClusterName string   `json:"clusterName,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
	Databases   []string `json:"databases,omitempty"`
}

type pgVectorSpec struct {
	postgresqlServiceSpec
	Databases []string
}

type pgVectorDatabaseStatus struct {
	Database       string
	ExtensionReady bool
	Blockers       []string
}

func validatePgVectorSpec(args PgVectorArgs) (pgVectorSpec, error) {
	base := postgresqlServiceSpec{VMName: strings.TrimSpace(args.VMName), ClusterName: strings.TrimSpace(args.ClusterName), Namespace: strings.TrimSpace(args.Namespace), Instances: 1}
	if base.VMName == "" || base.ClusterName == "" || base.Namespace == "" {
		return pgVectorSpec{}, errors.New("vmName, clusterName, and namespace are required")
	}

	databases := append([]string(nil), args.Databases...)
	if len(databases) == 0 {
		databases = []string{"opute"}
	}
	for _, database := range databases {
		if !standaloneIdentifier.MatchString(strings.TrimSpace(database)) {
			return pgVectorSpec{}, errors.New("databases must contain lowercase DNS-safe identifiers")
		}
	}

	return pgVectorSpec{
		postgresqlServiceSpec: base,
		Databases:             databases,
	}, nil
}

func pgVectorSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func (s *HostOperationsService) pgVectorClusterImage(ctx context.Context, spec pgVectorSpec) (string, error) {
	cluster, err := s.postgresqlServiceJSON(ctx, spec.postgresqlServiceSpec, []string{"get", "cluster.postgresql.cnpg.io", spec.ClusterName, "-n", spec.Namespace}, "get PostgreSQL Cluster for pgvector")
	if err != nil {
		return "", err
	}
	if image := nestedString(cluster, "status", "image"); image != "" {
		return image, nil
	}
	return nestedString(cluster, "spec", "imageName"), nil
}

func (s *HostOperationsService) pgVectorClusterReady(ctx context.Context, spec pgVectorSpec) bool {
	cluster, err := s.postgresqlServiceJSON(ctx, spec.postgresqlServiceSpec, []string{"get", "cluster.postgresql.cnpg.io", spec.ClusterName, "-n", spec.Namespace}, "get PostgreSQL Cluster readiness for pgvector")
	if err != nil {
		return false
	}
	instances := nestedInt(cluster, "spec", "instances")
	if instances == 0 {
		instances = 1
	}
	return strings.Contains(strings.ToLower(nestedString(cluster, "status", "phase")), "healthy") && nestedInt(cluster, "status", "readyInstances") >= instances
}

func (s *HostOperationsService) reconcilePgVectorCluster(ctx context.Context, spec pgVectorSpec) error {
	currentImage, err := s.pgVectorClusterImage(ctx, spec)
	if err != nil {
		return err
	}
	if currentImage == pgVectorImageName {
		return nil
	}
	patch := map[string]any{
		"spec": map[string]any{
			"imageName": pgVectorImageName,
		},
	}
	payload, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encode pgvector Cluster patch: %w", err)
	}
	_, err = s.runKubernetesKubectlContext(ctx, spec.VMName, []string{
		"patch", "cluster.postgresql.cnpg.io", spec.ClusterName,
		"-n", spec.Namespace, "--type=merge", "-p", string(payload),
	}, "reconcile pgvector CloudNativePG Cluster", 2*time.Minute)
	return err
}

func (s *HostOperationsService) waitForPgVectorCluster(ctx context.Context, spec pgVectorSpec) (postgresqlServiceSecret, string, error) {
	deadline := time.NewTimer(postgresqlServiceReadinessTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastBlockers := []string{}
	for {
		clusterReady := s.pgVectorClusterReady(ctx, spec)
		serviceReady, _ := s.postgresqlServiceReady(ctx, spec.postgresqlServiceSpec)
		credentials, secretReady, _ := s.postgresqlServiceSecret(ctx, spec.postgresqlServiceSpec)
		primary := ""
		if secretReady && serviceReady {
			primary, _ = s.postgresqlServicePrimary(ctx, spec.postgresqlServiceSpec)
		}
		if clusterReady && serviceReady && secretReady && primary != "" {
			return credentials, primary, nil
		}
		lastBlockers = lastBlockers[:0]
		if !clusterReady {
			lastBlockers = append(lastBlockers, "PostgreSQL Cluster is not healthy")
		}
		if !serviceReady {
			lastBlockers = append(lastBlockers, "PostgreSQL read/write Service has no ready endpoint")
		}
		if !secretReady {
			lastBlockers = append(lastBlockers, "PostgreSQL application Secret is missing required keys")
		}
		if primary == "" {
			lastBlockers = append(lastBlockers, "PostgreSQL primary pod is not ready")
		}
		select {
		case <-ctx.Done():
			return postgresqlServiceSecret{}, "", ctx.Err()
		case <-deadline.C:
			return postgresqlServiceSecret{}, "", fmt.Errorf("PostgreSQL Cluster did not become ready for pgvector: %s", strings.Join(lastBlockers, "; "))
		case <-ticker.C:
		}
	}
}

func (s *HostOperationsService) ensurePgVectorDatabase(ctx context.Context, spec pgVectorSpec, credentials postgresqlServiceSecret, primary, database string) (pgVectorDatabaseStatus, error) {
	status := pgVectorDatabaseStatus{Database: database}
	checkSQL := "SELECT 1 FROM pg_extension WHERE extname = " + pgVectorSQLLiteral(pgVectorExtensionName)
	out, err := s.runPostgreSQLServiceSQL(ctx, spec.postgresqlServiceSpec, credentials, primary, database, checkSQL)
	if err != nil {
		status.Blockers = []string{"database is not SQL-ready"}
		return status, err
	}
	if strings.TrimSpace(out) == "1" {
		status.ExtensionReady = true
		return status, nil
	}
	if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{
		"exec", primary, "-n", spec.Namespace, "--",
		"psql", "-d", database, "-v", "ON_ERROR_STOP=1", "-Atqc", "CREATE EXTENSION IF NOT EXISTS vector",
	}, "create pgvector extension through the local PostgreSQL owner socket", 60*time.Second); err != nil {
		status.Blockers = []string{"CREATE EXTENSION vector failed"}
		return status, err
	}
	status.ExtensionReady = true
	return status, nil
}

func (s *HostOperationsService) getPgVectorDatabaseStatus(ctx context.Context, spec pgVectorSpec, credentials postgresqlServiceSecret, primary, database string) pgVectorDatabaseStatus {
	status := pgVectorDatabaseStatus{Database: database}
	out, err := s.runPostgreSQLServiceSQL(ctx, spec.postgresqlServiceSpec, credentials, primary, database, "SELECT 1 FROM pg_extension WHERE extname = 'vector'")
	if err != nil {
		status.Blockers = []string{"database is not SQL-ready"}
		return status
	}
	status.ExtensionReady = strings.TrimSpace(out) == "1"
	if !status.ExtensionReady {
		status.Blockers = []string{"vector extension is not installed"}
	}
	return status
}

func (s *HostOperationsService) pgVectorStatus(ctx context.Context, spec pgVectorSpec, credentials postgresqlServiceSecret, primary string) ([]pgVectorDatabaseStatus, []string) {
	statuses := make([]pgVectorDatabaseStatus, 0, len(spec.Databases))
	blockers := []string{}
	for _, database := range spec.Databases {
		status := s.getPgVectorDatabaseStatus(ctx, spec, credentials, primary, database)
		if !status.ExtensionReady {
			blockers = append(blockers, fmt.Sprintf("database %s is not pgvector-ready", database))
		}
		statuses = append(statuses, status)
	}
	return statuses, blockers
}

func pgVectorResult(spec pgVectorSpec, imageName string, clusterReady, infrastructureReady bool, statuses []pgVectorDatabaseStatus, blockers []string) map[string]any {
	databases := make([]map[string]any, 0, len(statuses))
	ready := clusterReady && infrastructureReady && imageName == pgVectorImageName
	if imageName != pgVectorImageName {
		blockers = append(blockers, "PostgreSQL Cluster image is not the pinned pgvector image")
	}
	for _, status := range statuses {
		database := map[string]any{
			"database":       status.Database,
			"extensionReady": status.ExtensionReady,
		}
		if len(status.Blockers) > 0 {
			database["blockers"] = status.Blockers
		}
		databases = append(databases, database)
		ready = ready && status.ExtensionReady
	}
	return map[string]any{
		"ready":        ready,
		"clusterReady": clusterReady,
		"vmName":       spec.VMName,
		"namespace":    spec.Namespace,
		"clusterName":  spec.ClusterName,
		"imageName":    imageName,
		"databases":    databases,
		"blockers":     blockers,
	}
}

// EnsurePgVector reconciles the pinned pgvector image and vector extension
// presence in the selected databases. pgvector is not a shared-preload
// library; CNPG manages that parameter and rejects setting it here.
func (s *HostOperationsService) EnsurePgVector(ctx context.Context, args PgVectorArgs, _ func(string)) (map[string]any, error) {
	spec, err := validatePgVectorSpec(args)
	if err != nil {
		return nil, err
	}
	if err := s.reconcilePgVectorCluster(ctx, spec); err != nil {
		return nil, err
	}
	credentials, primary, err := s.waitForPgVectorCluster(ctx, spec)
	if err != nil {
		return nil, err
	}
	statuses := make([]pgVectorDatabaseStatus, 0, len(spec.Databases))
	blockers := []string{}
	for _, database := range spec.Databases {
		status, err := s.ensurePgVectorDatabase(ctx, spec, credentials, primary, database)
		statuses = append(statuses, status)
		if err != nil {
			blockers = append(blockers, fmt.Sprintf("database %s is not pgvector-ready", database))
		}
	}
	imageName, imageErr := s.pgVectorClusterImage(ctx, spec)
	if imageErr != nil {
		return nil, imageErr
	}
	result := pgVectorResult(spec, imageName, true, true, statuses, blockers)
	if ready, _ := result["ready"].(bool); !ready {
		return nil, errors.New("pgvector did not become ready in all requested databases")
	}
	return result, nil
}

// GetPgVectorStatus reports pgvector state without changing the Cluster or
// databases.
func (s *HostOperationsService) GetPgVectorStatus(ctx context.Context, args PgVectorArgs) (map[string]any, error) {
	spec, err := validatePgVectorSpec(args)
	if err != nil {
		return nil, err
	}
	imageName, err := s.pgVectorClusterImage(ctx, spec)
	if err != nil {
		return nil, err
	}
	clusterReady := s.pgVectorClusterReady(ctx, spec)
	serviceReady, _ := s.postgresqlServiceReady(ctx, spec.postgresqlServiceSpec)
	credentials, secretReady, _ := s.postgresqlServiceSecret(ctx, spec.postgresqlServiceSpec)
	primary := ""
	if serviceReady && secretReady {
		primary, _ = s.postgresqlServicePrimary(ctx, spec.postgresqlServiceSpec)
	}
	blockers := []string{}
	if !clusterReady {
		blockers = append(blockers, "PostgreSQL Cluster is not healthy")
	}
	if !serviceReady {
		blockers = append(blockers, "PostgreSQL read/write Service has no ready endpoint")
	}
	if !secretReady {
		blockers = append(blockers, "PostgreSQL application Secret is missing required keys")
	}
	if primary == "" {
		blockers = append(blockers, "PostgreSQL primary pod is not ready")
	}
	statuses := []pgVectorDatabaseStatus{}
	if primary != "" {
		statuses, _ = s.pgVectorStatus(ctx, spec, credentials, primary)
	}
	for _, status := range statuses {
		if !status.ExtensionReady {
			blockers = append(blockers, fmt.Sprintf("database %s is not pgvector-ready", status.Database))
		}
	}
	infrastructureReady := clusterReady && serviceReady && secretReady && primary != ""
	return pgVectorResult(spec, imageName, clusterReady, infrastructureReady, statuses, blockers), nil
}
