package hostmcp

import (
	"context"
	"errors"
	"testing"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
)

func TestProviderLifecycleEventsAreDeclaredWithTheirModes(t *testing.T) {
	server, _ := newBindingTestServer(t)
	// Re-defining an event is the check: it proves the name was already
	// declared, and DefineEvent is the only place a mode is fixed.
	for name, definition := range providerEventDefinitions {
		if err := server.providerContext.DefineEvent(name, definition); err == nil {
			t.Fatalf("event %q was not declared at construction", name)
		}
	}
	// A lifecycle event is an announcement, so it must not be reachable as a
	// waterfall — that would let a listener rewrite a transition after it happened.
	if _, err := server.providerContext.Waterfall(context.Background(), ProviderEventActivated, nil); err == nil {
		t.Fatal("provider.activated was usable as a waterfall")
	}
	// Admission must not be reachable as a waterfall: a waterfall listener can
	// short-circuit the chain and force an allow past a listener that denies.
	if _, err := server.providerContext.Waterfall(context.Background(), ProviderEventAdmission, nil); err == nil {
		t.Fatal("provider.admission was usable as a waterfall")
	}
}

// admissionPlugin registers an admission listener the only way Cordis allows:
// from inside Apply, so the listener is owned by a fiber and disappears with it.
type admissionPlugin struct {
	id       string
	listener cordis.EventListener
}

func (p admissionPlugin) ID() string                  { return p.id }
func (p admissionPlugin) Inject() []cordis.ServiceKey { return nil }
func (p admissionPlugin) Apply(ctx *cordis.Context) (cordis.Effect, error) {
	if _, err := ctx.On(ProviderEventAdmission, p.listener); err != nil {
		return nil, err
	}
	return nil, nil
}

func TestProviderAdmissionListenerCanDenyButNotOverrideADenial(t *testing.T) {
	server, _ := newBindingTestServer(t)
	denial := errors.New("policy refused this generation")
	const providerID = "com.opute.admission-test"
	const generationID = providerID + "-1"

	var observed providercontract.ProviderEvent
	if _, err := server.providerContext.Plugin(admissionPlugin{id: "observer", listener: func(_ context.Context, value any, _ cordis.Next) (any, error) {
		event, ok := value.(providercontract.ProviderEvent)
		if !ok {
			t.Fatalf("admission payload = %T, want a typed ProviderEvent", value)
		}
		observed = event
		// Returning a rewritten payload must have no effect: the value is
		// discarded and the next listener still sees the original event.
		return providercontract.ProviderEvent{ProviderID: "com.opute.substituted"}, nil
	}}); err != nil {
		t.Fatalf("mount observer plugin: %v", err)
	}
	if err := server.admitProvider(context.Background(), providerID, generationID); err != nil {
		t.Fatalf("admission denied without a denying listener: %v", err)
	}
	if observed.ProviderID != providerID || observed.GenerationID != generationID {
		t.Fatalf("listener saw %+v, want the requested provider and generation", observed)
	}
	if observed.Name != ProviderEventAdmission {
		t.Fatalf("event name = %q, want %q", observed.Name, ProviderEventAdmission)
	}

	// A denial by any listener is final: a listener registered after it cannot
	// restore admission, because the waterfall stops at the error.
	if _, err := server.providerContext.Plugin(admissionPlugin{id: "denier", listener: func(context.Context, any, cordis.Next) (any, error) {
		return nil, denial
	}}); err != nil {
		t.Fatalf("mount denier plugin: %v", err)
	}
	// Registered after the denier, so serial order reaches it only if the
	// denial failed to stop the chain.
	downstreamRuns := 0
	if _, err := server.providerContext.Plugin(admissionPlugin{id: "allower", listener: func(_ context.Context, value any, _ cordis.Next) (any, error) {
		downstreamRuns++
		return value, nil
	}}); err != nil {
		t.Fatalf("mount allower plugin: %v", err)
	}
	if err := server.admitProvider(context.Background(), providerID, generationID); !errors.Is(err, denial) {
		t.Fatalf("admitProvider error = %v, want the listener's denial", err)
	}
	if downstreamRuns != 0 {
		t.Fatalf("a listener after the denier ran %d times; a denial must end the chain", downstreamRuns)
	}

	// Disposing the denier's fiber releases its listener, so admission is
	// allowed again without any listener having to be unregistered by hand.
	if err := server.providerContext.DisposePlugin(context.Background(), "denier"); err != nil {
		t.Fatalf("dispose denier plugin: %v", err)
	}
	if err := server.admitProvider(context.Background(), providerID, generationID); err != nil {
		t.Fatalf("admission still denied after the denying fiber was disposed: %v", err)
	}
	if downstreamRuns != 1 {
		t.Fatalf("downstream listener ran %d times after the denier was disposed, want 1", downstreamRuns)
	}
}
