// Package cordis contains the Host Agent's small, provider-neutral lifecycle
// kernel. It deliberately has no MCP, recipe, host mutation, or UI imports.
package cordis

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type ServiceKey string

type Service interface {
	Key() ServiceKey
}

type Effect interface {
	Dispose(context.Context) error
}

type Plugin interface {
	ID() string
	Inject() []ServiceKey
	Apply(*Context) (Effect, error)
}

type Fiber interface {
	ID() string
	Dispose(context.Context) error
}

type EventMode string

const (
	EventEmit      EventMode = "emit"
	EventWaterfall EventMode = "waterfall"
	EventParallel  EventMode = "parallel"
	EventSerial    EventMode = "serial"
)

type EventDefinition struct{ Mode EventMode }

type Next func(context.Context, any) (any, error)
type EventListener func(context.Context, any, Next) (any, error)

type listenerRegistration struct {
	id    uint64
	owner string
	fn    EventListener
}

type Context struct {
	mu           sync.RWMutex
	services     map[ServiceKey]Service
	serviceOwner map[ServiceKey]string
	plugins      map[string]*fiber
	listeners    map[string][]listenerRegistration
	eventDefs    map[string]EventDefinition
	current      *fiber
	applyMu      sync.Mutex
	nextID       uint64
	nextFiberSeq uint64
}

type fiber struct {
	ctx *Context
	id  string
	// seq is the mount order. Disposal must reverse the order fibers were
	// applied in, and map iteration cannot express that.
	seq       uint64
	effects   []Effect
	listeners []func()
	services  []ServiceKey
	mu        sync.Mutex
	disposed  bool
}

func NewContext() *Context {
	return &Context{
		services:     make(map[ServiceKey]Service),
		serviceOwner: make(map[ServiceKey]string),
		plugins:      make(map[string]*fiber),
		listeners:    make(map[string][]listenerRegistration),
		eventDefs:    make(map[string]EventDefinition),
	}
}

