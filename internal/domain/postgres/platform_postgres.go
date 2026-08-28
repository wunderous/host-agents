package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/textutil"
)

const (
	postgresqlServiceOperatorRelease   = "cloudnativepg"
	postgresqlServiceOperatorNamespace = "cnpg-system"
	postgresqlServiceStorageSize       = "10Gi"
	postgresqlServicePort              = 5432
	postgresqlServiceReadinessTimeout  = 15 * time.Minute
)

// PostgreSQLServiceArgs is the versioned host-agent input for the platform
// PostgreSQL lifecycle. Credentials are owned by CNPG and are never accepted
// from, written to, or returned by the host agent.
type PostgreSQLServiceArgs struct {
	VMName           string                      `json:"vmName,omitempty"`
	ClusterName      string                      `json:"clusterName,omitempty"`
	Namespace        string                      `json:"namespace,omitempty"`
	Instances        int                         `json:"instances,omitempty"`
	StorageClass     string                      `json:"storageClass,omitempty"`
	StorageSize      string                      `json:"storageSize,omitempty"`
	RetentionPolicy  string                      `json:"retentionPolicy,omitempty"`
	RestartConsumers *bool                       `json:"restartConsumers,omitempty"`
	Relay            *PostgreSQLServiceRelayArgs `json:"localRelay,omitempty"`
	// Generic callers may provide service-owned database and projection names.
	// Empty values preserve the legacy compatibility defaults for existing
	// clients while the neutral operation requires them at the dispatch layer.
	Databases            []string          `json:"databases,omitempty"`
	ConsumerSecretName   string            `json:"consumerSecretName,omitempty"`
	ConsumerSecretLabel  string            `json:"consumerSecretLabel,omitempty"`
	ServiceOwner         string            `json:"serviceOwner,omitempty"`
	ServicePartOf        string            `json:"servicePartOf,omitempty"`
	ConsumerDatabaseKeys map[string]string `json:"consumerDatabaseKeys,omitempty"`
	RelayDeviceName      string            `json:"relayDeviceName,omitempty"`
}

