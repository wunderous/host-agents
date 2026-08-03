package ops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	platformPostgresOperatorRelease     = "cloudnativepg"
	platformPostgresOperatorNamespace   = "cnpg-system"
	platformPostgresClusterName         = "opute-platform-postgres"
	platformPostgresNamespace           = "opute-system"
	platformPostgresStorageSize         = "10Gi"
	platformPostgresServicePort         = 5432
	platformPostgresReadinessTimeout    = 15 * time.Minute
	platformPostgresConsumerSecretName  = "opute-platform-db"
	platformPostgresConsumerSecretLabel = "opute.io/managed-platform-postgres"
)

// PlatformPostgresArgs is the versioned host-agent input for the platform
// PostgreSQL lifecycle. Credentials are owned by CNPG and are never accepted
// from, written to, or returned by the host agent.
type PlatformPostgresArgs struct {
	VMName          string                     `json:"vmName,omitempty"`
	ClusterName     string                     `json:"clusterName,omitempty"`
	Namespace       string                     `json:"namespace,omitempty"`
	Instances       int                        `json:"instances,omitempty"`
	StorageClass    string                     `json:"storageClass,omitempty"`
	StorageSize     string                     `json:"storageSize,omitempty"`
	RetentionPolicy string                     `json:"retentionPolicy,omitempty"`
	Relay           *PlatformPostgresRelayArgs `json:"localRelay,omitempty"`
}

type PlatformPostgresRelayArgs struct {
	SessionID  string `json:"sessionId"`
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort,omitempty"`
	TargetHost string `json:"targetHost,omitempty"`
	TargetPort int    `json:"targetPort,omitempty"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
	RelayToken string `json:"relayToken"`
}

type platformPostgresSpec struct {
	VMName          string
	ClusterName     string
	Namespace       string
	Instances       int
	StorageClass    string
	StorageSize     string
	RetentionPolicy string
}

type platformPostgresSecret struct {
	Username string
	Password string
}

type platformPostgresProbe struct {
	OperatorReady      bool
	CRDPresent         bool
	ClusterReady       bool
	PrimaryReady       bool
	ServiceReady       bool
	SecretReady        bool
	SQLReady           bool
	TaskLedgerSQLReady bool
	Blockers           []string
	PrimaryPod         string
	Username           string
	Password           string
}

func validatePlatformPostgresSpec(args PlatformPostgresArgs) (platformPostgresSpec, error) {
	spec := platformPostgresSpec{
		VMName:          strings.TrimSpace(args.VMName),
		ClusterName:     strings.TrimSpace(args.ClusterName),
		Namespace:       strings.TrimSpace(args.Namespace),
		Instances:       args.Instances,
		StorageClass:    strings.TrimSpace(args.StorageClass),
		StorageSize:     strings.TrimSpace(args.StorageSize),
		RetentionPolicy: strings.TrimSpace(args.RetentionPolicy),
	}
	if spec.VMName == "" {
		return platformPostgresSpec{}, errors.New("vmName is required")
	}
	if spec.ClusterName == "" {
		spec.ClusterName = platformPostgresClusterName
	}
	if spec.Namespace == "" {
		spec.Namespace = platformPostgresNamespace
	}
	if spec.Instances == 0 {
		spec.Instances = 1
	}
	if spec.Instances < 1 || spec.Instances > 5 {
		return platformPostgresSpec{}, errors.New("instances must be between 1 and 5")
	}
	if spec.StorageClass == "" {
		spec.StorageClass = "local-path"
	}
	if spec.StorageSize == "" {
		spec.StorageSize = platformPostgresStorageSize
	}
	if spec.RetentionPolicy == "" {
		spec.RetentionPolicy = "delete"
	}
	if spec.RetentionPolicy != "delete" && spec.RetentionPolicy != "retain" {
		return platformPostgresSpec{}, errors.New("retentionPolicy must be delete or retain")
	}
	for field, value := range map[string]string{
		"clusterName":  spec.ClusterName,
		"namespace":    spec.Namespace,
		"storageClass": spec.StorageClass,
	} {
		if !isSafeKubernetesName(value) {
			return platformPostgresSpec{}, fmt.Errorf("%s is not a valid Kubernetes name/value", field)
		}
	}
	if !isValidStorageSize(spec.StorageSize) {
		return platformPostgresSpec{}, errors.New("storageSize must be a Kubernetes quantity such as 10Gi")
	}
	return spec, nil
}

func isValidStorageSize(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 3 {
		return false
	}
	suffix := value[len(value)-2:]
	if suffix != "Mi" && suffix != "Gi" && suffix != "Ti" {
		return false
	}
	for _, char := range value[:len(value)-2] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isSafeKubernetesName(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return value[0] != '-' && value[len(value)-1] != '-'
}

func renderPlatformPostgresOperatorManifest() string {
	return `apiVersion: helm.cattle.io/v1
kind: HelmChart
metadata:
  name: cloudnativepg
  namespace: kube-system
spec:
  chart: cloudnative-pg
  repo: https://cloudnative-pg.github.io/charts
  targetNamespace: cnpg-system
  createNamespace: true
  valuesContent: |
    monitoring:
      enabled: false
`
}

func renderPlatformPostgresClusterManifest(spec platformPostgresSpec) string {
	retention := spec.RetentionPolicy
	return fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: %s
  namespace: %s
  annotations:
    opute.io/platform-postgres-retention-policy: %s
  labels:
    app.kubernetes.io/part-of: opute-platform
    opute.io/ownership: platform-postgres
spec:
  instances: %d
  imageName: ghcr.io/cloudnative-pg/postgresql:16
  managed:
    roles:
      - name: opute
        ensure: present
        login: true
        superuser: false
        createdb: true
  storage:
    size: %s
    storageClass: %s
  bootstrap:
    initdb:
      database: opute
      owner: opute
      encoding: UTF8
      localeCType: C
      localeCollate: C
`, spec.ClusterName, spec.Namespace, retention, spec.Instances, spec.StorageSize, spec.StorageClass)
}

