package oci

import (
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/resource"
)

func (s *Service) admitRegistryStorage(targetURI, namespace, name, storageClass, storageSize string) error {
	pvc, err := s.deps.GetK8sResource(targetURI, "pvc", name+"-data", namespace)
	if err != nil {
		if isKubernetesNotFound(err) {
			return nil
		}
		return err
	}
	object := k8sResourceObject(pvc)
	current := nestedMapString(object, "spec", "resources", "requests", "storage")
	return admitPVCSizeChange(current, storageSize, func() error {
		className := strings.TrimSpace(nestedMapString(object, "spec", "storageClassName"))
		if className == "" {
			className = storageClass
		}
		return s.assertStorageClassExpands(targetURI, className)
	})
}

func (s *Service) assertStorageClassExpands(targetURI, className string) error {
	className = strings.TrimSpace(className)
	if className == "" {
		return fmt.Errorf("storageSize cannot grow: StorageClass is unknown, so allowVolumeExpansion cannot be verified")
	}
	class, err := s.deps.GetK8sResource(targetURI, "storageclass", className, "")
	if err != nil {
		return fmt.Errorf("storageSize cannot grow: StorageClass %q could not be read: %w", className, err)
	}
	object := k8sResourceObject(class)
	if expands, ok := object["allowVolumeExpansion"].(bool); ok && expands {
		return nil
	}
	return fmt.Errorf("storageSize cannot grow: StorageClass %q does not allow volume expansion", className)
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

func k8sResourceObject(result map[string]any) map[string]any {
	if nested, ok := result["resource"].(map[string]any); ok {
		return nested
	}
	return result
}

func nestedMapString(value map[string]any, keys ...string) string {
	current := value
	for index, key := range keys {
		next, ok := current[key]
		if !ok {
			return ""
		}
		if index == len(keys)-1 {
			text, _ := next.(string)
			return text
		}
		current, _ = next.(map[string]any)
		if current == nil {
			return ""
		}
	}
	return ""
}

func isKubernetesNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "notfound") || strings.Contains(message, "not found")
}
