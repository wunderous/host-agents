package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TiDBServiceArgs is an explicit opt-in TiDB service lifecycle. The caller
// supplies the service identity; the host agent does not infer an owner.
type TiDBServiceArgs struct {
	VMName          string `json:"vmName,omitempty"`
	ClusterName     string `json:"clusterName,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	PDReplicas      int    `json:"pdReplicas,omitempty"`
	TiKVReplicas    int    `json:"tikvReplicas,omitempty"`
	TiDBReplicas    int    `json:"tidbReplicas,omitempty"`
	StorageClass    string `json:"storageClass,omitempty"`
	StorageSize     string `json:"storageSize,omitempty"`
	TiDBVersion     string `json:"tidbVersion,omitempty"`
	RetentionPolicy string `json:"retentionPolicy,omitempty"`
}

func validateTiDBServiceArgs(args TiDBServiceArgs) (TiDBServiceArgs, error) {
	args.VMName = strings.TrimSpace(args.VMName)
	args.ClusterName = strings.TrimSpace(args.ClusterName)
	args.Namespace = strings.TrimSpace(args.Namespace)
	if args.VMName == "" {
		return args, errors.New("vmName is required")
	}
	if args.ClusterName == "" {
		return args, errors.New("clusterName is required")
	}
	if args.Namespace == "" {
		return args, errors.New("namespace is required")
	}
	if args.PDReplicas == 0 {
		args.PDReplicas = 1
	}
	if args.TiKVReplicas == 0 {
		args.TiKVReplicas = 1
	}
	if args.TiDBReplicas == 0 {
		args.TiDBReplicas = 1
	}
	if args.StorageClass == "" {
		args.StorageClass = "local-path"
	}
	if args.StorageSize == "" {
		args.StorageSize = "10Gi"
	}
	if args.TiDBVersion == "" {
		args.TiDBVersion = "v8.5.0"
	}
	if args.RetentionPolicy == "" {
		args.RetentionPolicy = "delete"
	}
	if args.RetentionPolicy != "delete" && args.RetentionPolicy != "retain" {
		return args, errors.New("retentionPolicy must be delete or retain")
	}
	if !isSafeKubernetesName(args.ClusterName) || !isSafeKubernetesName(args.Namespace) || !isSafeKubernetesName(args.StorageClass) {
		return args, errors.New("invalid TiDB Kubernetes name")
	}
	if !isValidStorageSize(args.StorageSize) {
		return args, errors.New("storageSize must be a Kubernetes quantity such as 10Gi")
	}
	return args, nil
}

func renderTiDBServiceManifest(args TiDBServiceArgs) string {
	return fmt.Sprintf(`apiVersion: helm.cattle.io/v1
kind: HelmChart
metadata:
  name: tidb-operator
  namespace: kube-system
spec:
  repo: https://charts.pingcap.com
  chart: tidb-operator
  targetNamespace: tidb-admin
  createNamespace: true
---
apiVersion: pingcap.com/v1alpha1
kind: TidbCluster
metadata:
  name: %s
  namespace: %s
  labels:
    host-agent.io/managed-tidb: "true"
spec:
  version: %s
  timezone: UTC
  pd:
    replicas: %d
    requests: { cpu: 100m, memory: 256Mi }
    config: {}
    storageClaims:
      - resources: { requests: { storage: %s } }
        storageClassName: %s
  tikv:
    replicas: %d
    requests: { cpu: 100m, memory: 512Mi }
    config: {}
    storageClaims:
      - resources: { requests: { storage: %s } }
        storageClassName: %s
  tidb:
    replicas: %d
    service:
      type: ClusterIP
`, args.ClusterName, args.Namespace, args.TiDBVersion, args.PDReplicas, args.StorageSize, args.StorageClass, args.TiKVReplicas, args.StorageSize, args.StorageClass, args.TiDBReplicas)
}

func (s *HostOperationsService) ReconcileTiDBService(ctx context.Context, raw TiDBServiceArgs, _ func(string)) (map[string]any, error) {
	args, err := validateTiDBServiceArgs(raw)
	if err != nil {
		return nil, err
	}
	if _, err = s.runKubernetesKubectlWithStdinContext(ctx, args.VMName, []string{"apply", "-f", "-"}, []byte(renderTiDBServiceManifest(args)), "apply opt-in TiDB service", 3*time.Minute); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "clusterName": args.ClusterName, "namespace": args.Namespace, "ready": true, "sqlReady": false, "taskLedgerSqlReady": false, "optIn": true}, nil
}

func (s *HostOperationsService) GetTiDBServiceStatus(ctx context.Context, raw TiDBServiceArgs) (map[string]any, error) {
	args, err := validateTiDBServiceArgs(raw)
	if err != nil {
		return nil, err
	}
	_, err = s.runKubernetesKubectlContext(ctx, args.VMName, []string{"get", "tidbcluster", args.ClusterName, "-n", args.Namespace}, "read opt-in TiDB service status", defaultDiscoveryTimeout)
	return map[string]any{"vmName": args.VMName, "clusterName": args.ClusterName, "namespace": args.Namespace, "ready": err == nil, "sqlReady": false, "taskLedgerSqlReady": false, "optIn": true}, nil
}

func (s *HostOperationsService) RemoveTiDBService(ctx context.Context, raw TiDBServiceArgs, confirm bool) (map[string]any, error) {
	if !confirm {
		return nil, errors.New("remove_tidb_service requires confirm=true")
	}
	args, err := validateTiDBServiceArgs(raw)
	if err != nil {
		return nil, err
	}
	if _, err = s.runKubernetesKubectlContext(ctx, args.VMName, []string{"delete", "tidbcluster", args.ClusterName, "-n", args.Namespace, "--ignore-not-found=true"}, "remove opt-in TiDB service", 3*time.Minute); err != nil {
		return nil, err
	}
	return map[string]any{"removed": true, "clusterName": args.ClusterName, "namespace": args.Namespace, "optIn": true}, nil
}
