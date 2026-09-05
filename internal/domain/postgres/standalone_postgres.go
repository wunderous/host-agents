package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/textutil"
)

type InstallPostgreSQLArgs struct {
	VMName    string `json:"vmName"`
	Namespace string `json:"namespace,omitempty"`
	Database  string `json:"database,omitempty"`
}

func (s *Service) InstallPostgreSQL(args InstallPostgreSQLArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	namespace := strings.TrimSpace(args.Namespace)
	if namespace == "" {
		namespace = "opute-local-db"
	}
	database := strings.TrimSpace(args.Database)
	if database == "" {
		database = "app"
	}
	if !standaloneIdentifier.MatchString(namespace) || !standaloneIdentifier.MatchString(database) {
		return nil, errors.New("namespace and database must be lowercase DNS-safe identifiers")
	}
	ctx := context.Background()
	if _, err := s.deps.RunKubectlWithStdinContext(ctx, vmName, []string{"apply", "-f", "-"}, []byte(standalonePostgresOperatorManifest(namespace)), "apply CloudNativePG operator", 3*time.Minute); err != nil {
		return nil, err
	}
	if err := s.waitForStandalonePostgresCRD(ctx, vmName); err != nil {
		return nil, err
	}
	if _, err := s.deps.RunKubectlWithStdinContext(ctx, vmName, []string{"apply", "-f", "-"}, []byte(standalonePostgresClusterManifest(namespace, database)), "apply CloudNativePG PostgreSQL Cluster", 3*time.Minute); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		status, statusErr := s.GetPostgreSQLStatus(vmName, namespace)
		if statusErr == nil {
			if ready, _ := status["ready"].(bool); ready {
				return status, nil
			}
		}
		time.Sleep(5 * time.Second)
	}
	return nil, fmt.Errorf("PostgreSQL deployment did not become ready in %s", namespace)
}

