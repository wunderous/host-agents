package hostmcp

import (
	"context"

	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

type resourceServicesPlugin struct {
	registry hostagent.ResourceRegistry
	resolver *hostagent.Service
	tenantID string
	service  resource.HostResourceService
}

func (resourceServicesPlugin) ID() string                  { return "host-agent.resource-boundary" }
func (resourceServicesPlugin) Inject() []cordis.ServiceKey { return nil }
func (p resourceServicesPlugin) Apply(ctx *cordis.Context) (cordis.Effect, error) {
	if err := ctx.Provide(hostagent.ResourceRegistryService{Registry: p.registry, TenantID: p.tenantID}); err != nil {
		return nil, err
	}
	if err := ctx.Provide(hostagent.ResourceResolverService{Resolver: p.resolver, TenantID: p.tenantID}); err != nil {
		return nil, err
	}
	if p.service != nil {
		if err := ctx.Provide(p.service); err != nil {
			return nil, err
		}
	}
	return resourceServicesEffect{service: p.service}, nil
}

type resourceServicesEffect struct {
	service resource.HostResourceService
}

func (e resourceServicesEffect) Dispose(context.Context) error {
	if closer, ok := e.service.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
