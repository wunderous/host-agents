package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	"github.com/wunderous/host-agents/internal/contract/clusterinfo"
	"github.com/wunderous/host-agents/internal/resourceid"
)

// KubernetesCapabilityID is the neutral provider capability implemented by a
// Kubernetes provider. Host Agent owns admission and lifecycle; the provider
// owns the concrete control-plane client and target execution.
const KubernetesCapabilityID = capabilitycontract.Kubernetes

const (
	KubernetesValidateOperation          = capabilitycontract.KubernetesValidateOperation
	KubernetesProvisionOperation         = capabilitycontract.KubernetesProvisionOperation
	KubernetesStatusOperation            = capabilitycontract.KubernetesStatusOperation
	KubernetesConfigureRegistryOperation = capabilitycontract.KubernetesConfigureRegistryOperation
	KubernetesRemoveOperation            = capabilitycontract.KubernetesRemoveOperation
	KubernetesRestartOperation           = capabilitycontract.KubernetesRestartOperation
	KubernetesApplyManifestOperation     = capabilitycontract.KubernetesApplyManifestOperation
	KubernetesPutSecretOperation         = capabilitycontract.KubernetesPutSecretOperation
	KubernetesGetResourceOperation       = capabilitycontract.KubernetesGetResourceOperation
	KubernetesDeleteResourceOperation    = capabilitycontract.KubernetesDeleteResourceOperation
	KubernetesGetResourceStatusOperation = capabilitycontract.KubernetesGetResourceStatusOperation
	KubernetesListEventsOperation        = capabilitycontract.KubernetesListEventsOperation
	KubernetesListClustersOperation      = capabilitycontract.KubernetesListClustersOperation
	KubernetesGetClusterInfoOperation    = capabilitycontract.KubernetesGetClusterInfoOperation
	KubernetesExecCommandOperation       = capabilitycontract.KubernetesExecCommandOperation
)

// KubernetesProviderRequest is the neutral execution envelope passed from
// the admitted Host Agent operation to the active Kubernetes provider. Raw
// client arguments remain in Arguments; target coordinates are supplied only
// after tenant-checked URI resolution.
type KubernetesProviderRequest struct {
	TargetURI            string
	ProviderInstanceName string
	InstanceType         string
	Arguments            map[string]any
}

// KubernetesProviderExecutor is intentionally small and transport-neutral.
// The hostmcp package adapts provider MCP to this interface without exposing
// MCP sessions or Host Agent internals to the provider implementation.
type KubernetesProviderExecutor interface {
	Execute(context.Context, string, KubernetesProviderRequest) (map[string]any, error)
}

func (s *Service) SetKubernetesProviderExecutor(executor KubernetesProviderExecutor) {
	if s != nil {
		s.executor = executor
	}
}

func (s *Service) KubernetesProviderExecutor() KubernetesProviderExecutor {
	if s == nil {
		return nil
	}
	return s.executor
}

func (s *Service) TargetURI(providerInstanceName string) (string, error) {
	providerInstanceName = strings.TrimSpace(providerInstanceName)
	if providerInstanceName == "" {
		return "", fmt.Errorf("Kubernetes provider instance name is required")
	}
	uri, err := resourceid.ClusterURI(s.shared.EffectiveTenantID(), providerInstanceName)
	if err != nil {
		return "", err
	}
	coordinates, err := s.deps.ResolveResource(uri.String(), "cluster")
	if err != nil {
		return "", fmt.Errorf("resolve Kubernetes target %s: %w", uri, err)
	}
	return coordinates.URI.String(), nil
}

func kubernetesProviderRequest(s *Service, targetURI string, arguments map[string]any) (KubernetesProviderRequest, error) {
	targetURI = strings.TrimSpace(targetURI)
	if targetURI == "" {
		return KubernetesProviderRequest{}, fmt.Errorf("canonical cluster URI is required")
	}
	coordinates, err := s.deps.ResolveResource(targetURI, "cluster")
	if err != nil {
		return KubernetesProviderRequest{}, err
	}
	providerName, _ := coordinates.Values["providerInstanceName"].(string)
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return KubernetesProviderRequest{}, fmt.Errorf("cluster %s has no provider instance coordinate", coordinates.URI)
	}
	instanceType, _ := coordinates.Values["instanceType"].(string)
	return KubernetesProviderRequest{
		TargetURI:            coordinates.URI.String(),
		ProviderInstanceName: providerName,
		InstanceType:         strings.TrimSpace(instanceType),
		Arguments:            arguments,
	}, nil
}

func (s *Service) ExecuteProvider(operation, targetURI string, arguments map[string]any) (map[string]any, bool, error) {
	executor := s.KubernetesProviderExecutor()
	if executor == nil {
		return nil, false, nil
	}
	request, err := kubernetesProviderRequest(s, targetURI, arguments)
	if err != nil {
		return nil, true, err
	}
	out, err := executor.Execute(context.Background(), operation, request)
	if err != nil {
		return nil, true, err
	}
	return out, true, nil
}

func (s *Service) executeUnboundProvider(operation string, arguments map[string]any) (map[string]any, bool, error) {
	executor := s.KubernetesProviderExecutor()
	if executor == nil {
		return nil, false, nil
	}
	out, err := executor.Execute(context.Background(), operation, KubernetesProviderRequest{Arguments: arguments})
	if err != nil {
		return nil, true, err
	}
	return out, true, nil
}

// ListKubernetesClusters is the neutral inventory operation. The active
// Kubernetes provider supplies the concrete discovery; there is deliberately
// no provider-less execution fallback.
func (s *Service) ListKubernetesClusters(source string) (clusterinfo.ClusterListResult, error) {
	if out, delegated, err := s.executeUnboundProvider(KubernetesListClustersOperation, map[string]any{"source": strings.TrimSpace(source)}); delegated {
		if err != nil {
			return clusterinfo.ClusterListResult{}, err
		}
		encoded, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return clusterinfo.ClusterListResult{}, marshalErr
		}
		var result clusterinfo.ClusterListResult
		if unmarshalErr := json.Unmarshal(encoded, &result); unmarshalErr != nil {
			return clusterinfo.ClusterListResult{}, unmarshalErr
		}
		if result.Total == 0 {
			result.Total = len(result.Clusters)
		}
		for index := range result.Clusters {
			cluster := &result.Clusters[index]
			if strings.TrimSpace(cluster.Name) == "" {
				continue
			}
			uri, uriErr := resourceid.ClusterURI(s.shared.EffectiveTenantID(), cluster.Name)
			if uriErr != nil {
				return clusterinfo.ClusterListResult{}, uriErr
			}
			cluster.URI = uri.String()
			cluster.ID = cluster.URI
			if cluster.VMName == "" {
				cluster.VMName = cluster.Name
			}
			if cluster.InstanceType != "vm" && cluster.InstanceType != "container" {
				return clusterinfo.ClusterListResult{}, fmt.Errorf("the Kubernetes provider returned unsupported instance type for cluster %q", cluster.Name)
			}
			if s.shared.ResourceRegistry != nil {
				if registerErr := s.shared.RegisterResource(cluster.URI, map[string]any{
					"providerInstanceName": cluster.Name,
					"displayName":          cluster.Name,
					"instanceType":         cluster.InstanceType,
					"vmName":               cluster.Name,
				}); registerErr != nil {
					return clusterinfo.ClusterListResult{}, registerErr
				}
			}
		}
		return result, nil
	}
	return clusterinfo.ClusterListResult{}, fmt.Errorf("the Kubernetes provider is required for Kubernetes cluster discovery")
}
