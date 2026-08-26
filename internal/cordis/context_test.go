package cordis

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
)

type testService struct{ key ServiceKey }

func (s testService) Key() ServiceKey { return s.key }

type testEffect struct {
	called *int32
	order  *[]string
	name   string
}

func (e testEffect) Dispose(context.Context) error {
	atomic.AddInt32(e.called, 1)
	*e.order = append(*e.order, e.name)
	return nil
}

type testPlugin struct {
	id       string
	requires []ServiceKey
	apply    func(*Context) (Effect, error)
}

func (p testPlugin) ID() string                       { return p.id }
func (p testPlugin) Inject() []ServiceKey             { return p.requires }
func (p testPlugin) Apply(c *Context) (Effect, error) { return p.apply(c) }

func TestPluginDependenciesAndReverseDisposal(t *testing.T) {
	c := NewContext()
	if _, err := c.Plugin(testPlugin{id: "consumer", requires: []ServiceKey{"provider"}}); err == nil {
		t.Fatal("plugin with missing dependency was mounted")
	}
	var disposed int32
	var order []string
	provider, err := c.Plugin(testPlugin{id: "provider", apply: func(c *Context) (Effect, error) {
		if err := c.Provide(testService{key: "provider"}); err != nil {
			return nil, err
		}
		if err := c.Effect(testEffect{called: &disposed, order: &order, name: "provider"}); err != nil {
			return nil, err
		}
		return testEffect{called: &disposed, order: &order, name: "returned"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Resolve("provider"); !ok {
		t.Fatal("provided service is not resolvable")
	}
	consumer, err := c.Plugin(testPlugin{id: "consumer", requires: []ServiceKey{"provider"}, apply: func(c *Context) (Effect, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if disposed != 2 || !reflect.DeepEqual(order, []string{"returned", "provider"}) {
		t.Fatalf("disposal = %d/%v", disposed, order)
	}
	if _, ok := c.Resolve("provider"); ok {
		t.Fatal("service survived fiber disposal")
	}
}

func TestTypedEventModesAndWaterfallShortCircuit(t *testing.T) {
	c := NewContext()
	for name, mode := range map[string]EventMode{"emit": EventEmit, "serial": EventSerial, "parallel": EventParallel, "waterfall": EventWaterfall} {
		if err := c.DefineEvent(name, EventDefinition{Mode: mode}); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	plugin, err := c.Plugin(testPlugin{id: "events", apply: func(c *Context) (Effect, error) {
		if _, err := c.On("emit", func(_ context.Context, value any, _ Next) (any, error) {
			seen = append(seen, value.(string))
			return nil, nil
		}); err != nil {
			return nil, err
		}
		if _, err := c.On("waterfall", func(_ context.Context, value any, next Next) (any, error) {
			return next(context.Background(), value.(string)+"-first")
		}); err != nil {
			return nil, err
		}
		if _, err := c.On("waterfall", func(_ context.Context, value any, _ Next) (any, error) {
			return value.(string) + "-stopped", nil
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Emit(context.Background(), "emit", "observed"); err != nil {
		t.Fatal(err)
	}
	value, err := c.Waterfall(context.Background(), "waterfall", "start")
	if err != nil {
		t.Fatal(err)
	}
	if value != "start-first-stopped" || !reflect.DeepEqual(seen, []string{"observed"}) {
		t.Fatalf("events = %#v/%v", value, seen)
	}
	if err := c.Serial(context.Background(), "parallel", nil); err == nil {
		t.Fatal("wrong event dispatch mode was accepted")
	}
	if err := plugin.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Emit(context.Background(), "emit", "after-dispose"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []string{"observed"}) {
		t.Fatalf("disposed listener still ran: %v", seen)
	}
}

// orderedPlugin records its disposal into a shared slice so a test can assert
// the order fibers were torn down in, not merely that they were.
type orderedPlugin struct {
	id       string
	inject   []ServiceKey
	provide  Service
	disposed *[]string
}

func (p orderedPlugin) ID() string           { return p.id }
func (p orderedPlugin) Inject() []ServiceKey { return p.inject }
func (p orderedPlugin) Apply(c *Context) (Effect, error) {
	if p.provide != nil {
		if err := c.Provide(p.provide); err != nil {
			return nil, err
		}
	}
	id := p.id
	disposed := p.disposed
	return effectFunc(func(context.Context) error {
		*disposed = append(*disposed, id)
		return nil
	}), nil
}

type effectFunc func(context.Context) error

func (f effectFunc) Dispose(ctx context.Context) error { return f(ctx) }

type namedService struct{ key ServiceKey }

func (s namedService) Key() ServiceKey { return s.key }

func TestContextDisposesFibersInReverseMountOrder(t *testing.T) {
	// Ten plugins make a chance pass from randomized map iteration
	// vanishingly unlikely, which a two- or three-plugin case would not.
	const count = 10
	for attempt := 0; attempt < 5; attempt++ {
		ctx := NewContext()
		disposed := make([]string, 0, count)
		expected := make([]string, 0, count)
		for index := 0; index < count; index++ {
			id := fmt.Sprintf("plugin-%02d", index)
			if _, err := ctx.Plugin(orderedPlugin{id: id, disposed: &disposed}); err != nil {
				t.Fatal(err)
			}
			expected = append([]string{id}, expected...)
		}
		if err := ctx.Dispose(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(disposed) != count {
			t.Fatalf("disposed %d fibers, want %d", len(disposed), count)
		}
		for index := range expected {
			if disposed[index] != expected[index] {
				t.Fatalf("attempt %d disposal order %v, want %v", attempt, disposed, expected)
			}
		}
	}
}

func TestContextMountFailsClosedOnUnavailableDependency(t *testing.T) {
	ctx := NewContext()
	if _, err := ctx.Plugin(orderedPlugin{id: "dependent", inject: []ServiceKey{"opute.missing"}}); err == nil {
		t.Fatal("plugin mounted despite an unavailable dependency")
	}
	if _, err := ctx.Plugin(orderedPlugin{id: "provider", provide: namedService{key: "opute.missing"}}); err != nil {
		t.Fatal(err)
	}
	// Boot order follows the dependency graph: the same mount succeeds once
	// the service it injects is present.
	if _, err := ctx.Plugin(orderedPlugin{id: "dependent", inject: []ServiceKey{"opute.missing"}}); err != nil {
		t.Fatalf("dependent plugin failed after its dependency was provided: %v", err)
	}
}

func TestDisposedFiberReleasesItsIDForRemount(t *testing.T) {
	ctx := NewContext()
	disposed := make([]string, 0, 2)
	fiber, err := ctx.Plugin(orderedPlugin{id: "generation", provide: namedService{key: "opute.generation"}, disposed: &disposed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctx.Plugin(orderedPlugin{id: "generation", disposed: &disposed}); err == nil {
		t.Fatal("a mounted plugin id was reused")
	}
	if err := fiber.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := ctx.Resolve("opute.generation"); ok {
		t.Fatal("disposal left the fiber's service resolvable")
	}
	// Generation swap remounts the same identity, so disposal must free both
	// the plugin id and every service key the fiber owned.
	if _, err := ctx.Plugin(orderedPlugin{id: "generation", provide: namedService{key: "opute.generation"}, disposed: &disposed}); err != nil {
		t.Fatalf("remount after disposal failed: %v", err)
	}
}

func TestPluginIDsFollowMountOrderNotLexicalOrder(t *testing.T) {
	ctx := NewContext()
	// Mount in an order that is the exact reverse of lexical, so a sorted
	// listing cannot be mistaken for a mount-ordered one. Callers reverse this
	// slice to dispose, and that reversal is only correct if the order is the
	// order things were applied.
	mounted := []string{"zulu", "yankee", "xray", "alpha"}
	for _, id := range mounted {
		disposed := make([]string, 0, 1)
		if _, err := ctx.Plugin(orderedPlugin{id: id, disposed: &disposed}); err != nil {
			t.Fatalf("mount %q: %v", id, err)
		}
	}
	got := ctx.PluginIDs()
	if len(got) != len(mounted) {
		t.Fatalf("PluginIDs() = %v, want %d entries", got, len(mounted))
	}
	for index, id := range mounted {
		if got[index] != id {
			t.Fatalf("PluginIDs() = %v, want mount order %v", got, mounted)
		}
	}
}
