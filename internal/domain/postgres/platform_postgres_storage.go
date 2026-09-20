package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/resource"
)

func (s *Service) admitPostgreSQLStorage(ctx context.Context, spec postgresqlServiceSpec) error {
	cluster, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "cluster.postgresql.cnpg.io", spec.ClusterName, "-n", spec.Namespace}, "get PostgreSQL service Cluster storage")
	if err != nil {
		if isKubernetesNotFound(err) {
			return nil
		}
		return fmt.Errorf("storageSize cannot be admitted: %w", err)
	}
	current := nestedString(cluster, "spec", "storage", "size")
	return admitPVCSizeChange(current, spec.StorageSize, func() error {
		className := strings.TrimSpace(nestedString(cluster, "spec", "storage", "storageClass"))
		if className == "" {
			className = spec.StorageClass
		}
		return s.assertStorageClassExpands(ctx, spec, className)
	})
}

func (s *Service) assertStorageClassExpands(ctx context.Context, spec postgresqlServiceSpec, className string) error {
	className = strings.TrimSpace(className)
	if className == "" {
		return fmt.Errorf("storageSize cannot grow: StorageClass is unknown, so allowVolumeExpansion cannot be verified")
	}
	class, err := s.postgresqlServiceJSON(ctx, spec, []string{"get", "storageclass", className}, "get PostgreSQL StorageClass")
	if err != nil {
		return fmt.Errorf("storageSize cannot grow: StorageClass %q could not be read: %w", className, err)
	}
	if expands, ok := class["allowVolumeExpansion"].(bool); ok && expands {
		return nil
	}
	return fmt.Errorf("storageSize cannot grow: StorageClass %q does not allow volume expansion", className)
}

func isKubernetesNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "notfound") || strings.Contains(message, "not found")
}

func admitPVCSizeChange(current, requested string, assertExpand func() error) error {
	current = strings.TrimSpace(current)
	requested = strings.TrimSpace(requested)
	if requested == "" || current == "" {
		return nil
	}
	want, err := resource.ParseByteCapacity(requested)
	if err != nil {
		return fmt.Errorf("storageSize %q is invalid: %w", requested, err)
	}
	have, err := resource.ParseByteCapacity(current)
	if err != nil {
		return fmt.Errorf("observed storage size %q is invalid: %w", current, err)
	}
	if want < have {
		return fmt.Errorf("storage shrink from %s to %s is refused; persistent volumes are grow-only", current, requested)
	}
	if want == have {
		return nil
	}
	return assertExpand()
}
