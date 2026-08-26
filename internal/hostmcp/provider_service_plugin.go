package hostmcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
)

// providerServiceCordisKey scopes a provider service to the generation that
// published it. The unscoped provider/service identity names the offering;
// the generation suffix keeps a candidate isolated from active lookup until
// activation succeeds (C-08). Nothing parses this key back into its parts.
func providerServiceCordisKey(providerID, serviceID, generationID string) cordis.ServiceKey {
	return cordis.ServiceKey(providercontract.ServiceKey(providerID, serviceID) + "@" + strings.TrimSpace(generationID))
}

// providerServiceValue is the typed service a mounted provider service
// exposes. The Cordis context sees this contract, never the adapter's
// endpoint, MCP session, or JSON-RPC payloads (C-02).
type providerServiceValue struct {
	key          cordis.ServiceKey
	providerID   string
	generationID string
	capabilityID string
	service      providercontract.ServiceDefinition
	adapter      *provideradapter.Adapter
	// inject is the resolved dependency graph for this mount: the concrete
	// service keys the declared capability families bound to.
	inject []cordis.ServiceKey
}

func (v *providerServiceValue) Key() cordis.ServiceKey { return v.key }

// OfferingKey is the generation-independent identity of everything this
// service offers. It is what the catalog indexes an offering by.
func (v *providerServiceValue) OfferingKey() string {
	return providercontract.ServiceKey(v.providerID, v.service.ID)
}

// providerServiceEffect owns teardown for one mounted provider service. The
// fiber calls it in reverse registration order, which is what makes provider
// replacement reversible without a hand-written restore path (C-09).
type providerServiceEffect struct {
	dispose func(context.Context) error
}

func (e providerServiceEffect) Dispose(ctx context.Context) error {
	if e.dispose == nil {
		return nil
	}
	return e.dispose(ctx)
}

// providerServicePlugin mounts exactly one provider service for one
// generation. A provider is composed of these mounts rather than being a map
// entry, so every adapter, listener, and session it creates has an owner that
// can dispose it.
type providerServicePlugin struct {
	value   *providerServiceValue
	dispose func(context.Context) error
}

func (p *providerServicePlugin) ID() string { return string(p.value.key) }

func (p *providerServicePlugin) Inject() []cordis.ServiceKey { return p.value.inject }

func (p *providerServicePlugin) Apply(ctx *cordis.Context) (cordis.Effect, error) {
	if err := ctx.Provide(p.value); err != nil {
		return nil, err
	}
	return providerServiceEffect{dispose: p.dispose}, nil
}

// resolveProviderServiceDependencies maps a service's declared capability
// dependencies onto the concrete service keys currently provided to the
// context. A dependency naming a family that nothing provides fails the mount
// rather than deferring the failure to first call.
//
// Ambiguity fails closed here. Once capability requirements carry typed
// feature constraints, an ambiguous family is resolved by the offering
// resolver instead of rejected.
func resolveProviderServiceDependencies(
	dependencies []providercontract.CapabilityRef,
	provided map[cordis.ServiceKey]string,
) ([]cordis.ServiceKey, error) {
	if len(dependencies) == 0 {
		return nil, nil
	}
	keys := make([]cordis.ServiceKey, 0, len(dependencies))
	for _, dependency := range dependencies {
		family := strings.TrimSpace(dependency.ID)
		matches := make([]cordis.ServiceKey, 0, 1)
		for key, capabilityID := range provided {
			if capabilityID == family {
				matches = append(matches, key)
			}
		}
		sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("capability %q is not provided by any mounted service", family)
		case 1:
			keys = append(keys, matches[0])
		default:
			return nil, fmt.Errorf("capability %q is provided by %d mounted services; typed provenance is required", family, len(matches))
		}
	}
	return keys, nil
}