type PostgreSQLServiceRelayArgs struct {
	SessionID  string `json:"sessionId"`
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort,omitempty"`
	TargetHost string `json:"targetHost,omitempty"`
	TargetPort int    `json:"targetPort,omitempty"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
	RelayToken string `json:"relayToken"`
	Persistent bool   `json:"persistent,omitempty"`
	// ReplaceExisting permits an explicit persistent recovery handoff when a
	// previous owner exited without releasing its relay. The manager still
	// refuses the handoff while authenticated connections are active.
	ReplaceExisting bool `json:"replaceExisting,omitempty"`
}

type postgresqlServiceSpec struct {
	VMName               string
	ClusterName          string
	Namespace            string
	Instances            int
	StorageClass         string
	StorageSize          string
	RetentionPolicy      string
	Databases            []string
	ConsumerSecretName   string
	ConsumerSecretLabel  string
	ServiceOwner         string
	ServicePartOf        string
	ConsumerDatabaseKeys map[string]string
	RelayDeviceName      string
}

type postgresqlServiceSecret struct {
	Username string
	Password string
}

type postgresqlServiceProbe struct {
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

func validatePostgreSQLServiceSpec(args PostgreSQLServiceArgs) (postgresqlServiceSpec, error) {
	spec := postgresqlServiceSpec{
		VMName:          strings.TrimSpace(args.VMName),
		ClusterName:     strings.TrimSpace(args.ClusterName),
		Namespace:       strings.TrimSpace(args.Namespace),
		Instances:       args.Instances,
		StorageClass:    strings.TrimSpace(args.StorageClass),
		StorageSize:     strings.TrimSpace(args.StorageSize),
		RetentionPolicy: strings.TrimSpace(args.RetentionPolicy),
		RelayDeviceName: strings.TrimSpace(args.RelayDeviceName),
	}
	if spec.VMName == "" {
		return postgresqlServiceSpec{}, errors.New("vmName is required")
	}
	if spec.ClusterName == "" {
		return postgresqlServiceSpec{}, errors.New("clusterName is required")
	}
	if spec.Namespace == "" {
		return postgresqlServiceSpec{}, errors.New("namespace is required")
	}
	if spec.Instances == 0 {
		spec.Instances = 1
	}
	if spec.Instances < 1 || spec.Instances > 5 {
		return postgresqlServiceSpec{}, errors.New("instances must be between 1 and 5")
	}
	if spec.StorageClass == "" {
		spec.StorageClass = "local-path"
	}
	if spec.StorageSize == "" {
		spec.StorageSize = postgresqlServiceStorageSize
	}
	if spec.RetentionPolicy == "" {
		spec.RetentionPolicy = "delete"
	}
	if spec.RetentionPolicy != "delete" && spec.RetentionPolicy != "retain" {
		return postgresqlServiceSpec{}, errors.New("retentionPolicy must be delete or retain")
	}
	if len(args.Databases) == 0 {
		return postgresqlServiceSpec{}, errors.New("databases must contain at least one database")
	} else {
		seen := map[string]bool{}
		for _, raw := range args.Databases {
			database := strings.TrimSpace(raw)
			if !postgresDatabaseIdentifier.MatchString(database) || seen[database] {
				return postgresqlServiceSpec{}, errors.New("databases must be unique lowercase SQL identifiers")
			}
			seen[database] = true
			spec.Databases = append(spec.Databases, database)
		}
	}
	spec.ConsumerSecretName = strings.TrimSpace(args.ConsumerSecretName)
	if spec.ConsumerSecretName == "" {
		return postgresqlServiceSpec{}, errors.New("consumerSecretName is required")
	}
	spec.ConsumerSecretLabel = strings.TrimSpace(args.ConsumerSecretLabel)
	if spec.ConsumerSecretLabel == "" {
		return postgresqlServiceSpec{}, errors.New("consumerSecretLabel is required")
	}
	spec.ServiceOwner = strings.TrimSpace(args.ServiceOwner)
	if spec.ServiceOwner == "" {
		return postgresqlServiceSpec{}, errors.New("serviceOwner is required")
	}
	spec.ServicePartOf = strings.TrimSpace(args.ServicePartOf)
	if spec.ServicePartOf == "" {
		return postgresqlServiceSpec{}, errors.New("servicePartOf is required")
	}
	if spec.RelayDeviceName == "" && args.Relay != nil {
		return postgresqlServiceSpec{}, errors.New("relayDeviceName is required when localRelay is requested")
	}
	spec.ConsumerDatabaseKeys = map[string]string{}
	for _, database := range spec.Databases {
		if key := strings.TrimSpace(args.ConsumerDatabaseKeys[database]); key != "" {
			spec.ConsumerDatabaseKeys[database] = key
		}
	}
	if len(spec.ConsumerDatabaseKeys) == 0 {
		return postgresqlServiceSpec{}, errors.New("consumerDatabaseKeys must map every database to a Secret key")
	}
	for _, database := range spec.Databases {
		if strings.TrimSpace(spec.ConsumerDatabaseKeys[database]) == "" {
			return postgresqlServiceSpec{}, fmt.Errorf("consumerDatabaseKeys missing key for database %q", database)
		}
	}
	for field, value := range map[string]string{
		"consumerSecretName": spec.ConsumerSecretName,
		"serviceOwner":       spec.ServiceOwner,
		"servicePartOf":      spec.ServicePartOf,
	} {
		if !isSafeKubernetesName(value) {
			return postgresqlServiceSpec{}, fmt.Errorf("%s is not a valid Kubernetes name/value", field)
		}
	}
	for field, value := range map[string]string{
		"clusterName":  spec.ClusterName,
		"namespace":    spec.Namespace,
		"storageClass": spec.StorageClass,
	} {
		if !isSafeKubernetesName(value) {
			return postgresqlServiceSpec{}, fmt.Errorf("%s is not a valid Kubernetes name/value", field)
		}
	}
	if !isValidStorageSize(spec.StorageSize) {
		return postgresqlServiceSpec{}, errors.New("storageSize must be a Kubernetes quantity such as 10Gi")
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

func renderPostgreSQLServiceOperatorManifest() string {
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

func renderPostgreSQLServiceClusterManifest(spec postgresqlServiceSpec) string {
	primaryDatabase := spec.Databases[0]
	retentionAnnotation := "host-agent.io/retention-policy"
	return fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: %s
  namespace: %s
  annotations:
    %s: %s
  labels:
    app.kubernetes.io/part-of: %s
    app.kubernetes.io/managed-by: host-agent
spec:
  instances: %d
  imageName: ghcr.io/cloudnative-pg/postgresql:16
  managed:
    roles:
      - name: %s
        ensure: present
        login: true
        superuser: false
        createdb: true
  storage:
    size: %s
    storageClass: %s
  bootstrap:
    initdb:
      database: %s
      owner: %s
      encoding: UTF8
      localeCType: C
      localeCollate: C
`, spec.ClusterName, spec.Namespace, retentionAnnotation, spec.RetentionPolicy, spec.ServicePartOf, spec.Instances, spec.ServiceOwner, spec.StorageSize, spec.StorageClass, primaryDatabase, spec.ServiceOwner)
}

