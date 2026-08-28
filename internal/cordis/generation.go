package cordis

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

type GenerationState string

const (
	GenerationCandidate GenerationState = "candidate"
	GenerationReady     GenerationState = "ready"
	GenerationActive    GenerationState = "active"
	GenerationDraining  GenerationState = "draining"
	GenerationStopped   GenerationState = "stopped"
	GenerationFailed    GenerationState = "failed"
)

type ProviderGeneration struct {
	ID              string
	Provider        providercontract.ProviderRef
	ManifestHash    string
	Endpoint        string
	CatalogRevision string
	State           GenerationState
	CreatedAt       time.Time
	ActiveAt        time.Time
	Sessions        int
}

type DrainPolicy struct {
	Timeout         time.Duration
	CancelOnTimeout bool
}

type ProviderLifecycleManager struct {
	mu          sync.Mutex
	generations map[string]*ProviderGeneration
	active      map[string]string
	sequence    uint64
	drain       DrainPolicy
}

// Restore rehydrates a generation projection from durable state. Runtime
// adapters remain process-local and must be connected separately by the
// provider boundary before they can serve calls.
func (m *ProviderLifecycleManager) Restore(g ProviderGeneration) error {
	if m == nil || g.ID == "" || g.Provider.ID == "" || g.Provider.Version == "" || g.ManifestHash == "" || g.Endpoint == "" {
		return fmt.Errorf("provider generation identity, manifest hash, and endpoint are required")
	}
	if g.State == "" {
		g.State = GenerationCandidate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.generations[g.ID]; exists {
		return nil
	}
	copy := g
	m.generations[g.ID] = &copy
	if index := restoredGenerationSequence(g.ID); index > m.sequence {
		m.sequence = index
	}
	if g.State == GenerationActive {
		if current := m.active[g.Provider.ID]; current != "" && current != g.ID {
			return fmt.Errorf("provider %q has multiple active generations", g.Provider.ID)
		}
		m.active[g.Provider.ID] = g.ID
	}
	return nil
}

func restoredGenerationSequence(id string) uint64 {
	separator := strings.LastIndex(id, "-")
	if separator < 0 || separator == len(id)-1 {
		return 0
	}
	suffix := id[separator+1:]
	value, err := strconv.ParseUint(suffix, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func (m *ProviderLifecycleManager) Get(id string) (ProviderGeneration, bool) {
	if m == nil {
		return ProviderGeneration{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	generation, ok := m.generations[id]
	if !ok {
		return ProviderGeneration{}, false
	}
	return *generation, true
}

// Refresh records the current provider declaration after a live reconnect.
// The manifest hash and catalog revision are durable projections of the
// connected generation, not inputs that can prevent recovery when the
// provider's declaration evolves between process lifetimes.
func (m *ProviderLifecycleManager) Refresh(id, manifestHash, catalogRevision string) (ProviderGeneration, error) {
	if m == nil || strings.TrimSpace(id) == "" || strings.TrimSpace(manifestHash) == "" || strings.TrimSpace(catalogRevision) == "" {
		return ProviderGeneration{}, fmt.Errorf("provider generation, manifest hash, and catalog revision are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	generation, ok := m.generations[id]
	if !ok {
		return ProviderGeneration{}, fmt.Errorf("provider generation %q not found", id)
	}
	if generation.State != GenerationActive {
		return ProviderGeneration{}, fmt.Errorf("provider generation %q is %s, expected active", id, generation.State)
	}
	generation.ManifestHash = manifestHash
	generation.CatalogRevision = catalogRevision
	return *generation, nil
}

func NewProviderLifecycleManager(policy DrainPolicy) *ProviderLifecycleManager {
	if policy.Timeout <= 0 {
		policy.Timeout = 30 * time.Second
	}
	return &ProviderLifecycleManager{generations: make(map[string]*ProviderGeneration), active: make(map[string]string), drain: policy}
}

func (m *ProviderLifecycleManager) CreateCandidate(provider providercontract.ProviderRef, manifestHash, endpoint, catalogRevision string) (ProviderGeneration, error) {
	if m == nil || provider.ID == "" || provider.Version == "" || manifestHash == "" || endpoint == "" || strings.TrimSpace(catalogRevision) == "" {
		return ProviderGeneration{}, fmt.Errorf("provider, manifest hash, endpoint, and catalog revision are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequence++
	id := fmt.Sprintf("%s-%d", provider.ID, m.sequence)
	generation := &ProviderGeneration{ID: id, Provider: provider, ManifestHash: manifestHash, Endpoint: endpoint, CatalogRevision: catalogRevision, State: GenerationCandidate, CreatedAt: time.Now().UTC()}
	m.generations[id] = generation
	return *generation, nil
}

func (m *ProviderLifecycleManager) MarkReady(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	generation, ok := m.generations[id]
	if !ok {
		return fmt.Errorf("provider generation %q not found", id)
	}
	if generation.State != GenerationCandidate {
		return fmt.Errorf("provider generation %q is %s, expected candidate", id, generation.State)
	}
	generation.State = GenerationReady
	return nil
}

// Activate atomically switches new work to a ready generation. The previous
// generation is returned in draining state and remains addressable for
// existing sessions.
func (m *ProviderLifecycleManager) Activate(id string) (previous *ProviderGeneration, activated ProviderGeneration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate, ok := m.generations[id]
	if !ok {
		return nil, ProviderGeneration{}, fmt.Errorf("provider generation %q not found", id)
	}
	if candidate.State != GenerationReady {
		return nil, ProviderGeneration{}, fmt.Errorf("provider generation %q is %s, expected ready", id, candidate.State)
	}
	if oldID := m.active[candidate.Provider.ID]; oldID != "" && oldID != id {
		old := m.generations[oldID]
		if old != nil && old.State == GenerationActive {
			old.State = GenerationDraining
			copy := *old
			previous = &copy
		}
	}
	candidate.State = GenerationActive
	candidate.ActiveAt = time.Now().UTC()
	m.active[candidate.Provider.ID] = id
	return previous, *candidate, nil
}

// RollbackActivation restores the previous active generation after a
// post-activation failure. The new generation is failed and remains
// undispatchable; the previous generation becomes active again.
func (m *ProviderLifecycleManager) RollbackActivation(id, previousID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.generations[id]
	if !ok || m.active[current.Provider.ID] != id || current.State != GenerationActive {
		return fmt.Errorf("provider generation %q is not the active generation", id)
	}
	if current.Sessions > 0 {
		return fmt.Errorf("provider generation %q has %d active session(s)", id, current.Sessions)
	}
	if previousID == "" {
		delete(m.active, current.Provider.ID)
		current.State = GenerationFailed
		return nil
	}
	previous, ok := m.generations[previousID]
	if !ok || previous.Provider.ID != current.Provider.ID || previous.State != GenerationDraining {
		return fmt.Errorf("provider generation %q cannot restore previous generation %q", id, previousID)
	}
	previous.State = GenerationActive
	current.State = GenerationFailed
	m.active[current.Provider.ID] = previousID
	return nil
}

func (m *ProviderLifecycleManager) Active(providerID string) (ProviderGeneration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.active[providerID]
	generation, ok := m.generations[id]
	if !ok || generation.State != GenerationActive {
		return ProviderGeneration{}, false
	}
	return *generation, true
}

func (m *ProviderLifecycleManager) OpenSession(providerID string) (*GenerationSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.active[providerID]
	generation, ok := m.generations[id]
	if !ok || generation.State != GenerationActive {
		return nil, fmt.Errorf("provider %q has no active generation", providerID)
	}
	generation.Sessions++
	return &GenerationSession{manager: m, generationID: id}, nil
}

// OpenSessionForGeneration binds work to an explicitly selected generation.
// Candidate validation uses this seam before activation; it never consults
// the mutable provider-to-active lookup.
func (m *ProviderLifecycleManager) OpenSessionForGeneration(providerID, generationID string) (*GenerationSession, error) {
	if strings.TrimSpace(generationID) == "" {
		return nil, fmt.Errorf("provider generation is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	generation, ok := m.generations[generationID]
	if !ok || generation.Provider.ID != providerID {
		return nil, fmt.Errorf("provider generation %q is not registered for provider %q", generationID, providerID)
	}
	switch generation.State {
	case GenerationCandidate, GenerationReady, GenerationActive, GenerationDraining:
		generation.Sessions++
		return &GenerationSession{manager: m, generationID: generationID}, nil
	default:
		return nil, fmt.Errorf("provider generation %q is %s", generationID, generation.State)
	}
}

type GenerationSession struct {
	manager      *ProviderLifecycleManager
	generationID string
	once         sync.Once
}

func (s *GenerationSession) GenerationID() string { return s.generationID }

func (s *GenerationSession) Close() {
	if s == nil || s.manager == nil {
		return
	}
	s.once.Do(func() {
		s.manager.mu.Lock()
		if generation := s.manager.generations[s.generationID]; generation != nil && generation.Sessions > 0 {
			generation.Sessions--
		}
		s.manager.mu.Unlock()
	})
}

func (m *ProviderLifecycleManager) Drain(ctx context.Context, id string) error {
	deadline := time.NewTimer(m.drain.Timeout)
	defer deadline.Stop()
	for {
		m.mu.Lock()
		generation, ok := m.generations[id]
		sessions := 0
		if ok {
			sessions = generation.Sessions
		}
		m.mu.Unlock()
		if !ok {
			return fmt.Errorf("provider generation %q not found", id)
		}
		if sessions == 0 {
			return m.Dispose(id)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if m.drain.CancelOnTimeout {
				return m.Dispose(id)
			}
			return fmt.Errorf("provider generation %q drain timed out with %d session(s)", id, sessions)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (m *ProviderLifecycleManager) Fail(id string, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	generation, ok := m.generations[id]
	if !ok {
		return fmt.Errorf("provider generation %q not found", id)
	}
	generation.State = GenerationFailed
	if m.active[generation.Provider.ID] == id {
		delete(m.active, generation.Provider.ID)
	}
	return nil
}

func (m *ProviderLifecycleManager) Dispose(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	generation, ok := m.generations[id]
	if !ok {
		return fmt.Errorf("provider generation %q not found", id)
	}
	if generation.Sessions > 0 {
		return fmt.Errorf("provider generation %q still has %d session(s)", id, generation.Sessions)
	}
	if m.active[generation.Provider.ID] == id {
		delete(m.active, generation.Provider.ID)
	}
	generation.State = GenerationStopped
	return nil
}
