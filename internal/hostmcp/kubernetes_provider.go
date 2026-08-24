package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
	"github.com/wunderous/host-agents/internal/ops"
)

// kubernetesProviderExecutor is the only bridge from public neutral
// Kubernetes primitives to a concrete Kubernetes provider. Provider
// selection is capability-based; the Host Agent never names K3s in this
// dispatch path.
type kubernetesProviderExecutor struct {
	server *Server
}

func (e *kubernetesProviderExecutor) Execute(ctx context.Context, operation string, request ops.KubernetesProviderRequest) (map[string]any, error) {
	if e == nil || e.server == nil {
		return nil, fmt.Errorf("Kubernetes provider executor is unavailable")
	}
	providerID, adapter, err := e.activeProvider()
	if err != nil {
		return nil, err
	}
	arguments := cloneArguments(request.Arguments)
	if request.TargetURI != "" {
		arguments["targetUri"] = request.TargetURI
		arguments["providerInstanceName"] = request.ProviderInstanceName
		if request.InstanceType != "" {
			arguments["instanceType"] = request.InstanceType
		}
	}
	result, err := adapter.Call(ctx, operation, arguments)
	if err != nil {
		return nil, fmt.Errorf("Kubernetes provider %q operation %q: %w", providerID, operation, err)
	}
	if result == nil {
		return nil, fmt.Errorf("Kubernetes provider %q returned no result", providerID)
	}
	if result.IsError {
		return nil, fmt.Errorf("Kubernetes provider %q operation %q failed", providerID, operation)
	}
	if result.StructuredContent == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, fmt.Errorf("encode Kubernetes provider result: %w", err)
	}
	var output map[string]any
	if err := json.Unmarshal(encoded, &output); err != nil {
		return nil, fmt.Errorf("decode Kubernetes provider result: %w", err)
	}
	return output, nil
}

func (e *kubernetesProviderExecutor) activeProvider() (string, *provideradapter.Adapter, error) {
	e.server.providerMu.RLock()
	defer e.server.providerMu.RUnlock()
	var candidates []struct {
		id      string
		adapter *provideradapter.Adapter
	}
	for providerID, manifest := range e.server.providerManifests {
		if !providesCapability(manifest.Provides, ops.KubernetesCapabilityID) {
			continue
		}
		if _, ok := e.server.providerLifecycle.Active(providerID); !ok {
			continue
		}
		adapter := e.server.providerAdapters[providerID]
		if adapter != nil {
			candidates = append(candidates, struct {
				id      string
				adapter *provideradapter.Adapter
			}{providerID, adapter})
		}
	}
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("no active Kubernetes provider is installed")
	}
	if len(candidates) > 1 {
		return "", nil, fmt.Errorf("multiple active Kubernetes providers are available; typed provider provenance is required")
	}
	return candidates[0].id, candidates[0].adapter, nil
}

func providesCapability(refs []providercontract.CapabilityRef, capabilityID string) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == capabilityID {
			return true
		}
	}
	return false
}