func (s *Service) applyPostgreSQLServiceManifest(ctx context.Context, spec postgresqlServiceSpec, manifest, label string) error {
	if _, err := s.deps.RunKubectlWithStdinContext(ctx, spec.VMName, []string{"apply", "-f", "-"}, []byte(manifest), label, 3*time.Minute); err != nil {
		return err
	}
	return nil
}

func (s *Service) replacePostgreSQLServiceConsumerSecret(ctx context.Context, spec postgresqlServiceSpec, manifest string) error {
	if _, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{
		"delete", "secret", spec.ConsumerSecretName,
		"-n", spec.Namespace, "--ignore-not-found=true",
	}, "replace PostgreSQL service consumer Secret", defaultDiscoveryTimeout); err != nil {
		return err
	}
	return s.applyPostgreSQLServiceManifest(ctx, spec, manifest, "apply PostgreSQL service consumer Secret")
}

func (s *Service) ensurePostgreSQLServiceNamespace(ctx context.Context, spec postgresqlServiceSpec) error {
	for _, namespace := range []string{spec.Namespace, postgresqlServiceOperatorNamespace} {
		if _, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{"get", "namespace", namespace}, "get namespace", defaultDiscoveryTimeout); err == nil {
			continue
		}
		if _, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{"create", "namespace", namespace}, "create namespace", defaultDiscoveryTimeout); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) postgresqlServiceJSON(ctx context.Context, spec postgresqlServiceSpec, args []string, label string) (map[string]any, error) {
	raw, err := s.deps.RunKubectlContext(ctx, spec.VMName, append(args, "-o", "json"), label, defaultDiscoveryTimeout)
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

func (s *Service) postgresqlServiceOperatorReady(ctx context.Context, spec postgresqlServiceSpec) (bool, bool, error) {
	crdPresent, err := s.postgresqlServiceCRDPresent(ctx, spec)
	if err != nil {
		return false, false, err
	}
	if !crdPresent {
		return false, false, nil
	}
	deployments, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "deployments", "-n", postgresqlServiceOperatorNamespace, "-l", "app.kubernetes.io/name=cloudnative-pg"}, "get CloudNativePG operator")
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

func (s *Service) postgresqlServiceClusterReady(ctx context.Context, spec postgresqlServiceSpec) (bool, error) {
	cluster, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "cluster.postgresql.cnpg.io", spec.ClusterName, "-n", spec.Namespace}, "get PostgreSQL service Cluster")
	if err != nil {
		return false, nil
	}
	phase := strings.ToLower(nestedString(cluster, "status", "phase"))
	readyInstances := nestedInt(cluster, "status", "readyInstances")
	instances := nestedInt(cluster, "spec", "instances")
	return strings.Contains(phase, "healthy") && instances == spec.Instances && readyInstances >= spec.Instances, nil
}

func (s *Service) postgresqlServiceReady(ctx context.Context, spec postgresqlServiceSpec) (bool, error) {
	service, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "service", spec.ClusterName + "-rw", "-n", spec.Namespace}, "get PostgreSQL service read/write service")
	if err != nil {
		return false, nil
	}
	if nestedString(service, "spec", "clusterIP") == "" {
		return false, nil
	}
	endpoints, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "endpoints", spec.ClusterName + "-rw", "-n", spec.Namespace}, "get PostgreSQL service endpoints")
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