func (s *Service) GetPostgreSQLStatus(vmName, namespace string) (map[string]any, error) {
	if strings.TrimSpace(namespace) == "" {
		namespace = "opute-local-db"
	}
	out, err := s.deps.RunKubectlTimed(vmName, []string{"get", "cluster.postgresql.cnpg.io", "postgres", "-n", namespace, "-o", "json"}, "get CloudNativePG PostgreSQL Cluster", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var cluster struct {
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			Phase          string `json:"phase"`
			ReadyInstances int    `json:"readyInstances"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &cluster); err != nil {
		return nil, fmt.Errorf("parse CloudNativePG PostgreSQL Cluster: %w", err)
	}
	instances := cluster.Spec.Replicas
	if instances == 0 {
		instances = 1
	}
	ready := strings.Contains(strings.ToLower(cluster.Status.Phase), "healthy") && cluster.Status.ReadyInstances >= instances
	return map[string]any{"vmName": vmName, "namespace": namespace, "cluster": "postgres", "phase": cluster.Status.Phase, "ready": ready, "readyInstances": cluster.Status.ReadyInstances, "instances": instances}, nil
}

func (s *Service) DeletePostgreSQL(vmName, namespace string, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(namespace) == "" {
		namespace = "opute-local-db"
	}
	_, err := s.deps.RunKubectlTimed(vmName, []string{"delete", "namespace", namespace, "--ignore-not-found=true"}, "delete PostgreSQL namespace", 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "namespace": namespace, "status": "deleted"}, nil
}

func (s *Service) RunSQL(vmName, namespace, database, sql string) (map[string]any, error) {
	if strings.TrimSpace(vmName) == "" || strings.TrimSpace(sql) == "" {
		return nil, errors.New("vmName and sql are required")
	}
	if namespace == "" {
		namespace = "opute-local-db"
	}
	if database == "" {
		database = "app"
	}
	namespace = strings.TrimSpace(namespace)
	database = strings.TrimSpace(database)
	if !standaloneIdentifier.MatchString(namespace) || !standaloneIdentifier.MatchString(database) {
		return nil, errors.New("namespace and database must be lowercase DNS-safe identifiers")
	}
	if len(sql) > 64*1024 {
		return nil, errors.New("sql exceeds 64 KiB limit")
	}
	pod, credentials, err := s.standalonePostgresExecutionTarget(vmName, namespace)
	if err != nil {
		return nil, err
	}
	script := standalonePostgresSQLScript(credentials.Username, database, sql)
	input := []byte(fmt.Sprintf("*:*:*:%s:%s\n", credentials.Username, credentials.Password))
	out, err := s.deps.RunKubectlWithStdinContext(
		context.Background(),
		vmName,
		[]string{"exec", "-i", pod, "-n", namespace, "--", "sh", "-ceu", script},
		input,
		"run SQL through CloudNativePG primary",
		2*time.Minute,
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "namespace": namespace, "database": database, "output": out}, nil
}

func standalonePostgresManifest(namespace, database string) string {
	return standalonePostgresOperatorManifest(namespace) + "---\n" + standalonePostgresClusterManifest(namespace, database)
}

func standalonePostgresOperatorManifest(namespace string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
%s---
`, namespace, renderPostgreSQLServiceOperatorManifest())
}

func standalonePostgresClusterManifest(namespace, database string) string {
	return fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: postgres
  namespace: %s
spec:
  instances: 1
  imageName: %s
  storage:
    size: 2Gi
  bootstrap:
    initdb:
      database: %s
      owner: app
      encoding: UTF8
      localeCType: C
      localeCollate: C
`, namespace, postgresqlOperandImageName, database)
}

func (s *Service) waitForStandalonePostgresCRD(ctx context.Context, vmName string) error {
	deadline := time.NewTimer(3 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.deps.RunKubectlContext(ctx, vmName, []string{"get", "crd", "clusters.postgresql.cnpg.io"}, "wait for CloudNativePG Cluster CRD", 30*time.Second); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("CloudNativePG Cluster CRD did not become available")
		case <-ticker.C:
		}
	}
}

func (s *Service) standalonePostgresExecutionTarget(vmName, namespace string) (string, postgresqlServiceSecret, error) {
	podsJSON, err := s.deps.RunKubectlTimed(vmName, []string{"get", "pods", "-n", namespace, "-l", "cnpg.io/cluster=postgres,role=primary", "-o", "json"}, "find CloudNativePG primary", 30*time.Second)
	if err != nil {
		return "", postgresqlServiceSecret{}, err
	}
	var pods struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(podsJSON), &pods); err != nil {
		return "", postgresqlServiceSecret{}, fmt.Errorf("parse CloudNativePG primary pods: %w", err)
	}
	pod := ""
	for _, item := range pods.Items {
		if item.Metadata.Name != "" && item.Status.Phase == "Running" {
			pod = item.Metadata.Name
			break
		}
	}
	if pod == "" {
		return "", postgresqlServiceSecret{}, errors.New("CloudNativePG primary pod is not running")
	}
	secretJSON, err := s.deps.RunKubectlTimed(vmName, []string{"get", "secret", "postgres-app", "-n", namespace, "-o", "json"}, "read CloudNativePG application Secret", 30*time.Second)
	if err != nil {
		return "", postgresqlServiceSecret{}, err
	}
	var secret map[string]any
	if err := json.Unmarshal([]byte(secretJSON), &secret); err != nil {
		return "", postgresqlServiceSecret{}, fmt.Errorf("parse CloudNativePG application Secret: %w", err)
	}
	data, _ := secret["data"].(map[string]any)
	username := decodeSecretValue(data, "username")
	password := decodeSecretValue(data, "password")
	if username == "" || password == "" {
		return "", postgresqlServiceSecret{}, errors.New("CloudNativePG application Secret is incomplete")
	}
	return pod, postgresqlServiceSecret{Username: username, Password: password}, nil
}

func standalonePostgresSQLScript(username, database, sql string) string {
	return fmt.Sprintf(`set -eu
pgpass="$(mktemp)"
trap 'rm -f "$pgpass"' EXIT
cat >"$pgpass"
chmod 600 "$pgpass"
PGPASSFILE="$pgpass" psql -h 127.0.0.1 -p 5432 -U %s -d %s -v ON_ERROR_STOP=1 -Atqc %s
`, textutil.ShellQuote(username), textutil.ShellQuote(database), textutil.ShellQuote(sql))
}
