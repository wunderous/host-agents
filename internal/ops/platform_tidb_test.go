package ops

import (
	"strings"
	"testing"
)

func TestValidateTiDBServiceArgsRequiresExplicitTarget(t *testing.T) {
	args, err := validateTiDBServiceArgs(TiDBServiceArgs{VMName: "host-a", ClusterName: "test-tidb", Namespace: "test-system"})
	if err != nil {
		t.Fatalf("validate defaults: %v", err)
	}
	if args.ClusterName != "test-tidb" || args.Namespace != "test-system" || args.PDReplicas != 1 || args.TiKVReplicas != 1 || args.TiDBReplicas != 1 {
		t.Fatalf("unexpected defaults: %+v", args)
	}
	if _, err := validateTiDBServiceArgs(TiDBServiceArgs{VMName: "host-a", ClusterName: "bad/name", Namespace: "test-system"}); err == nil {
		t.Fatal("unsafe cluster name was accepted")
	}
}

func TestRenderTiDBServiceManifestIsOperatorOwnedAndPersistent(t *testing.T) {
	args, err := validateTiDBServiceArgs(TiDBServiceArgs{VMName: "host-a", ClusterName: "test-tidb", Namespace: "test-system", StorageSize: "10Gi"})
	if err != nil {
		t.Fatalf("validate args: %v", err)
	}
	manifest := renderTiDBServiceManifest(args)
	for _, required := range []string{"kind: HelmChart", "chart: tidb-operator", "kind: TidbCluster", "storageClaims:", "storageClassName: local-path", "host-agent.io/managed-tidb"} {
		if !strings.Contains(manifest, required) {
			t.Errorf("manifest missing %q", required)
		}
	}
}