func (s *Service) postgresqlServiceSecret(ctx context.Context, spec postgresqlServiceSpec) (postgresqlServiceSecret, bool, error) {
	secret, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "secret", spec.ClusterName + "-app", "-n", spec.Namespace}, "get PostgreSQL service application secret")
	if err != nil {
		return postgresqlServiceSecret{}, false, nil
	}
	data, _ := secret["data"].(map[string]any)
	username := decodeSecretValue(data, "username")
	password := decodeSecretValue(data, "password")
	if username == "" || password == "" {
		return postgresqlServiceSecret{}, false, nil
	}
	return postgresqlServiceSecret{Username: username, Password: password}, true, nil
}

func (s *Service) postgresqlServiceConsumerSecretReady(ctx context.Context, spec postgresqlServiceSpec) bool {
	secret, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "secret", spec.ConsumerSecretName, "-n", spec.Namespace}, "get PostgreSQL consumer Secret")
	if err != nil {
		return false
	}
	data, _ := secret["data"].(map[string]any)
	for _, key := range spec.ConsumerDatabaseKeys {
		value, _ := data[key].(string)
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return len(spec.ConsumerDatabaseKeys) > 0
}

func (s *Service) postgresqlServicePrimary(ctx context.Context, spec postgresqlServiceSpec) (string, error) {
	pods, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "pods", "-n", spec.Namespace, "-l", "cnpg.io/cluster=" + spec.ClusterName + ",role=primary"}, "get PostgreSQL service primary")
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

func postgresqlServiceSQLScript(serviceHost, username, database, sql string) string {
	return fmt.Sprintf(`set -eu
pgpass_dir="${TMPDIR:-/controller/tmp}"
if [ ! -d "$pgpass_dir" ] || [ ! -w "$pgpass_dir" ]; then
  pgpass_dir="/tmp"
fi
pgpass="$(mktemp "$pgpass_dir/opute-pgpass.XXXXXX")"
trap 'rm -f "$pgpass"' EXIT
cat >"$pgpass"
chmod 600 "$pgpass"
PGPASSFILE="$pgpass" psql -h %s -p %d -U %s -d %s -v ON_ERROR_STOP=1 -Atqc %s
`, textutil.ShellQuote(serviceHost), postgresqlServicePort, textutil.ShellQuote(username), textutil.ShellQuote(database), textutil.ShellQuote(sql))
}

// Kubernetes provider exec arguments are single-line transport values. Keep
// the SQL helper's multiline shell script inside the Host Agent boundary and
// materialize it as a bounded base64 payload for the provider's shell, rather
// than weakening the provider's argument validation for one caller.
func kubectlShellScriptArgument(script string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	// Decode into the current shell with eval so the script retains the
	// kubectl exec stdin stream for its ephemeral pgpass file. Piping the
	// decoded script into a nested sh would consume that same stream.
	return "eval \"$(printf '%s' '" + encoded + "' | base64 -d)\""
}

func (s *Service) runPostgreSQLServiceSQL(ctx context.Context, spec postgresqlServiceSpec, credentials postgresqlServiceSecret, pod, database, sql string) (string, error) {
	serviceHost := spec.ClusterName + "-rw." + spec.Namespace + ".svc"
	script := postgresqlServiceSQLScript(
		serviceHost,
		credentials.Username,
		database,
		fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s", textutil.ShellQuote(database)),
	)
	input := []byte(fmt.Sprintf("*:*:*:%s:%s\n", credentials.Username, credentials.Password))
	args := []string{"exec", "-i", pod, "-n", spec.Namespace, "--", "sh", "-ceu", kubectlShellScriptArgument(script)}
	return s.deps.RunKubectlWithStdinContext(ctx, spec.VMName, args, input, "query PostgreSQL service through read/write service", 60*time.Second)
}

