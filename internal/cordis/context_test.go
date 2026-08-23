package cordis

import (
	"context"
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
