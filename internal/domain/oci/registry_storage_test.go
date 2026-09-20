package oci

import (
	"errors"
	"strings"
	"testing"
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

func TestAdmitRegistryStorageGrowsOnlyWithExpansion(t *testing.T) {
	service := New(nil, Deps{
		GetK8sResource: func(_, kind, name, _ string) (map[string]any, error) {
			switch kind {
			case "pvc":
				return map[string]any{"spec": map[string]any{
					"storageClassName": "fast",
					"resources":        map[string]any{"requests": map[string]any{"storage": "10Gi"}},
				}}, nil
			case "storageclass":
				if name != "fast" {
					t.Fatalf("unexpected storageclass %q", name)
				}
				return map[string]any{"allowVolumeExpansion": true}, nil
			default:
				return nil, errors.New("unexpected kind " + kind)
			}
		},
	}, "")
	if err := service.admitRegistryStorage("cluster:local:x", "registry-system", "local-registry", "local-path", "20Gi"); err != nil {
		t.Fatalf("grow: %v", err)
	}
}

func TestAdmitRegistryStorageFailsClosedWithoutExpansion(t *testing.T) {
	service := New(nil, Deps{
		GetK8sResource: func(_, kind, _, _ string) (map[string]any, error) {
			if kind == "pvc" {
				return map[string]any{"resource": map[string]any{"spec": map[string]any{
					"storageClassName": "local-path",
					"resources":        map[string]any{"requests": map[string]any{"storage": "10Gi"}},
				}}}, nil
			}
			return map[string]any{"allowVolumeExpansion": false}, nil
		},
	}, "")
	if err := service.admitRegistryStorage("cluster:local:x", "registry-system", "local-registry", "local-path", "20Gi"); err == nil || !strings.Contains(err.Error(), "allow volume expansion") {
		t.Fatalf("expected expansion refusal, got %v", err)
	}
}

func TestAdmitRegistryStorageTreatsMissingPVCAsCreate(t *testing.T) {
	service := New(nil, Deps{
		GetK8sResource: func(_, _, _, _ string) (map[string]any, error) {
			return nil, errors.New(`persistentvolumeclaims "local-registry-data" not found`)
		},
	}, "")
	if err := service.admitRegistryStorage("cluster:local:x", "registry-system", "local-registry", "local-path", "20Gi"); err != nil {
		t.Fatalf("missing PVC must be a create, got %v", err)
	}
}