func (s *Service) ensurePostgreSQLServiceDatabase(ctx context.Context, spec postgresqlServiceSpec, credentials postgresqlServiceSecret, pod, database string) error {
	serviceHost := spec.ClusterName + "-rw." + spec.Namespace + ".svc"
	checkSQL := fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s", textutil.ShellQuote(database))
	script := postgresqlServiceSQLScript(serviceHost, credentials.Username, "postgres", checkSQL)
	input := []byte(fmt.Sprintf("*:*:*:%s:%s\n", credentials.Username, credentials.Password))
	args := []string{"exec", "-i", pod, "-n", spec.Namespace, "--", "sh", "-ceu", kubectlShellScriptArgument(script)}
	result, err := s.deps.RunKubectlWithStdinContext(ctx, spec.VMName, args, input, "check PostgreSQL service database", 60*time.Second)
	if err != nil {
		return err
	}
	if strings.TrimSpace(result) != "" {
		return nil
	}
	createSQL := postgresqlServiceCreateDatabaseSQL(database)
	script = postgresqlServiceSQLScript(serviceHost, credentials.Username, "postgres", createSQL)
	args[len(args)-1] = script
	if _, err := s.deps.RunKubectlWithStdinContext(ctx, spec.VMName, args, input, "create PostgreSQL service database", 60*time.Second); err != nil {
		return err
	}
	return nil
}

func postgresqlServiceCreateDatabaseSQL(database string) string {
	identifier := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
	return "CREATE DATABASE " + identifier
}

func postgresqlServiceDatabaseURL(spec postgresqlServiceSpec, credentials postgresqlServiceSecret, database string) string {
	connection := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(credentials.Username, credentials.Password),
		Host:   fmt.Sprintf("%s-rw.%s.svc:%d", spec.ClusterName, spec.Namespace, postgresqlServicePort),
		Path:   "/" + database,
	}
	return connection.String()
}