func (s *HostOperationsService) applyPlatformPostgresManifest(ctx context.Context, spec platformPostgresSpec, manifest, label string) error {
	if _, err := s.runKubernetesKubectlWithStdinContext(ctx, spec.VMName, []string{"apply", "-f", "-"}, []byte(manifest), label, 3*time.Minute); err != nil {
		return err
	}
	return nil
}

func (s *HostOperationsService) replacePlatformPostgresConsumerSecret(ctx context.Context, spec platformPostgresSpec, manifest string) error {
	if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{
		"delete", "secret", platformPostgresConsumerSecretName,
		"-n", spec.Namespace, "--ignore-not-found=true",
	}, "replace platform PostgreSQL consumer Secret", defaultDiscoveryTimeout); err != nil {
		return err
	}
	return s.applyPlatformPostgresManifest(ctx, spec, manifest, "apply platform PostgreSQL consumer Secret")
}

func (s *HostOperationsService) ensurePlatformPostgresNamespace(ctx context.Context, spec platformPostgresSpec) error {
	for _, namespace := range []string{spec.Namespace, platformPostgresOperatorNamespace} {
		if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"get", "namespace", namespace}, "get namespace", defaultDiscoveryTimeout); err == nil {
			continue
		}
		if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"create", "namespace", namespace}, "create namespace", defaultDiscoveryTimeout); err != nil {
			return err
		}
	}
	return nil
}

func (s *HostOperationsService) platformPostgresJSON(ctx context.Context, spec platformPostgresSpec, args []string, label string) (map[string]any, error) {
	raw, err := s.runKubernetesKubectlContext(ctx, spec.VMName, append(args, "-o", "json"), label, defaultDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", label, err)
	}
	return value, nil
}

