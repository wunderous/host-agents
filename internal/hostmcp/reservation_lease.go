package hostmcp

import (
	"context"
	"sync"
	"time"

	"github.com/wunderous/host-agents/internal/resource"
)

// reservationRenewInterval sits well inside the reservation TTL so a run
// survives one slow or contended renewal rather than losing its lease to it.
const reservationRenewInterval = 2 * time.Minute

type reservationLeaseKey struct{}

// reservationLease owns the release of a reservation admitted for work that
// outlives the call which admitted it.
//
// A lifecycle tool that launches a durable plan returns as soon as the run is
// recorded, but the host capacity it reserved is occupied by the run, not by
// the launching call. Releasing the reservation when that call returned made
// admission decorative for every asynchronous lifecycle tool -- two heavy plans
// could hold the single heavy permit at once -- and, before the release landed,
// left the run's own nodes contending with their launcher's still-persisted
// reservation instead of inheriting it. That is how a read-only
// inspect_host_service inside a provider teardown was refused with
// host_capacity_saturated on a host with 10.6 GiB free.
//
// So the launcher offers the lease and the run claims it. Exactly one of the
// two releases the reservation, and a claimed lease is renewed for as long as
// the run executes.
type reservationLease struct {
	admission   resource.HostResourceService
	reservation *resource.Reservation
	mu          sync.Mutex
	claimed     bool
	released    bool
}

func newReservationLease(admission resource.HostResourceService, reservation *resource.Reservation) *reservationLease {
	if admission == nil || reservation == nil {
		return nil
	}
	return &reservationLease{admission: admission, reservation: reservation}
}

func withReservationLease(ctx context.Context, lease *reservationLease) context.Context {
	if lease == nil {
		return ctx
	}
	return context.WithValue(ctx, reservationLeaseKey{}, lease)
}

// claimReservationLease transfers release authority from the calling request to
// the asynchronous run being started. It reports nil when there is nothing to
// claim, so a caller that starts no run changes nothing.
func claimReservationLease(ctx context.Context) *reservationLease {
	if ctx == nil {
		return nil
	}
	lease, ok := ctx.Value(reservationLeaseKey{}).(*reservationLease)
	if !ok || lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.claimed || lease.released {
		return nil
	}
	lease.claimed = true
	return lease
}

// releaseIfUnclaimed is the synchronous caller's release: it runs when the
// lifecycle call returns and does nothing once a run has taken the lease over.
func (l *reservationLease) releaseIfUnclaimed() {
	if l == nil {
		return
	}
	l.mu.Lock()
	claimed := l.claimed
	l.mu.Unlock()
	if claimed {
		return
	}
	l.finish()
}

// keepAlive renews the lease until the run's context ends, so a plan longer
// than the reservation TTL keeps the record its nodes inherit.
func (l *reservationLease) keepAlive(ctx context.Context) {
	if l == nil || ctx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(reservationRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.mu.Lock()
				released := l.released
				l.mu.Unlock()
				if released {
					return
				}
				// A refused renewal means the record is gone, which the run
				// cannot repair; stop renewing and let the nodes admit on
				// their own rather than spinning against a lost lease.
				if err := l.admission.Renew(l.reservation); err != nil {
					return
				}
			}
		}
	}()
}

// finish releases the reservation once. It is safe on a nil lease so the plan
// runner can call it without knowing whether its run was launched under one.
func (l *reservationLease) finish() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	l.mu.Unlock()
	_ = l.admission.Release(l.reservation)
}