func (c *Context) DefineEvent(name string, definition EventDefinition) error {
	if c == nil || name == "" {
		return fmt.Errorf("event name is required")
	}
	switch definition.Mode {
	case EventEmit, EventWaterfall, EventParallel, EventSerial:
	default:
		return fmt.Errorf("unsupported event mode %q", definition.Mode)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.eventDefs[name]; exists {
		return fmt.Errorf("event %q is already defined", name)
	}
	c.eventDefs[name] = definition
	return nil
}

func (c *Context) Provide(service Service) error {
	if c == nil || service == nil || service.Key() == "" {
		return fmt.Errorf("service and service key are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return fmt.Errorf("service %q must be provided from a plugin", service.Key())
	}
	if _, exists := c.services[service.Key()]; exists {
		return fmt.Errorf("service %q is already provided", service.Key())
	}
	c.services[service.Key()] = service
	c.serviceOwner[service.Key()] = c.current.id
	c.current.services = append(c.current.services, service.Key())
	return nil
}

func (c *Context) Resolve(key ServiceKey) (Service, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	service, ok := c.services[key]
	return service, ok
}

func (c *Context) Plugin(plugin Plugin) (Fiber, error) {
	if c == nil || plugin == nil || plugin.ID() == "" {
		return nil, fmt.Errorf("plugin and plugin id are required")
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	c.mu.Lock()
	if _, exists := c.plugins[plugin.ID()]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("plugin %q is already mounted", plugin.ID())
	}
	for _, dependency := range plugin.Inject() {
		if _, exists := c.services[dependency]; !exists {
			c.mu.Unlock()
			return nil, fmt.Errorf("plugin %q requires unavailable service %q", plugin.ID(), dependency)
		}
	}
	c.nextFiberSeq++
	f := &fiber{ctx: c, id: plugin.ID(), seq: c.nextFiberSeq}
	c.current = f
	c.mu.Unlock()

	effect, err := plugin.Apply(c)
	c.mu.Lock()
	if c.current == f {
		c.current = nil
	}
	c.mu.Unlock()
	if err != nil {
		_ = f.Dispose(context.Background())
		return nil, fmt.Errorf("apply plugin %q: %w", plugin.ID(), err)
	}
	if effect != nil {
		f.effects = append(f.effects, effect)
	}
	c.mu.Lock()
	c.plugins[plugin.ID()] = f
	c.mu.Unlock()
	return f, nil
}

func (c *Context) Effect(effect Effect) error {
	if effect == nil {
		return fmt.Errorf("effect is required")
	}
	c.mu.RLock()
	f := c.current
	c.mu.RUnlock()
	if f == nil {
		return fmt.Errorf("no plugin fiber is applying")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.disposed {
		return fmt.Errorf("fiber %q is disposed", f.id)
	}
	f.effects = append(f.effects, effect)
	return nil
}

func (c *Context) On(event string, listener EventListener) (func(), error) {
	if event == "" || listener == nil {
		return nil, fmt.Errorf("event and listener are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return nil, fmt.Errorf("event listener must be registered from a plugin")
	}
	if _, exists := c.eventDefs[event]; !exists {
		return nil, fmt.Errorf("event %q is not defined", event)
	}
	c.nextID++
	registration := listenerRegistration{id: c.nextID, owner: c.current.id, fn: listener}
	c.listeners[event] = append(c.listeners[event], registration)
	stop := c.stopListenerLocked(event, registration.id)
	c.current.listeners = append(c.current.listeners, stop)
	return stop, nil
}

func (c *Context) stopListenerLocked(event string, id uint64) func() {
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		listeners := c.listeners[event]
		for index, registration := range listeners {
			if registration.id == id {
				c.listeners[event] = append(listeners[:index], listeners[index+1:]...)
				return
			}
		}
	}
}

func (c *Context) snapshotListeners(event string, expected EventMode) ([]listenerRegistration, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	definition, exists := c.eventDefs[event]
	if !exists {
		return nil, fmt.Errorf("event %q is not defined", event)
	}
	if definition.Mode != expected {
		return nil, fmt.Errorf("event %q is %s, requested %s", event, definition.Mode, expected)
	}
	return append([]listenerRegistration(nil), c.listeners[event]...), nil
}

func (c *Context) Emit(ctx context.Context, event string, value any) error {
	listeners, err := c.snapshotListeners(event, EventEmit)
	if err != nil {
		return err
	}
	for _, registration := range listeners {
		if _, err := registration.fn(ctx, value, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) Serial(ctx context.Context, event string, value any) error {
	listeners, err := c.snapshotListeners(event, EventSerial)
	if err != nil {
		return err
	}
	for _, registration := range listeners {
		if _, err := registration.fn(ctx, value, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) Parallel(ctx context.Context, event string, value any) error {
	listeners, err := c.snapshotListeners(event, EventParallel)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(listeners))
	for _, registration := range listeners {
		registration := registration
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := registration.fn(ctx, value, nil); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func (c *Context) Waterfall(ctx context.Context, event string, value any) (any, error) {
	listeners, err := c.snapshotListeners(event, EventWaterfall)
	if err != nil {
		return nil, err
	}
	var invoke func(int, any) (any, error)
	invoke = func(index int, current any) (any, error) {
		if index == len(listeners) {
			return current, nil
		}
		return listeners[index].fn(ctx, current, func(nextCtx context.Context, nextValue any) (any, error) {
			return invoke(index+1, nextValue)
		})
	}
	return invoke(0, value)
}

func (f *fiber) ID() string { return f.id }

func (f *fiber) Dispose(ctx context.Context) error {
	f.mu.Lock()
	if f.disposed {
		f.mu.Unlock()
		return nil
	}
	f.disposed = true
	effects := append([]Effect(nil), f.effects...)
	listeners := append([]func(){}, f.listeners...)
	services := append([]ServiceKey(nil), f.services...)
	f.effects = nil
	f.listeners = nil
	f.services = nil
	f.mu.Unlock()

	for index := len(listeners) - 1; index >= 0; index-- {
		listeners[index]()
	}
	var firstErr error
	for index := len(effects) - 1; index >= 0; index-- {
		if err := effects[index].Dispose(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	f.ctx.mu.Lock()
	for _, key := range services {
		if f.ctx.serviceOwner[key] == f.id {
			delete(f.ctx.serviceOwner, key)
			delete(f.ctx.services, key)
		}
	}
	delete(f.ctx.plugins, f.id)
	f.ctx.mu.Unlock()
	return firstErr
}

// DisposePlugin disposes exactly one mounted plugin by id, releasing the
// services it provided and running its effects in reverse. Disposing an
// unknown or already-disposed plugin is a no-op, so retirement is idempotent.
func (c *Context) DisposePlugin(ctx context.Context, id string) error {
	c.mu.RLock()
	f, ok := c.plugins[id]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	return f.Dispose(ctx)
}

// ServiceKeys returns the keys of every service currently provided, sorted so
// the listing is stable. It exposes identity only: resolving a key still goes
// through Resolve, so no caller can reach a fiber or an effect through it.
func (c *Context) ServiceKeys() []ServiceKey {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]ServiceKey, 0, len(c.services))
	for key := range c.services {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// PluginIDs returns the ids of every live plugin in the order they were
// mounted. Disposal order is defined as the reverse of this, so a caller
// tearing down a subset gets the same ordering guarantee Dispose gives the
// whole context (C-09).
func (c *Context) PluginIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fibers := make([]*fiber, 0, len(c.plugins))
	for _, f := range c.plugins {
		fibers = append(fibers, f)
	}
	sort.Slice(fibers, func(i, j int) bool { return fibers[i].seq < fibers[j].seq })
	ids := make([]string, 0, len(fibers))
	for _, f := range fibers {
		ids = append(ids, f.id)
	}
	return ids
}

func (c *Context) Dispose(ctx context.Context) error {
	c.mu.RLock()
	fibers := make([]*fiber, 0, len(c.plugins))
	for _, fiber := range c.plugins {
		fibers = append(fibers, fiber)
	}
	c.mu.RUnlock()
	// Sort by mount order so disposal is deterministically the reverse of
	// application. Ranging over the plugin map would dispose in Go's
	// randomized map order, which C-09 forbids.
	sort.Slice(fibers, func(i, j int) bool { return fibers[i].seq < fibers[j].seq })
	var firstErr error
	for index := len(fibers) - 1; index >= 0; index-- {
		if err := fibers[index].Dispose(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
