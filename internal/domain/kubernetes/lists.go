package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/textutil"
)

var clusterScopedK8sResources = map[string]bool{
	"namespaces": true, "ingressclasses": true, "storageclasses": true, "clusterissuers": true,
}

type k8sMeta struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	UID               string `json:"uid"`
	CreationTimestamp string `json:"creationTimestamp"`
}

type k8sPodItem struct {
	Metadata k8sMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		PodIP             string `json:"podIP"`
		ContainerStatuses []struct {
			Ready        bool `json:"ready"`
			RestartCount int  `json:"restartCount"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type k8sDeploymentItem struct {
	Metadata k8sMeta `json:"metadata"`
	Spec     struct {
		Replicas int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas       int `json:"readyReplicas"`
		AvailableReplicas   int `json:"availableReplicas"`
		UnavailableReplicas int `json:"unavailableReplicas"`
	} `json:"status"`
}

func (s *Service) ListNamespaces(vmName string) ([]string, error) {
	data, err := s.getKubernetesList(vmName, "namespaces", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		out = append(out, meta["name"].(string))
	}
	return out, nil
}

func (s *Service) ListStorageClasses(vmName string) ([]string, error) {
	data, err := s.getKubernetesList(vmName, "storageclasses", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		out = append(out, meta["name"].(string))
	}
	return out, nil
}

func (s *Service) ListIngressClasses(vmName string) ([]string, error) {
	data, err := s.getKubernetesList(vmName, "ingressclasses", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		out = append(out, meta["name"].(string))
	}
	return out, nil
}

func (s *Service) ListServices(vmName, namespace string) ([]map[string]any, error) {
	data, err := s.getKubernetesList(vmName, "services", namespace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, item := range data["items"].([]any) {
		m := item.(map[string]any)
		meta := m["metadata"].(map[string]any)
		row := map[string]any{
			"name":      meta["name"].(string),
			"namespace": meta["namespace"].(string),
		}
		if uri, err := resourceid.ServiceURI(s.shared.TenantID, vmName+"/"+meta["namespace"].(string)+"/"+meta["name"].(string)); err == nil {
			row["uri"] = uri.String()
			if s.shared.ResourceRegistry != nil {
				_ = s.shared.RegisterResource(uri.String(), map[string]any{
					"providerInstanceName": vmName,
					"namespace":            meta["namespace"].(string),
					"serviceName":          meta["name"].(string),
				})
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) ListPods(vmName, namespace string) ([]map[string]any, error) {
	data, err := s.getKubernetesList(vmName, "pods", namespace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, raw := range data["items"].([]any) {
		var item k8sPodItem
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &item)
		if strings.TrimSpace(item.Metadata.UID) == "" {
			return nil, fmt.Errorf("the Kubernetes pod %q/%q has no metadata.uid; refusing to issue an unstable pod URI", item.Metadata.Namespace, item.Metadata.Name)
		}
		ready := true
		restarts := 0
		for _, cs := range item.Status.ContainerStatuses {
			if !cs.Ready {
				ready = false
			}
			restarts += cs.RestartCount
		}
		row := map[string]any{
			"name":      item.Metadata.Name,
			"namespace": item.Metadata.Namespace,
			"kind":      resourceid.TypePod,
			"status":    textutil.Default(item.Status.Phase, "Unknown"),
			"ready":     ready,
			"restarts":  restarts,
			"age":       k8sAge(item.Metadata.CreationTimestamp),
		}
		if item.Status.PodIP != "" {
			row["ip"] = item.Status.PodIP
		}
		if item.Spec.NodeName != "" {
			row["node"] = item.Spec.NodeName
		}
		if item.Metadata.UID != "" {
			resourceID := vmName + "/" + item.Metadata.Namespace + "/" + item.Metadata.Name + "/" + item.Metadata.UID
			if uri, uriErr := resourceid.PodURI(s.shared.TenantID, resourceID); uriErr == nil {
				row["uri"] = uri.String()
				if s.shared.ResourceRegistry != nil {
					_ = s.shared.RegisterResource(uri.String(), map[string]any{
						"providerInstanceName": vmName,
						"namespace":            item.Metadata.Namespace,
						"podName":              item.Metadata.Name,
						"uid":                  item.Metadata.UID,
						"clusterResource":      vmName,
					})
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) ListDeployments(vmName, namespace string) ([]map[string]any, error) {
	data, err := s.getKubernetesList(vmName, "deployments", namespace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, raw := range data["items"].([]any) {
		var item k8sDeploymentItem
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &item)
		ready := item.Status.ReadyReplicas
		desired := item.Spec.Replicas
		status := "pending"
		if ready >= desired && ready > 0 {
			status = "ready"
		}
		out = append(out, map[string]any{
			"name":        item.Metadata.Name,
			"namespace":   item.Metadata.Namespace,
			"ready":       ready,
			"desired":     desired,
			"available":   item.Status.AvailableReplicas,
			"unavailable": item.Status.UnavailableReplicas,
			"age":         k8sAge(item.Metadata.CreationTimestamp),
			"status":      status,
		})
	}
	return out, nil
}

func (s *Service) getKubernetesList(vmName, resource, namespace string) (map[string]any, error) {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	nsArgs := []string{"--all-namespaces"}
	if namespace != "" {
		nsArgs = []string{"-n", namespace}
	} else if clusterScopedK8sResources[resource] {
		nsArgs = nil
	}
	stdout, err := s.RunKubectl(vmName, append([]string{"get", resource}, append(nsArgs, "-o", "json")...), "list "+resource)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(stdout, "{") {
		return nil, fmt.Errorf("expected JSON output while listing %s", resource)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		return nil, err
	}
	items, ok := parsed["items"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid Kubernetes %s response: missing items array", resource)
	}
	return map[string]any{"items": items}, nil
}

// --- Host services / prerequisites ---

// --- Bridge diagnostics ---

// --- helpers ---

func k8sAge(creationTimestamp string) string {
	if creationTimestamp == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, creationTimestamp)
	if err != nil {
		return "unknown"
	}
	elapsed := time.Since(t)
	minutes := int(elapsed.Minutes())
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}
