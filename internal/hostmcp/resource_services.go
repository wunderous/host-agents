package hostmcp

import (
	"context"

	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/hostagent"
)

type resourceServicesPlugin struct {
	registry hostagent.ResourceRegistry
	resolver *hostagent.Service
	tenantID string
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
	return resourceServicesEffect{}, nil
}

type resourceServicesEffect struct{}

func (resourceServicesEffect) Dispose(context.Context) error { return nil }