func renderPostgreSQLServiceConsumerSecret(spec postgresqlServiceSpec, credentials postgresqlServiceSecret) string {
	data := ""
	for _, database := range spec.Databases {
		data += fmt.Sprintf("  %s: %s\n", spec.ConsumerDatabaseKeys[database], base64.StdEncoding.EncodeToString([]byte(postgresqlServiceDatabaseURL(spec, credentials, database))))
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    %s: "true"
type: Opaque
data:
%s`, spec.ConsumerSecretName, spec.Namespace, spec.ConsumerSecretLabel, data)
}

func (s *Service) restartPostgreSQLServiceConsumers(ctx context.Context, spec postgresqlServiceSpec) error {
	deployments, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "deployments", "-n", spec.Namespace, "-l", spec.ConsumerSecretLabel}, "find PostgreSQL consumers")
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
		if _, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{"rollout", "restart", "deployment", name, "-n", spec.Namespace}, "restart PostgreSQL service consumer", 2*time.Minute); err != nil {
			return err
		}
		if _, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{"rollout", "status", "deployment", name, "-n", spec.Namespace, "--timeout=2m"}, "wait for PostgreSQL service consumer", 3*time.Minute); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) probePostgreSQLService(ctx context.Context, spec postgresqlServiceSpec) (postgresqlServiceProbe, error) {
	operatorReady, crdPresent, _ := s.postgresqlServiceOperatorReady(ctx, spec)
	clusterReady, _ := s.postgresqlServiceClusterReady(ctx, spec)
	serviceReady, _ := s.postgresqlServiceReady(ctx, spec)
	credentials, secretReady, _ := s.postgresqlServiceSecret(ctx, spec)
	primary := ""
	if serviceReady && secretReady {
		primary, _ = s.postgresqlServicePrimary(ctx, spec)
	}
	probe := postgresqlServiceProbe{
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
		probe.Blockers = append(probe.Blockers, "PostgreSQL service Cluster is not healthy with the expected instance count")
	}
	if !serviceReady {
		probe.Blockers = append(probe.Blockers, "PostgreSQL service read/write Service has no ready endpoint")
	}
	if !secretReady {
		probe.Blockers = append(probe.Blockers, "PostgreSQL service application Secret is missing required keys")
	}
	if !probe.PrimaryReady {
		probe.Blockers = append(probe.Blockers, "PostgreSQL service primary pod is not ready")
	}
	if probe.PrimaryReady && probe.SecretReady {
		if _, err := s.runPostgreSQLServiceSQL(ctx, spec, credentials, primary, "postgres", "SELECT 1"); err != nil {
			probe.Blockers = append(probe.Blockers, fmt.Sprintf("SQL SELECT 1 failed through the read/write Service for postgres: %s", err.Error()))
			return probe, nil
		}
		probe.SQLReady = true
		probe.TaskLedgerSQLReady = true
		for _, database := range spec.Databases {
			if _, err := s.runPostgreSQLServiceSQL(ctx, spec, credentials, primary, database, "SELECT 1"); err != nil {
				probe.TaskLedgerSQLReady = false
				probe.Blockers = append(probe.Blockers, fmt.Sprintf("SQL SELECT 1 failed through the read/write Service for %s: %s", database, err.Error()))
			}
		}
	}
	return probe, nil
}

func (s *Service) waitForPostgreSQLService(ctx context.Context, spec postgresqlServiceSpec) (postgresqlServiceProbe, error) {
	deadline := time.NewTimer(postgresqlServiceReadinessTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var last postgresqlServiceProbe
	for {
		probe, err := s.probePostgreSQLService(ctx, spec)
		if err != nil {
			return postgresqlServiceProbe{}, err
		}
		last = probe
		if postgresqlServiceInfrastructureReady(probe) {
			return probe, nil
		}
		select {
		case <-ctx.Done():
			return postgresqlServiceProbe{}, ctx.Err()
		case <-deadline.C:
			return postgresqlServiceProbe{}, fmt.Errorf("PostgreSQL service did not become SQL-ready: %s", strings.Join(last.Blockers, "; "))
		case <-ticker.C:
		}
	}
}

func (s *Service) postgresqlServiceCRDPresent(ctx context.Context, spec postgresqlServiceSpec) (bool, error) {
	if _, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{"get", "crd", "clusters.postgresql.cnpg.io"}, "get CloudNativePG CRD", defaultDiscoveryTimeout); err != nil {
		return false, nil
	}
	return true, nil
}

// postgresqlServiceWebhookReady reports whether the CloudNativePG admission
// webhook service has at least one ready endpoint. Applying the tenant Cluster
// before the webhook endpoints exist fails with "no endpoints available for
// service cnpg-webhook-service".
func (s *Service) postgresqlServiceWebhookReady(ctx context.Context, spec postgresqlServiceSpec) (bool, error) {
	endpoints, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "endpoints", "cnpg-webhook-service", "-n", postgresqlServiceOperatorNamespace}, "get CloudNativePG webhook endpoints")
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

// waitForPostgreSQLServiceCRD waits until the CloudNativePG operator HelmChart
// has installed the clusters.postgresql.cnpg.io CRD and its admission webhook
// endpoints are ready. Applying the tenant Cluster before the CRD exists makes
// a fresh-cluster apply fail, and before the webhook endpoints exist it fails
// the admission call itself.
func (s *Service) waitForPostgreSQLServiceCRD(ctx context.Context, spec postgresqlServiceSpec) error {
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		present, err := s.postgresqlServiceCRDPresent(ctx, spec)
		if err != nil {
			return err
		}
		webhookReady := false
		if present {
			webhookReady, err = s.postgresqlServiceWebhookReady(ctx, spec)
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

func (s *Service) postgresqlServiceK3sReady(ctx context.Context, spec postgresqlServiceSpec) (bool, error) {
	nodesJSON, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{"get", "nodes", "-o", "json"}, "get K3s nodes", defaultDiscoveryTimeout)
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

func (s *Service) waitForPostgreSQLServiceK3sReady(ctx context.Context, spec postgresqlServiceSpec) error {
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ready, err := s.postgresqlServiceK3sReady(ctx, spec)
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
			return errors.New("K3s did not report a Ready node before PostgreSQL service reconciliation")
		case <-ticker.C:
		}
	}
}

// ensurePostgreSQLServiceOrdered runs the documented fresh-cluster sequence:
// K3s readiness, the operator HelmChart, a wait for the CNPG Cluster CRD, and
// only then the tenant Cluster apply. Reusing the standalone CRD wait pattern
// prevents a fresh-cluster apply from racing the operator installation.
func (s *Service) ensurePostgreSQLServiceOrdered(ctx context.Context, spec postgresqlServiceSpec) error {
	if err := s.waitForPostgreSQLServiceK3sReady(ctx, spec); err != nil {
		return err
	}
	if err := s.applyPostgreSQLServiceManifest(ctx, spec, renderPostgreSQLServiceOperatorManifest(), "apply CloudNativePG HelmChart"); err != nil {
		return err
	}
	if err := s.waitForPostgreSQLServiceCRD(ctx, spec); err != nil {
		return err
	}
	if err := s.applyPostgreSQLServiceManifest(ctx, spec, renderPostgreSQLServiceClusterManifest(spec), "apply PostgreSQL service Cluster"); err != nil {
		return err
	}
	return nil
}

func postgresqlServiceProbeReady(probe postgresqlServiceProbe) bool {
	return probe.OperatorReady && probe.CRDPresent && probe.ClusterReady && probe.ServiceReady && probe.SecretReady && probe.PrimaryReady && probe.SQLReady && probe.TaskLedgerSQLReady
}

// postgresqlServiceInfrastructureReady is the convergence gate used before
// service-owned databases are reconciled. The system database must be
// reachable, but a configured database may legitimately be absent until the
// reconciliation step creates it.
func postgresqlServiceInfrastructureReady(probe postgresqlServiceProbe) bool {
	return probe.OperatorReady && probe.CRDPresent && probe.ClusterReady && probe.ServiceReady && probe.SecretReady && probe.PrimaryReady && probe.SQLReady
}

// probePostgreSQLServiceStable gives an already-running service a short
// convergence window before the repair path is selected. K3s can briefly
// return an incomplete object set while the API server and kubelet restore
// watches; treating that single observation as a missing installation causes
// an unnecessary Helm/Cluster reapply and can make the outage self-sustaining.
func (s *Service) probePostgreSQLServiceStable(ctx context.Context, spec postgresqlServiceSpec) (postgresqlServiceProbe, error) {
	var last postgresqlServiceProbe
	for attempt := 0; attempt < 3; attempt++ {
		probe, err := s.probePostgreSQLService(ctx, spec)
		if err != nil {
			return postgresqlServiceProbe{}, err
		}
		last = probe
		if postgresqlServiceProbeReady(probe) {
			return probe, nil
		}
		if attempt < 2 {
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return postgresqlServiceProbe{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return last, nil
}

func (s *Service) ReconcilePostgreSQLService(ctx context.Context, args PostgreSQLServiceArgs, _ func(string)) (map[string]any, error) {
	spec, err := validatePostgreSQLServiceSpec(args)
	if err != nil {
		return nil, err
	}
	if err := s.ensurePostgreSQLServiceNamespace(ctx, spec); err != nil {
		return nil, err
	}
	// Reconciliation is also the steady-state bootstrap path. Probe first so a
	// ready service does not reinstall the operator, reapply the tenant Cluster,
	// and tear down consumers on every caller restart. An incomplete service
	// still follows the ordered repair path below.
	probe, probeErr := s.probePostgreSQLServiceStable(ctx, spec)
	if probeErr != nil || !postgresqlServiceInfrastructureReady(probe) {
		if err := s.ensurePostgreSQLServiceOrdered(ctx, spec); err != nil {
			return nil, err
		}
		if probe, err = s.waitForPostgreSQLService(ctx, spec); err != nil {
			return nil, err
		}
	}
	credentials := postgresqlServiceSecret{Username: probe.Username, Password: probe.Password}
	for _, database := range spec.Databases {
		if err := s.ensurePostgreSQLServiceDatabase(ctx, spec, credentials, probe.PrimaryPod, database); err != nil {
			return nil, err
		}
	}
	probe, _ = s.probePostgreSQLService(ctx, spec)
	if !probe.SQLReady || !probe.TaskLedgerSQLReady {
		return nil, errors.New("PostgreSQL service databases did not become SQL-ready")
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
		"servicePort":        postgresqlServicePort,
		"secretName":         spec.ConsumerSecretName,
		"cnpgSecretName":     spec.ClusterName + "-app",
		"databases":          append([]string(nil), spec.Databases...),
		"sqlReady":           probe.SQLReady && probe.TaskLedgerSQLReady,
		"taskLedgerSqlReady": probe.TaskLedgerSQLReady,
		"credentialState":    "cnpg-owned",
		"postgresVersion":    "cloudnativepg",
	}
	if err := s.replacePostgreSQLServiceConsumerSecret(ctx, spec, renderPostgreSQLServiceConsumerSecret(spec, credentials)); err != nil {
		return nil, err
	}
	if args.RestartConsumers == nil || *args.RestartConsumers {
		if err := s.restartPostgreSQLServiceConsumers(ctx, spec); err != nil {
			return nil, err
		}
	}
	consumerSecretReady := s.postgresqlServiceConsumerSecretReady(ctx, spec)
	result["consumerSecretReady"] = consumerSecretReady
	result["ready"] = probe.SQLReady && consumerSecretReady
	if args.Relay != nil {
		relay, err := s.ensurePostgreSQLServiceRelay(ctx, spec, *args.Relay)
		if err != nil {
			return nil, err
		}
		result["localRelay"] = relay
	}
	return result, nil
}

func (s *Service) GetPostgreSQLServiceStatus(ctx context.Context, args PostgreSQLServiceArgs) (map[string]any, error) {
	spec, err := validatePostgreSQLServiceSpec(args)
	if err != nil {
		return nil, err
	}
	probe, err := s.probePostgreSQLService(ctx, spec)
	if err != nil {
		return nil, err
	}
	consumerSecretReady := s.postgresqlServiceConsumerSecretReady(ctx, spec)
	blockers := probe.Blockers
	if blockers == nil {
		blockers = []string{}
	}
	result := map[string]any{
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
		"blockers":            blockers,
		"vmName":              spec.VMName,
		"namespace":           spec.Namespace,
		"clusterName":         spec.ClusterName,
		"serviceName":         spec.ClusterName + "-rw",
		"secretName":          spec.ConsumerSecretName,
		"cnpgSecretName":      spec.ClusterName + "-app",
		"credentialState":     "cnpg-owned",
	}
	// Status is also the idempotent fast path for consumers that need the
	// host-agent-owned PostgreSQL relay. Reconcile callers must not enqueue a
	// second long-running CNPG task when the cluster is already SQL-ready.
	if args.Relay != nil && result["ready"] == true {
		relay, relayErr := s.ensurePostgreSQLServiceRelay(ctx, spec, *args.Relay)
		if relayErr != nil {
			return nil, relayErr
		}
		result["localRelay"] = relay
	}
	return result, nil
}

func (s *Service) RemovePostgreSQLService(ctx context.Context, args PostgreSQLServiceArgs, confirm bool) (map[string]any, error) {
	if !confirm {
		return nil, errors.New("remove_postgresql_service requires confirm=true")
	}
	spec, err := validatePostgreSQLServiceSpec(args)
	if err != nil {
		return nil, err
	}
	if _, err := s.deps.RunKubectlContext(ctx, spec.VMName, []string{"delete", "cluster.postgresql.cnpg.io", spec.ClusterName, "-n", spec.Namespace, "--ignore-not-found=true", "--wait=true"}, "delete PostgreSQL service Cluster", 5*time.Minute); err != nil {
		return nil, err
	}
	if spec.RetentionPolicy == "delete" {
		_, _ = s.deps.RunKubectlContext(ctx, spec.VMName, []string{"delete", "secret", spec.ClusterName + "-app", "-n", spec.Namespace, "--ignore-not-found=true"}, "delete PostgreSQL service Secret", defaultDiscoveryTimeout)
		_, _ = s.deps.RunKubectlContext(ctx, spec.VMName, []string{"delete", "secret", spec.ConsumerSecretName, "-n", spec.Namespace, "--ignore-not-found=true"}, "delete PostgreSQL consumer Secret", defaultDiscoveryTimeout)
		_, _ = s.deps.RunKubectlContext(ctx, spec.VMName, []string{"delete", "pvc", "-l", "cnpg.io/cluster=" + spec.ClusterName, "-n", spec.Namespace, "--ignore-not-found=true"}, "delete PostgreSQL service PVCs", 5*time.Minute)
	}
	s.RevokeAllRelays()
	return map[string]any{
		"removed":           true,
		"vmName":            spec.VMName,
		"namespace":         spec.Namespace,
		"clusterName":       spec.ClusterName,
		"operatorPreserved": true,
	}, nil
}
