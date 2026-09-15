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

// ListCertificateIssuers answers with cert-manager's issuers: the cluster-scoped
// `ClusterIssuer` objects plus the namespaced `Issuer` ones.
//
// `clusterissuers` was already listed in clusterScopedK8sResources above, but
// nothing ever called it -- the Host Agent had no `list_certificate_issuers`
// handler at all, while the Platform's catalog advertised one. Asking for it
// answered "tool not found" against a cluster where cert-manager was installed
// and working.
//
// A namespace narrows the question to that namespace, so the cluster-scoped kind
// is skipped: a caller that asked about one namespace should not be handed
// objects that live outside it.
func (s *Service) ListCertificateIssuers(vmName, namespace string) ([]map[string]any, error) {
	out := make([]map[string]any, 0)
	if strings.TrimSpace(namespace) == "" {
		rows, err := s.listIssuerKind(vmName, "clusterissuers", "ClusterIssuer", "")
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	rows, err := s.listIssuerKind(vmName, "issuers", "Issuer", namespace)
	if err != nil {
		return nil, err
	}
	return append(out, rows...), nil
}

// listIssuerKind reads one issuer kind, treating an absent CRD as an empty list.
//
// cert-manager is optional. On a cluster without it `kubectl get clusterissuers`
// exits non-zero with "the server doesn't have a resource type", which is an
// answer -- there are no issuers, because the operator that defines them is not
// installed -- and not a failure of this call. Propagating it would put a shell
// error in front of an operator who asked a question the cluster answered
// perfectly well.
func (s *Service) listIssuerKind(vmName, resource, kind, namespace string) ([]map[string]any, error) {
	data, err := s.getKubernetesList(vmName, resource, namespace)
	if err != nil {
		if isMissingResourceType(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, raw := range data["items"].([]any) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		ns, _ := meta["namespace"].(string)
		row := map[string]any{
			"name":      name,
			"namespace": ns,
			"kind":      kind,
			"ready":     issuerReady(m),
			"age":       k8sAge(stringOrEmpty(meta["creationTimestamp"])),
		}
		if issuerType := issuerType(m); issuerType != "" {
			row["type"] = issuerType
		}
		out = append(out, row)
	}
	return out, nil
}

// issuerReady reads the `Ready` condition cert-manager writes on every issuer.
// Absent means not yet reconciled, which is reported as not ready rather than
// omitted: "ready" is the one thing a caller asks an issuer about.
func issuerReady(item map[string]any) bool {
	status, _ := item["status"].(map[string]any)
	conditions, _ := status["conditions"].([]any)
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringOrEmpty(condition["type"]) == "Ready" {
			return stringOrEmpty(condition["status"]) == "True"
		}
	}
	return false
}

// issuerType names the solver an issuer is configured with. cert-manager models
// this as exactly one populated key on `spec`, so the key is the answer.
func issuerType(item map[string]any) string {
	spec, _ := item["spec"].(map[string]any)
	for _, candidate := range []string{"acme", "ca", "selfSigned", "vault", "venafi"} {
		if _, ok := spec[candidate]; ok {
			return candidate
		}
	}
	return ""
}

// isMissingResourceType reports whether kubectl refused because the kind is not
// registered in the cluster, rather than because the call itself went wrong.
// Matched on the message because the error arrives here as provider-formatted
// text, having crossed the provider RPC boundary that erases typed kinds.
func isMissingResourceType(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "doesn't have a resource type") ||
		strings.Contains(text, "the server could not find the requested resource") ||
		strings.Contains(text, "could not find the requested resource")
}

func stringOrEmpty(value any) string {
	text, _ := value.(string)
	return text
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