// mountProviderGeneration mounts every service a provider generation declares
// as its own Cordis plugin. Mounting is all-or-nothing: a failure disposes the
// fibers already applied, so a partially mounted generation can never become
// addressable.
func (s *Server) mountProviderGeneration(
	manifest providercontract.InstallManifest,
	generationID string,
	adapter *provideradapter.Adapter,
) error {
	if adapter == nil {
		return fmt.Errorf("provider %q generation %q has no connected adapter", manifest.Provider.ID, generationID)
	}
	if strings.TrimSpace(generationID) == "" {
		return fmt.Errorf("provider %q must be mounted against a generation", manifest.Provider.ID)
	}
	mounted := make([]string, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		key := providerServiceCordisKey(manifest.Provider.ID, service.ID, generationID)
		inject, err := resolveProviderServiceDependencies(service.Dependencies, s.injectableServiceFamilies(generationID))
		if err != nil {
			_ = s.unmountProviderPlugins(mounted)
			return fmt.Errorf("mount provider service %q: %w", service.ID, err)
		}
		plugin := &providerServicePlugin{
			value: &providerServiceValue{
				key: key, providerID: manifest.Provider.ID, generationID: generationID,
				capabilityID: strings.TrimSpace(service.CapabilityID), service: service, adapter: adapter,
				inject: inject,
			},
		}
		// One adapter serves every service of a generation, so exactly one
		// mount owns closing it. The first mount is chosen because disposal
		// reverses mount order: it tears down last, after every other service
		// of the generation is already gone.
		if len(mounted) == 0 {
			connection := adapter
			plugin.dispose = func(context.Context) error { return connection.Close() }
		}
		if _, err := s.providerContext.Plugin(plugin); err != nil {
			_ = s.unmountProviderPlugins(mounted)
			return fmt.Errorf("mount provider service %q: %w", service.ID, err)
		}
		mounted = append(mounted, string(key))
	}
	return nil
}

func (s *Server) unmountProviderPlugins(ids []string) error {
	var errs []error
	for index := len(ids) - 1; index >= 0; index-- {
		if err := s.providerContext.DisposePlugin(context.Background(), ids[index]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// unmountProviderGeneration disposes every service mounted for one
// generation. This is the whole of provider retirement: there is no map to
// clear afterwards.
func (s *Server) unmountProviderGeneration(providerID, generationID string) error {
	suffix := "@" + strings.TrimSpace(generationID)
	prefix := providercontract.ServiceKey(providerID, "")
	// Plugin ids come back in mount order, so unmountProviderPlugins disposes
	// this generation in true reverse mount order — which is what makes the
	// first-mounted service the last to go, and therefore the safe owner of
	// closing the shared adapter.
	ids := make([]string, 0)
	for _, id := range s.providerContext.PluginIDs() {
		if strings.HasSuffix(id, suffix) && strings.HasPrefix(id, prefix) {
			ids = append(ids, id)
		}
	}
	return s.unmountProviderPlugins(ids)
}

// injectableServiceFamilies maps the capability family of every service that
// a mount may legitimately inject.
//
// Only two generations qualify: the one being mounted, and whichever
// generation is currently active for its provider. A superseded generation is
// still mounted while its replacement is being brought up, but it is on its
// way out — injecting from it would bind a fresh service to a connection about
// to be disposed, and would make every family ambiguous for the duration of
// every swap. Restricting the scope here is what lets a replacement be mounted
// before its predecessor is torn down (C-08).
func (s *Server) injectableServiceFamilies(mountingGenerationID string) map[cordis.ServiceKey]string {
	keys := s.providerContext.ServiceKeys()
	families := make(map[cordis.ServiceKey]string, len(keys))
	active := make(map[string]string)
	for _, key := range keys {
		service, ok := s.providerContext.Resolve(key)
		if !ok {
			continue
		}
		value, ok := service.(*providerServiceValue)
		if !ok {
			continue
		}
		if value.generationID != mountingGenerationID {
			activeID, known := active[value.providerID]
			if !known {
				if generation, ok := s.providerLifecycle.Active(value.providerID); ok {
					activeID = generation.ID
				}
				active[value.providerID] = activeID
			}
			if value.generationID != activeID {
				continue
			}
		}
		families[key] = value.capabilityID
	}
	return families
}

// providerServiceValueFor resolves one mounted provider service.
func (s *Server) providerServiceValueFor(providerID, serviceID, generationID string) (*providerServiceValue, bool) {
	service, ok := s.providerContext.Resolve(providerServiceCordisKey(providerID, serviceID, generationID))
	if !ok {
		return nil, false
	}
	value, ok := service.(*providerServiceValue)
	return value, ok
}

// providerGenerationAdapter returns the adapter owned by a mounted
// generation. A generation that is not mounted has no adapter, which is what
// makes an unmounted generation unusable rather than merely unlisted.
func (s *Server) providerGenerationAdapter(providerID, generationID string) *provideradapter.Adapter {
	suffix := "@" + strings.TrimSpace(generationID)
	prefix := providercontract.ServiceKey(providerID, "")
	for _, key := range s.providerContext.ServiceKeys() {
		id := string(key)
		if !strings.HasSuffix(id, suffix) || !strings.HasPrefix(id, prefix) {
			continue
		}
		service, ok := s.providerContext.Resolve(key)
		if !ok {
			continue
		}
		if value, ok := service.(*providerServiceValue); ok {
			return value.adapter
		}
	}
	return nil
}
