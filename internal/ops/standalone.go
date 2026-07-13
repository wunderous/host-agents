package ops

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var standaloneIdentifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type LocalPrerequisitesResult struct {
	Provider       string            `json:"provider"`
	ProviderBinary string            `json:"providerBinary"`
	ProviderReady  bool              `json:"providerReady"`
	Commands       map[string]bool   `json:"commands"`
	Checks         map[string]string `json:"checks,omitempty"`
}

func (s *HostOperationsService) CheckLocalPrerequisites() (*LocalPrerequisitesResult, error) {
	commands := map[string]bool{}
	for _, name := range []string{"incus", "bash", "curl", "base64"} {
		_, err := exec.LookPath(name)
		commands[name] = err == nil
	}
	providerReady := false
	checks := map[string]string{}
	res, err := s.commandRunner([]string{"version"}, nil, 10*time.Second)
	if err != nil {
		checks["incus"] = err.Error()
	} else if res.ExitCode == 0 {
		providerReady = true
		checks["incus"] = "ready"
	} else {
		checks["incus"] = firstNonEmpty(res.Stderr, res.Stdout, "incus check failed")
	}
	return &LocalPrerequisitesResult{
		Provider:       string(s.runtime.ReadProviderID()),
		ProviderBinary: s.runtime.ProviderBinary(),
		ProviderReady:  providerReady,
		Commands:       commands,
		Checks:         checks,
	}, nil
}

func (s *HostOperationsService) GetLocalStatus() (map[string]any, error) {
	vms, err := s.ListVMs(true)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"provider":       s.runtime.ReadProviderID(),
		"providerBinary": s.runtime.ProviderBinary(),
		"vmCount":        len(vms.VMs),
		"vms":            vms.VMs,
		"checkedAt":      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *HostOperationsService) GetK3sStatus(vmName string) (map[string]any, error) {
	nodesJSON, err := s.runKubernetesKubectlTimed(vmName, []string{"get", "nodes", "-o", "json"}, "get K3s nodes", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var nodes struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(nodesJSON), &nodes); err != nil {
		return nil, fmt.Errorf("parse K3s nodes: %w", err)
	}
	ready := 0
	items := make([]map[string]any, 0, len(nodes.Items))
	version := ""
	for _, node := range nodes.Items {
		nodeReady := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" {
				nodeReady = condition.Status == "True"
			}
		}
		if nodeReady {
			ready++
		}
		if version == "" {
			version = node.Status.NodeInfo.KubeletVersion
		}
		items = append(items, map[string]any{"name": node.Metadata.Name, "ready": nodeReady, "version": node.Status.NodeInfo.KubeletVersion})
	}
	return map[string]any{
		"vmName":         vmName,
		"status":         map[bool]string{true: "ready", false: "pending"}[len(items) > 0 && ready == len(items)],
		"nodes":          items,
		"readyNodes":     ready,
		"totalNodes":     len(items),
		"kubeletVersion": version,
	}, nil
}

type InstallPostgreSQLArgs struct {
	VMName    string `json:"vmName"`
	Namespace string `json:"namespace,omitempty"`
	Database  string `json:"database,omitempty"`
	Password  string `json:"password"`
}

func (s *HostOperationsService) InstallPostgreSQL(args InstallPostgreSQLArgs, onData func(string)) (map[string]any, error) {
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
	if strings.TrimSpace(args.Password) == "" {
		return nil, errors.New("password is required")
	}
	manifest := postgresManifest(namespace, database, args.Password)
	encoded := base64.StdEncoding.EncodeToString([]byte(manifest))
	apply := fmt.Sprintf("printf '%s' | base64 -d | k3s kubectl apply -f -", encoded)
	res, err := s.runVMExec(vmName, []string{"bash", "-lc", apply}, onData, 2*time.Minute)
	if err != nil || res.ExitCode != 0 {
		return nil, fmt.Errorf("apply PostgreSQL manifest: %s", firstNonEmpty(res.Stderr, res.Stdout, errorText(err)))
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

func (s *HostOperationsService) GetPostgreSQLStatus(vmName, namespace string) (map[string]any, error) {
	if strings.TrimSpace(namespace) == "" {
		namespace = "opute-local-db"
	}
	out, err := s.runKubernetesKubectlTimed(vmName, []string{"get", "deployment", "postgres", "-n", namespace, "-o", "json"}, "get PostgreSQL deployment", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var deployment struct {
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas     int `json:"readyReplicas"`
			AvailableReplicas int `json:"availableReplicas"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &deployment); err != nil {
		return nil, fmt.Errorf("parse PostgreSQL deployment: %w", err)
	}
	ready := deployment.Status.ReadyReplicas >= 1 && deployment.Status.AvailableReplicas >= 1
	return map[string]any{"vmName": vmName, "namespace": namespace, "deployment": "postgres", "ready": ready, "readyReplicas": deployment.Status.ReadyReplicas, "replicas": deployment.Spec.Replicas}, nil
}

func (s *HostOperationsService) DeletePostgreSQL(vmName, namespace string, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(namespace) == "" {
		namespace = "opute-local-db"
	}
	_, err := s.runKubernetesKubectlTimed(vmName, []string{"delete", "namespace", namespace, "--ignore-not-found=true"}, "delete PostgreSQL namespace", 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "namespace": namespace, "status": "deleted"}, nil
}

func (s *HostOperationsService) RunSQL(vmName, namespace, database, sql string) (map[string]any, error) {
	if strings.TrimSpace(vmName) == "" || strings.TrimSpace(sql) == "" {
		return nil, errors.New("vmName and sql are required")
	}
	if namespace == "" {
		namespace = "opute-local-db"
	}
	if database == "" {
		database = "app"
	}
	if len(sql) > 64*1024 {
		return nil, errors.New("sql exceeds 64 KiB limit")
	}
	out, err := s.runKubernetesKubectlTimed(vmName, []string{"exec", "-n", namespace, "deploy/postgres", "--", "psql", "-U", "postgres", "-d", database, "-v", "ON_ERROR_STOP=1", "-At", "-c", sql}, "run SQL", 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "namespace": namespace, "database": database, "output": out}, nil
}

func postgresManifest(namespace, database, password string) string {
	encodedPassword := base64.StdEncoding.EncodeToString([]byte(password))
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: Secret
metadata:
  name: postgres-secret
  namespace: %s
type: Opaque
data:
  POSTGRES_PASSWORD: %s
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-data
  namespace: %s
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 2Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_DB
              value: %s
            - name: POSTGRES_USER
              value: postgres
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: postgres-secret
                  key: POSTGRES_PASSWORD
          readinessProbe:
            exec:
              command: ["sh", "-c", "pg_isready -U postgres"]
            initialDelaySeconds: 5
            periodSeconds: 5
          volumeMounts:
            - name: data
              mountPath: /var/lib/postgresql/data
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: postgres-data
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
  namespace: %s
spec:
  selector:
    app: postgres
  ports:
    - port: 5432
      targetPort: 5432
`, namespace, namespace, encodedPassword, namespace, namespace, database, namespace)
}

func errorText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