func nestedMap(value map[string]any, keys ...string) map[string]any {
	current := value
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func nestedString(value map[string]any, keys ...string) string {
	current := value
	for index, key := range keys {
		next, ok := current[key]
		if !ok {
			return ""
		}
		if index == len(keys)-1 {
			result, _ := next.(string)
			return result
		}
		current, _ = next.(map[string]any)
		if current == nil {
			return ""
		}
	}
	return ""
}

func nestedInt(value map[string]any, keys ...string) int {
	current := value
	for index, key := range keys {
		next, ok := current[key]
		if !ok {
			return 0
		}
		if index == len(keys)-1 {
			switch result := next.(type) {
			case float64:
				return int(result)
			case int:
				return result
			}
			return 0
		}
		current, _ = next.(map[string]any)
		if current == nil {
			return 0
		}
	}
	return 0
}

func (s *HostOperationsService) platformPostgresOperatorReady(ctx context.Context, spec platformPostgresSpec) (bool, bool, error) {
	crdPresent, err := s.platformPostgresCRDPresent(ctx, spec)
	if err != nil {
		return false, false, err
	}
	if !crdPresent {
		return false, false, nil
	}
	deployments, err := s.platformPostgresJSON(ctx, spec, []string{"get", "deployments", "-n", platformPostgresOperatorNamespace, "-l", "app.kubernetes.io/name=cloudnative-pg"}, "get CloudNativePG operator")
	if err != nil {
		return false, true, nil
	}
	items, _ := deployments["items"].([]any)
	for _, item := range items {
		deployment, _ := item.(map[string]any)
		desired := nestedInt(deployment, "spec", "replicas")
		available := nestedInt(deployment, "status", "availableReplicas")
		if desired > 0 && available >= desired {
			return true, true, nil
		}
	}
	return false, true, nil
}

func (s *HostOperationsService) platformPostgresClusterReady(ctx context.Context, spec platformPostgresSpec) (bool, error) {
	cluster, err := s.platformPostgresJSON(ctx, spec, []string{"get", "cluster.postgresql.cnpg.io", spec.ClusterName, "-n", spec.Namespace}, "get platform PostgreSQL Cluster")
	if err != nil {
		return false, nil
	}
	phase := strings.ToLower(nestedString(cluster, "status", "phase"))
	readyInstances := nestedInt(cluster, "status", "readyInstances")
	instances := nestedInt(cluster, "spec", "instances")
	return strings.Contains(phase, "healthy") && instances == spec.Instances && readyInstances >= spec.Instances, nil
}

func (s *HostOperationsService) platformPostgresServiceReady(ctx context.Context, spec platformPostgresSpec) (bool, error) {
	service, err := s.platformPostgresJSON(ctx, spec, []string{"get", "service", spec.ClusterName + "-rw", "-n", spec.Namespace}, "get platform PostgreSQL read/write service")
	if err != nil {
		return false, nil
	}
	if nestedString(service, "spec", "clusterIP") == "" {
		return false, nil
	}
	endpoints, err := s.platformPostgresJSON(ctx, spec, []string{"get", "endpoints", spec.ClusterName + "-rw", "-n", spec.Namespace}, "get platform PostgreSQL endpoints")
	if err != nil {
		return false, nil
	}
	subsets, _ := endpoints["subsets"].([]any)
	for _, subset := range subsets {
		item, _ := subset.(map[string]any)
		addresses, _ := item["addresses"].([]any)
		if len(addresses) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func decodeSecretValue(data map[string]any, key string) string {
	encoded, _ := data[key].(string)
	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	return string(value)
}

func (s *HostOperationsService) platformPostgresSecret(ctx context.Context, spec platformPostgresSpec) (platformPostgresSecret, bool, error) {
	secret, err := s.platformPostgresJSON(ctx, spec, []string{"get", "secret", spec.ClusterName + "-app", "-n", spec.Namespace}, "get platform PostgreSQL application secret")
	if err != nil {
		return platformPostgresSecret{}, false, nil
	}
	data, _ := secret["data"].(map[string]any)
	username := decodeSecretValue(data, "username")
	password := decodeSecretValue(data, "password")
	if username == "" || password == "" {
		return platformPostgresSecret{}, false, nil
	}
	return platformPostgresSecret{Username: username, Password: password}, true, nil
}

func (s *HostOperationsService) platformPostgresConsumerSecretReady(ctx context.Context, spec platformPostgresSpec) bool {
	secret, err := s.platformPostgresJSON(ctx, spec, []string{"get", "secret", platformPostgresConsumerSecretName, "-n", spec.Namespace}, "get platform PostgreSQL consumer Secret")
	if err != nil {
		return false
	}
	data, _ := secret["data"].(map[string]any)
	platformURL, _ := data["platformDatabaseUrl"].(string)
	taskLedgerURL, _ := data["taskLedgerDatabaseUrl"].(string)
	return strings.TrimSpace(platformURL) != "" && strings.TrimSpace(taskLedgerURL) != ""
}

func (s *HostOperationsService) platformPostgresPrimary(ctx context.Context, spec platformPostgresSpec) (string, error) {
	pods, err := s.platformPostgresJSON(ctx, spec, []string{"get", "pods", "-n", spec.Namespace, "-l", "cnpg.io/cluster=" + spec.ClusterName + ",role=primary"}, "get platform PostgreSQL primary")
	if err != nil {
		return "", nil
	}
	items, _ := pods["items"].([]any)
	for _, item := range items {
		pod, _ := item.(map[string]any)
		name := nestedString(pod, "metadata", "name")
		if name == "" || nestedString(pod, "status", "phase") != "Running" {
			continue
		}
		conditions, _ := nestedMap(pod, "status")["conditions"].([]any)
		readyCondition := false
		for _, raw := range conditions {
			condition, _ := raw.(map[string]any)
			if nestedString(condition, "type") == "Ready" && nestedString(condition, "status") == "True" {
				readyCondition = true
				break
			}
		}
		if !readyCondition {
			continue
		}
		statuses, _ := nestedMap(pod, "status")["containerStatuses"].([]any)
		if len(statuses) == 0 {
			continue
		}
		allReady := true
		for _, raw := range statuses {
			status, _ := raw.(map[string]any)
			if ready, _ := status["ready"].(bool); !ready {
				allReady = false
				break
			}
		}
		if allReady {
			return name, nil
		}
	}
	return "", nil
}

func platformPostgresSQLScript(serviceHost, username, sql string) string {
	return fmt.Sprintf(`set -eu
pgpass="$(mktemp)"
trap 'rm -f "$pgpass"' EXIT
cat >"$pgpass"
chmod 600 "$pgpass"
PGPASSFILE="$pgpass" psql -h %s -p %d -U %s -d postgres -v ON_ERROR_STOP=1 -Atqc %s
`, shellEscape(serviceHost), platformPostgresServicePort, shellEscape(username), shellEscape(sql))
}

func (s *HostOperationsService) runPlatformPostgresSQL(ctx context.Context, spec platformPostgresSpec, credentials platformPostgresSecret, pod, database, sql string) (string, error) {
	serviceHost := spec.ClusterName + "-rw." + spec.Namespace + ".svc"
	script := platformPostgresSQLScript(serviceHost, credentials.Username, fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s", shellEscape(database)))
	if database != "postgres" {
		script = fmt.Sprintf(`set -eu
pgpass="$(mktemp)"
trap 'rm -f "$pgpass"' EXIT
cat >"$pgpass"
chmod 600 "$pgpass"
PGPASSFILE="$pgpass" psql -h %s -p %d -U %s -d %s -v ON_ERROR_STOP=1 -Atqc %s
`, shellEscape(serviceHost), platformPostgresServicePort, shellEscape(credentials.Username), shellEscape(database), shellEscape(sql))
	}
	input := []byte(fmt.Sprintf("*:*:*:%s:%s\n", credentials.Username, credentials.Password))
	args := []string{"exec", "-i", pod, "-n", spec.Namespace, "--", "sh", "-ceu", script}
	return s.runKubernetesKubectlWithStdinContext(ctx, spec.VMName, args, input, "query platform PostgreSQL through read/write service", 60*time.Second)
}

func (s *HostOperationsService) ensurePlatformPostgresDatabase(ctx context.Context, spec platformPostgresSpec, credentials platformPostgresSecret, pod, database string) error {
	serviceHost := spec.ClusterName + "-rw." + spec.Namespace + ".svc"
	checkSQL := fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s", shellEscape(database))
	script := platformPostgresSQLScript(serviceHost, credentials.Username, checkSQL)
	input := []byte(fmt.Sprintf("*:*:*:%s:%s\n", credentials.Username, credentials.Password))
	args := []string{"exec", "-i", pod, "-n", spec.Namespace, "--", "sh", "-ceu", script}
	result, err := s.runKubernetesKubectlWithStdinContext(ctx, spec.VMName, args, input, "check platform PostgreSQL database", 60*time.Second)
	if err != nil {
		return err
	}
	if strings.TrimSpace(result) != "" {
		return nil
	}
	createSQL := platformPostgresCreateDatabaseSQL(database)
	script = platformPostgresSQLScript(serviceHost, credentials.Username, createSQL)
	args[len(args)-1] = script
	if _, err := s.runKubernetesKubectlWithStdinContext(ctx, spec.VMName, args, input, "create platform PostgreSQL database", 60*time.Second); err != nil {
		return err
	}
	return nil
}

func platformPostgresCreateDatabaseSQL(database string) string {
	identifier := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
	return "CREATE DATABASE " + identifier
}

func platformPostgresDatabaseURL(spec platformPostgresSpec, credentials platformPostgresSecret, database string) string {
	connection := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(credentials.Username, credentials.Password),
		Host:   fmt.Sprintf("%s-rw.%s.svc:%d", spec.ClusterName, spec.Namespace, platformPostgresServicePort),
		Path:   "/" + database,
	}
	return connection.String()
}

func renderPlatformPostgresConsumerSecret(spec platformPostgresSpec, credentials platformPostgresSecret) string {
	platformURL := platformPostgresDatabaseURL(spec, credentials, "opute")
	taskLedgerURL := platformPostgresDatabaseURL(spec, credentials, "opute_task_ledger")
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    %s: "true"
type: Opaque
data:
  platformDatabaseUrl: %s
  taskLedgerDatabaseUrl: %s
`, platformPostgresConsumerSecretName, spec.Namespace, platformPostgresConsumerSecretLabel,
		base64.StdEncoding.EncodeToString([]byte(platformURL)),
		base64.StdEncoding.EncodeToString([]byte(taskLedgerURL)))
}

func (s *HostOperationsService) restartPlatformPostgresConsumers(ctx context.Context, spec platformPostgresSpec) error {
	deployments, err := s.platformPostgresJSON(ctx, spec, []string{"get", "deployments", "-n", spec.Namespace, "-l", platformPostgresConsumerSecretLabel}, "find platform PostgreSQL consumers")
	if err != nil {
		// A CNPG-only VM may not have cell consumers yet. The Secret is still
		// ready for the next Helm reconciliation.
		return nil
	}
	items, _ := deployments["items"].([]any)
	for _, raw := range items {
		deployment, _ := raw.(map[string]any)
		name := nestedString(deployment, "metadata", "name")
		if name == "" {
			continue
		}
		if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"rollout", "restart", "deployment", name, "-n", spec.Namespace}, "restart platform PostgreSQL consumer", 2*time.Minute); err != nil {
			return err
		}
		if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"rollout", "status", "deployment", name, "-n", spec.Namespace, "--timeout=2m"}, "wait for platform PostgreSQL consumer", 3*time.Minute); err != nil {
			return err
		}
	}
	return nil
}

func (s *HostOperationsService) probePlatformPostgres(ctx context.Context, spec platformPostgresSpec) (platformPostgresProbe, error) {
	operatorReady, crdPresent, _ := s.platformPostgresOperatorReady(ctx, spec)
	clusterReady, _ := s.platformPostgresClusterReady(ctx, spec)
	serviceReady, _ := s.platformPostgresServiceReady(ctx, spec)
	credentials, secretReady, _ := s.platformPostgresSecret(ctx, spec)
	primary := ""
	if serviceReady && secretReady {
		primary, _ = s.platformPostgresPrimary(ctx, spec)
	}
	probe := platformPostgresProbe{
		OperatorReady: operatorReady,
		CRDPresent:    crdPresent,
		ClusterReady:  clusterReady,
		PrimaryReady:  primary != "",
		ServiceReady:  serviceReady,
		SecretReady:   secretReady,
		PrimaryPod:    primary,
		Username:      credentials.Username,
		Password:      credentials.Password,
	}
	if !operatorReady {
		probe.Blockers = append(probe.Blockers, "CloudNativePG operator is not ready")
	}
	if !crdPresent {
		probe.Blockers = append(probe.Blockers, "CloudNativePG Cluster CRD is not present")
	}
	if !clusterReady {
		probe.Blockers = append(probe.Blockers, "platform PostgreSQL Cluster is not healthy with the expected instance count")
	}
	if !serviceReady {
		probe.Blockers = append(probe.Blockers, "platform PostgreSQL read/write Service has no ready endpoint")
	}
	if !secretReady {
		probe.Blockers = append(probe.Blockers, "platform PostgreSQL application Secret is missing required keys")
	}
	if !probe.PrimaryReady {
		probe.Blockers = append(probe.Blockers, "platform PostgreSQL primary pod is not ready")
	}
	if probe.PrimaryReady && probe.SecretReady {
		for _, database := range []string{"postgres", "opute"} {
			if _, err := s.runPlatformPostgresSQL(ctx, spec, credentials, primary, database, "SELECT 1"); err != nil {
				probe.Blockers = append(probe.Blockers, "SQL SELECT 1 failed through the read/write Service")
				return probe, nil
			}
		}
		probe.SQLReady = true
		if _, err := s.runPlatformPostgresSQL(ctx, spec, credentials, primary, "opute_task_ledger", "SELECT 1"); err == nil {
			probe.TaskLedgerSQLReady = true
		} else {
			probe.Blockers = append(probe.Blockers, "Task Ledger PostgreSQL database is not SQL-ready")
		}
	}
	return probe, nil
}

func (s *HostOperationsService) waitForPlatformPostgres(ctx context.Context, spec platformPostgresSpec) (platformPostgresProbe, error) {
	deadline := time.NewTimer(platformPostgresReadinessTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var last platformPostgresProbe
	for {
		probe, err := s.probePlatformPostgres(ctx, spec)
		if err != nil {
			return platformPostgresProbe{}, err
		}
		last = probe
		if probe.OperatorReady && probe.CRDPresent && probe.ClusterReady && probe.ServiceReady && probe.SecretReady && probe.PrimaryReady && probe.SQLReady {
			return probe, nil
		}
		select {
		case <-ctx.Done():
			return platformPostgresProbe{}, ctx.Err()
		case <-deadline.C:
			return platformPostgresProbe{}, fmt.Errorf("platform PostgreSQL did not become SQL-ready: %s", strings.Join(last.Blockers, "; "))
		case <-ticker.C:
		}
	}
}

func (s *HostOperationsService) platformPostgresCRDPresent(ctx context.Context, spec platformPostgresSpec) (bool, error) {
	if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"get", "crd", "clusters.postgresql.cnpg.io"}, "get CloudNativePG CRD", defaultDiscoveryTimeout); err != nil {
		return false, nil
	}
	return true, nil
}

// platformPostgresWebhookReady reports whether the CloudNativePG admission
// webhook service has at least one ready endpoint. Applying the tenant Cluster
// before the webhook endpoints exist fails with "no endpoints available for
// service cnpg-webhook-service".
func (s *HostOperationsService) platformPostgresWebhookReady(ctx context.Context, spec platformPostgresSpec) (bool, error) {
	endpoints, err := s.platformPostgresJSON(ctx, spec, []string{"get", "endpoints", "cnpg-webhook-service", "-n", platformPostgresOperatorNamespace}, "get CloudNativePG webhook endpoints")
	if err != nil {
		return false, nil
	}
	subsets, _ := endpoints["subsets"].([]any)
	for _, subset := range subsets {
		item, _ := subset.(map[string]any)
		addresses, _ := item["addresses"].([]any)
		if len(addresses) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// waitForPlatformPostgresCRD waits until the CloudNativePG operator HelmChart
// has installed the clusters.postgresql.cnpg.io CRD and its admission webhook
// endpoints are ready. Applying the tenant Cluster before the CRD exists makes
// a fresh-cluster apply fail, and before the webhook endpoints exist it fails
// the admission call itself.
func (s *HostOperationsService) waitForPlatformPostgresCRD(ctx context.Context, spec platformPostgresSpec) error {
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		present, err := s.platformPostgresCRDPresent(ctx, spec)
		if err != nil {
			return err
		}
		webhookReady := false
		if present {
			webhookReady, err = s.platformPostgresWebhookReady(ctx, spec)
			if err != nil {
				return err
			}
		}
		if present && webhookReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("CloudNativePG operator did not become ready (CRD or admission webhook) after applying the HelmChart")
		case <-ticker.C:
		}
	}
}

func (s *HostOperationsService) platformPostgresK3sReady(ctx context.Context, spec platformPostgresSpec) (bool, error) {
	nodesJSON, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"get", "nodes", "-o", "json"}, "get K3s nodes", defaultDiscoveryTimeout)
	if err != nil {
		return false, nil
	}
	var nodes struct {
		Items []struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(nodesJSON), &nodes); err != nil {
		return false, fmt.Errorf("parse K3s nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return false, nil
	}
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *HostOperationsService) waitForPlatformPostgresK3sReady(ctx context.Context, spec platformPostgresSpec) error {
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ready, err := s.platformPostgresK3sReady(ctx, spec)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("K3s did not report a Ready node before platform PostgreSQL reconciliation")
		case <-ticker.C:
		}
	}
}

// ensurePlatformPostgresOrdered runs the documented fresh-cluster sequence:
// K3s readiness, the operator HelmChart, a wait for the CNPG Cluster CRD, and
// only then the tenant Cluster apply. Reusing the standalone CRD wait pattern
// prevents a fresh-cluster apply from racing the operator installation.
func (s *HostOperationsService) ensurePlatformPostgresOrdered(ctx context.Context, spec platformPostgresSpec) error {
	if err := s.waitForPlatformPostgresK3sReady(ctx, spec); err != nil {
		return err
	}
	if err := s.applyPlatformPostgresManifest(ctx, spec, renderPlatformPostgresOperatorManifest(), "apply CloudNativePG HelmChart"); err != nil {
		return err
	}
	if err := s.waitForPlatformPostgresCRD(ctx, spec); err != nil {
		return err
	}
	if err := s.applyPlatformPostgresManifest(ctx, spec, renderPlatformPostgresClusterManifest(spec), "apply platform PostgreSQL Cluster"); err != nil {
		return err
	}
	return nil
}

func (s *HostOperationsService) EnsurePlatformPostgres(ctx context.Context, args PlatformPostgresArgs, _ func(string)) (map[string]any, error) {
	spec, err := validatePlatformPostgresSpec(args)
	if err != nil {
		return nil, err
	}
	if err := s.ensurePlatformPostgresNamespace(ctx, spec); err != nil {
		return nil, err
	}
	if err := s.ensurePlatformPostgresOrdered(ctx, spec); err != nil {
		return nil, err
	}
	if _, err := s.waitForPlatformPostgres(ctx, spec); err != nil {
		return nil, err
	}
	probe, _ := s.probePlatformPostgres(ctx, spec)
	credentials := platformPostgresSecret{Username: probe.Username, Password: probe.Password}
	for _, database := range []string{"opute", "opute_task_ledger"} {
		if err := s.ensurePlatformPostgresDatabase(ctx, spec, credentials, probe.PrimaryPod, database); err != nil {
			return nil, err
		}
	}
	probe, _ = s.probePlatformPostgres(ctx, spec)
	if !probe.SQLReady || !probe.TaskLedgerSQLReady {
		return nil, errors.New("platform PostgreSQL databases did not become SQL-ready")
	}
	result := map[string]any{
		"ready":              false,
		"vmName":             spec.VMName,
		"namespace":          spec.Namespace,
		"clusterName":        spec.ClusterName,
		"instances":          spec.Instances,
		"storageClass":       spec.StorageClass,
		"storageSize":        spec.StorageSize,
		"serviceName":        spec.ClusterName + "-rw",
		"servicePort":        platformPostgresServicePort,
		"secretName":         platformPostgresConsumerSecretName,
		"cnpgSecretName":     spec.ClusterName + "-app",
		"databases":          []string{"opute", "opute_task_ledger"},
		"sqlReady":           probe.SQLReady && probe.TaskLedgerSQLReady,
		"taskLedgerSqlReady": probe.TaskLedgerSQLReady,
		"credentialState":    "cnpg-owned",
		"postgresVersion":    "cloudnativepg",
	}
	if err := s.replacePlatformPostgresConsumerSecret(ctx, spec, renderPlatformPostgresConsumerSecret(spec, credentials)); err != nil {
		return nil, err
	}
	if err := s.restartPlatformPostgresConsumers(ctx, spec); err != nil {
		return nil, err
	}
	consumerSecretReady := s.platformPostgresConsumerSecretReady(ctx, spec)
	result["consumerSecretReady"] = consumerSecretReady
	result["ready"] = probe.SQLReady && probe.TaskLedgerSQLReady && consumerSecretReady
	if args.Relay != nil {
		relay, err := s.ensurePlatformPostgresRelay(ctx, spec, *args.Relay)
		if err != nil {
			return nil, err
		}
		result["localRelay"] = relay
	}
	return result, nil
}

func (s *HostOperationsService) GetPlatformPostgresStatus(ctx context.Context, args PlatformPostgresArgs) (map[string]any, error) {
	spec, err := validatePlatformPostgresSpec(args)
	if err != nil {
		return nil, err
	}
	probe, err := s.probePlatformPostgres(ctx, spec)
	if err != nil {
		return nil, err
	}
	consumerSecretReady := s.platformPostgresConsumerSecretReady(ctx, spec)
	return map[string]any{
		"ready":               probe.OperatorReady && probe.CRDPresent && probe.ClusterReady && probe.ServiceReady && probe.SecretReady && probe.PrimaryReady && probe.SQLReady && probe.TaskLedgerSQLReady && consumerSecretReady,
		"operatorReady":       probe.OperatorReady,
		"crdPresent":          probe.CRDPresent,
		"clusterReady":        probe.ClusterReady,
		"serviceReady":        probe.ServiceReady,
		"secretReady":         probe.SecretReady,
		"primaryReady":        probe.PrimaryReady,
		"sqlReady":            probe.SQLReady && probe.TaskLedgerSQLReady,
		"taskLedgerSqlReady":  probe.TaskLedgerSQLReady,
		"consumerSecretReady": consumerSecretReady,
		"blockers":            probe.Blockers,
		"vmName":              spec.VMName,
		"namespace":           spec.Namespace,
		"clusterName":         spec.ClusterName,
		"serviceName":         spec.ClusterName + "-rw",
		"secretName":          platformPostgresConsumerSecretName,
		"cnpgSecretName":      spec.ClusterName + "-app",
		"credentialState":     "cnpg-owned",
	}, nil
}

func (s *HostOperationsService) RemovePlatformPostgres(ctx context.Context, args PlatformPostgresArgs, confirm bool) (map[string]any, error) {
	if !confirm {
		return nil, errors.New("remove_platform_postgres requires confirm=true")
	}
	spec, err := validatePlatformPostgresSpec(args)
	if err != nil {
		return nil, err
	}
	if _, err := s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"delete", "cluster.postgresql.cnpg.io", spec.ClusterName, "-n", spec.Namespace, "--ignore-not-found=true", "--wait=true"}, "delete platform PostgreSQL Cluster", 5*time.Minute); err != nil {
		return nil, err
	}
	if spec.RetentionPolicy == "delete" {
		_, _ = s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"delete", "secret", spec.ClusterName + "-app", "-n", spec.Namespace, "--ignore-not-found=true"}, "delete platform PostgreSQL Secret", defaultDiscoveryTimeout)
		_, _ = s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"delete", "secret", platformPostgresConsumerSecretName, "-n", spec.Namespace, "--ignore-not-found=true"}, "delete platform PostgreSQL consumer Secret", defaultDiscoveryTimeout)
		_, _ = s.runKubernetesKubectlContext(ctx, spec.VMName, []string{"delete", "pvc", "-l", "cnpg.io/cluster=" + spec.ClusterName, "-n", spec.Namespace, "--ignore-not-found=true"}, "delete platform PostgreSQL PVCs", 5*time.Minute)
	}
	s.revokeAllPlatformPostgresRelays()
	return map[string]any{
		"removed":           true,
		"vmName":            spec.VMName,
		"namespace":         spec.Namespace,
		"clusterName":       spec.ClusterName,
		"operatorPreserved": true,
	}, nil
}
