package hostmcp

import (
	"context"
	"fmt"
	"time"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
)

// Provider lifecycle event names. Each transition the generation manager can
// make has exactly one event, so an observer never has to infer a transition
// from the absence of another.
const (
	ProviderEventCandidate = "provider.candidate"
	ProviderEventReady     = "provider.ready"
	ProviderEventActivated = "provider.activated"
	ProviderEventDraining  = "provider.draining"
	ProviderEventStopped   = "provider.stopped"

	// ProviderEventAdmission is the one interception point: a listener may
	// refuse an activation, and refusal is all it may do.
	//
	// It is deliberately serial rather than waterfall. A waterfall listener
	// short-circuits the chain by returning without calling next, which would
	// let one listener force an allow past a peer that would have denied.
	// Serial runs every listener in registration order and aborts on the first
	// error, so a denial cannot be overridden and the returned value is moot.
	ProviderEventAdmission = "provider.admission"
)

var providerEventDefinitions = map[string]cordis.EventDefinition{
	ProviderEventCandidate: {Mode: cordis.EventEmit},
	ProviderEventReady:     {Mode: cordis.EventEmit},
	ProviderEventActivated: {Mode: cordis.EventEmit},
	ProviderEventDraining:  {Mode: cordis.EventEmit},
	ProviderEventStopped:   {Mode: cordis.EventEmit},
	ProviderEventAdmission: {Mode: cordis.EventSerial},
}

// defineProviderEvents declares the provider lifecycle vocabulary on a
// context. Declaring up front is what makes On and Emit fail closed on an
// unknown or mis-moded event rather than silently doing nothing.
func defineProviderEvents(ctx *cordis.Context) error {
	for name, definition := range providerEventDefinitions {
		if err := ctx.DefineEvent(name, definition); err != nil {
			return fmt.Errorf("define provider event %q: %w", name, err)
		}
	}
	return nil
}

func (s *Server) providerEvent(name, providerID, generationID, reason string) providercontract.ProviderEvent {
	return providercontract.ProviderEvent{
		Name:            name,
		ProviderID:      providerID,
		GenerationID:    generationID,
		CatalogRevision: s.CatalogSnapshot().Revision,
		Reason:          reason,
		At:              time.Now().UTC(),
	}
}

// emitProviderLifecycleEvent announces a transition that has already happened.
// A listener error cannot undo it, so the error is surfaced to the caller but
// the transition is never rolled back on its account — interception belongs to
// admitProvider, before the fact.
func (s *Server) emitProviderLifecycleEvent(ctx context.Context, name, providerID, generationID, reason string) {
	if s.providerContext == nil {
		return
	}
	_ = s.providerContext.Emit(ctx, name, s.providerEvent(name, providerID, generationID, reason))
}

// admitProvider asks every admission listener whether an activation may
// proceed. A listener votes by returning an error or not; it can neither
// rewrite the event nor force an allow past a peer that already denied.
func (s *Server) admitProvider(ctx context.Context, providerID, generationID string) error {
	if s.providerContext == nil {
		return nil
	}
	event := s.providerEvent(ProviderEventAdmission, providerID, generationID, "")
	if err := s.providerContext.Serial(ctx, ProviderEventAdmission, event); err != nil {
		return fmt.Errorf("provider %q generation %q was denied admission: %w", providerID, generationID, err)
	}
	return nil
}
