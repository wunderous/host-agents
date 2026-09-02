package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/heartbeat"
	"github.com/wunderous/host-agents/internal/resourceid"
)

// HostResourcePolicyRevision is the current neutral policy contract. Concrete
// WSL/systemd and Incus renderers may project it, but they do not change the
// service API or the identity of a reservation.
const HostResourcePolicyRevision = "opute-host-resource-policy.v1"

const (
	EnforcementEnforced    = "enforced"
	EnforcementUnsupported = "unsupported"
	EnforcementUnknown     = "unknown"
)

// PolicyReconcileFunc is the narrow concrete-backend seam for approval-gated
// policy repair. The neutral resource service validates the revision, tenant,
// and host-service target before invoking it; the callback owns the platform
// mechanism and must not accept an arbitrary unit, cgroup path, or command.
type PolicyReconcileFunc func(context.Context, resourceid.URI) error

// HostResourceService is the single typed admission and capacity service.
// Cordis only knows this service by its key; it does not know WSL, systemd,
// Incus, shell commands, or MCP.
type HostResourceService interface {
	cordis.Service
	Snapshot() CapacitySnapshot
	Admit(context.Context, AdmissionRequest) (*Reservation, error)
	Release(*Reservation) error
	// ReclaimTerminalTaskReservations removes durable reservations whose owning
	// task is known to be terminal after a process restart. Reservations for
	// working or input_required tasks remain fenced until their owner releases
	// them or their normal expiry is reached.
	ReclaimTerminalTaskReservations(map[string]struct{}) (int, error)
	Reconcile(context.Context, string, string) (CapacitySnapshot, error)
}

// ResourceLimits are effective policy limits for one host boundary.
type ResourceLimits struct {
	CPUCores    float64 `json:"cpuCores,omitempty"`
	MemoryBytes int64   `json:"memoryBytes,omitempty"`
	DiskBytes   int64   `json:"diskBytes,omitempty"`
	Tasks       int64   `json:"tasks,omitempty"`
}

// ResourceUsage is observed usage at the host cgroup/filesystem boundary.
type ResourceUsage struct {
	CPUCores             float64 `json:"cpuCores,omitempty"`
	MemoryBytes          int64   `json:"memoryBytes,omitempty"`
	MemoryAvailableBytes int64   `json:"memoryAvailableBytes,omitempty"`
	DiskBytes            int64   `json:"diskBytes,omitempty"`
	DiskAvailableBytes   int64   `json:"diskAvailableBytes,omitempty"`
	Tasks                int64   `json:"tasks,omitempty"`
}

// ReservationTotals keeps reservations separate from observed process usage.
// This prevents a queued VM from being admitted merely because its bytes have
// not been allocated yet.
type ReservationTotals struct {
	Count       int     `json:"count"`
	CPUCores    float64 `json:"cpuCores,omitempty"`
	MemoryBytes int64   `json:"memoryBytes,omitempty"`
	DiskBytes   int64   `json:"diskBytes,omitempty"`
	Tasks       int64   `json:"tasks,omitempty"`
}

type QueueState struct {
	Queued       int `json:"queued"`
	HeavyQueued  int `json:"heavyQueued"`
	NormalActive int `json:"normalActive"`
	HeavyActive  int `json:"heavyActive"`
}

// CapacitySnapshot is the sanitized projection exposed to diagnostics and
// the neutral MCP capability. It deliberately contains no provider command,
// cgroup path, PID, or systemd unit name.
type CapacitySnapshot struct {
	PressureSnapshot
	PolicyRevision  string            `json:"policyRevision"`
	ObservedLimits  ResourceLimits    `json:"observedLimits"`
	EffectiveLimits ResourceLimits    `json:"effectiveLimits"`
	CurrentUsage    ResourceUsage     `json:"currentUsage"`
	Reservations    ReservationTotals `json:"reservations"`
	Queue           QueueState        `json:"queue"`
	Enforcement     string            `json:"enforcement"`
}

// AdmissionRequest is an explicit, typed cost and ownership request. A
// provider operation must carry these fields from its capability descriptor;
// the resource service never classifies a request from its name.
type AdmissionRequest struct {
	CPUCores            float64 `json:"cpuCores,omitempty"`
	MemoryBytes         int64   `json:"memoryBytes,omitempty"`
	DiskBytes           int64   `json:"diskBytes,omitempty"`
	Tasks               int64   `json:"tasks,omitempty"`
	Class               Class   `json:"class"`
	Operation           string  `json:"operation"`
	AgentID             string  `json:"agentId"`
	OperationID         string  `json:"operationId,omitempty"`
	TaskID              string  `json:"taskId,omitempty"`
	ResourceURI         string  `json:"resourceUri,omitempty"`
	GenerationID        string  `json:"generationId,omitempty"`
	ParentReservationID string  `json:"parentReservationId,omitempty"`
}

// Reservation is a durable ownership handle. Inherited reservations are
// views used by nested provider callbacks and do not release the parent.
type Reservation struct {
	ID        string           `json:"id"`
	Request   AdmissionRequest `json:"request"`
	CreatedAt time.Time        `json:"createdAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
	inherited bool
}

type persistedReservation struct {
	ID        string           `json:"id"`
	Request   AdmissionRequest `json:"request"`
	CreatedAt string           `json:"createdAt"`
	ExpiresAt string           `json:"expiresAt"`
}

type RequestError struct {
	Code   string `json:"code"`
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Reason)
}

type ReconcileError struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

func (e *ReconcileError) Error() string { return e.Code + ": " + e.Reason }

var reservationSequence atomic.Uint64

type reservationContextKey struct{}

type operationIdentity struct {
	operationID string
	taskID      string
}

// WithOperationIdentity associates durable task ownership with a request
// without mutating the caller's raw argument object.
func WithOperationIdentity(ctx context.Context, operationID, taskID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, operationIdentityContextKey{}, operationIdentity{operationID: strings.TrimSpace(operationID), taskID: strings.TrimSpace(taskID)})
}

func OperationIdentityFromContext(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	identity, _ := ctx.Value(operationIdentityContextKey{}).(operationIdentity)
	return identity.operationID, identity.taskID
}

type operationIdentityContextKey struct{}

// WithReservation carries one outer provider admission through nested Host
// Agent callbacks without taking a second global permit.
func WithReservation(ctx context.Context, reservation *Reservation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if reservation == nil || strings.TrimSpace(reservation.ID) == "" {
		return ctx
	}
	return context.WithValue(ctx, reservationContextKey{}, reservation)
}

func ReservationFromContext(ctx context.Context) (*Reservation, bool) {
	if ctx == nil {
		return nil, false
	}
	reservation, ok := ctx.Value(reservationContextKey{}).(*Reservation)
	return reservation, ok && reservation != nil && strings.TrimSpace(reservation.ID) != ""
}

// Key lets Coordinator be mounted directly as the Cordis-owned service.
func (*Coordinator) Key() cordis.ServiceKey { return HostResourceServiceKey }

const HostResourceServiceKey cordis.ServiceKey = "opute.host.resource"

// DefaultCostForClass is an explicit compatibility cost for built-in
// registrations that predate resource-cost metadata. Dynamic provider
// definitions must provide their own typed cost instead.
func DefaultCostForClass(class Class) AdmissionRequest {
	switch class {
	case ClassHeavy:
		return AdmissionRequest{Class: class, CPUCores: 2, MemoryBytes: 2 << 30, Tasks: 8}
	case ClassNormal:
		return AdmissionRequest{Class: class, CPUCores: 0.25, MemoryBytes: 256 << 20, Tasks: 1}
	default:
		return AdmissionRequest{Class: ClassControl}
	}
}

func newReservationID() string {
	return fmt.Sprintf("host-reservation-%d-%d", time.Now().UTC().UnixNano(), reservationSequence.Add(1))
}

func (c *Coordinator) Admit(ctx context.Context, request AdmissionRequest) (*Reservation, error) {
	if c == nil {
		return &Reservation{ID: "unmanaged", Request: request, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC()}, nil
	}
	if c.closed.Load() {
		return nil, &RequestError{Code: "host_resource_unavailable", Reason: "resource service is closed"}
	}
	if request.Class == "" {
		request.Class = ClassNormal
	}
	if err := validateAdmissionRequest(request); err != nil {
		return nil, err
	}
	if parent, ok := ReservationFromContext(ctx); ok {
		if request.ParentReservationID != "" && request.ParentReservationID != parent.ID {
			return nil, &RequestError{Code: "host_reservation_parent_mismatch", Reason: "request parent reservation does not match the reservation in context"}
		}
		if time.Now().Before(parent.ExpiresAt) {
			if !reservationOwnerCanInherit(parent.Request, request) {
				return nil, &RequestError{Code: "host_reservation_owner_mismatch", Reason: "nested reservation owner does not match the reservation in context"}
			}
			inherited := *parent
			inherited.Request = request
			inherited.inherited = true
			return &inherited, nil
		}
		if request.ParentReservationID != "" {
			return nil, &RequestError{Code: "host_reservation_expired", Reason: "the parent reservation has expired"}
		}
	}
	if request.Class == ClassControl && request.CPUCores == 0 && request.MemoryBytes == 0 && request.DiskBytes == 0 && request.Tasks == 0 {
		return &Reservation{ID: "control", Request: request, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(c.config.ReservationTTL)}, nil
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	lockRelease, err := c.reservationLock.acquire(ctx, true)
	if err != nil {
		return nil, err
	}
	defer lockRelease()

	records, err := c.readReservations()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	changed := removeExpired(records, now)
	pressure := c.pressure()
	if request.Class == ClassHeavy && pressure.Pressure == "critical" {
		if changed {
			_ = c.writeReservations(records)
		}
		return nil, c.admissionError(request, pressure, "host_resource_pressure", pressure.Reason)
	}
	if c.config.FailClosedOnUnknown && request.Class != ClassControl && pressure.CgroupEnforcement != EnforcementEnforced {
		if changed {
			_ = c.writeReservations(records)
		}
		return nil, c.admissionError(request, pressure, "host_resource_enforcement_unknown", "workload cgroup enforcement is not verified")
	}
	if !c.fits(request, records, pressure) {
		if changed {
			_ = c.writeReservations(records)
		}
		return nil, c.admissionError(request, pressure, "host_capacity_saturated", "declared resource cost exceeds effective host capacity")
	}
	reservation := &Reservation{
		ID: newReservationID(), Request: request, CreatedAt: now,
		ExpiresAt: now.Add(c.config.ReservationTTL),
	}
	records[reservation.ID] = persistedReservation{
		ID: reservation.ID, Request: request,
		CreatedAt: reservation.CreatedAt.Format(time.RFC3339Nano),
		ExpiresAt: reservation.ExpiresAt.Format(time.RFC3339Nano),
	}
	if err := c.writeReservations(records); err != nil {
		return nil, err
	}
	return reservation, nil
}

func (c *Coordinator) Release(reservation *Reservation) error {
	if c == nil || reservation == nil || reservation.inherited || reservation.ID == "" || reservation.ID == "control" || reservation.ID == "unmanaged" {
		return nil
	}
	lockRelease, err := c.reservationLock.acquire(context.Background(), true)
	if err != nil {
		return err
	}
	defer lockRelease()
	records, err := c.readReservations()
	if err != nil {
		return err
	}
	record, ok := records[reservation.ID]
	if !ok {
		return nil
	}
	if !sameReservationOwner(record.Request, reservation.Request) {
		return &RequestError{Code: "host_reservation_owner_mismatch", Reason: "reservation ownership does not match the releasing operation"}
	}
	delete(records, reservation.ID)
	return c.writeReservations(records)
}

// ReclaimTerminalTaskReservations repairs the restart boundary without
// guessing from operation names or deleting live ownership. The task registry
// is the authority for terminal state; this coordinator only removes the
// matching durable reservation records.
func (c *Coordinator) ReclaimTerminalTaskReservations(taskIDs map[string]struct{}) (int, error) {
	if c == nil || len(taskIDs) == 0 {
		return 0, nil
	}
	lockRelease, err := c.reservationLock.acquire(context.Background(), true)
	if err != nil {
		return 0, err
	}
	defer lockRelease()
	records, err := c.readReservations()
	if err != nil {
		return 0, err
	}
	removed := 0
	for id, record := range records {
		taskID := strings.TrimSpace(record.Request.TaskID)
		if taskID == "" {
			continue
		}
		if _, ok := taskIDs[taskID]; !ok {
			continue
		}
		delete(records, id)
		removed++
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, c.writeReservations(records)
}

func (c *Coordinator) Reconcile(ctx context.Context, policyRevision, targetURI string) (CapacitySnapshot, error) {
	if c == nil {
		return CapacitySnapshot{}, &ReconcileError{Code: "host_resource_unavailable", Reason: "resource service is unavailable"}
	}
	if err := ctxErr(ctx); err != nil {
		return CapacitySnapshot{}, err
	}
	if strings.TrimSpace(policyRevision) == "" || policyRevision != c.config.PolicyRevision {
		return CapacitySnapshot{}, &ReconcileError{Code: "host_resource_policy_revision_mismatch", Reason: "requested policy revision is not the installed revision"}
	}
	parsed, err := resourceid.Parse(targetURI)
	if err != nil || parsed.ResourceType != resourceid.TypeHostService {
		return CapacitySnapshot{}, &ReconcileError{Code: "host_resource_target_invalid", Reason: "target must be an exact host-service URI"}
	}
	if c.config.TenantID != "" && parsed.TenantID != c.config.TenantID {
		return CapacitySnapshot{}, &ReconcileError{Code: "host_resource_target_foreign_tenant", Reason: "target host-service URI belongs to another tenant"}
	}
	if c.config.ReconcilePolicy == nil {
		return CapacitySnapshot{}, &ReconcileError{Code: "host_resource_reconcile_unsupported", Reason: "the concrete host resource policy backend is not mounted"}
	}
	if err := c.config.ReconcilePolicy(ctx, parsed); err != nil {
		if reconcileErr, ok := err.(*ReconcileError); ok {
			return CapacitySnapshot{}, reconcileErr
		}
		return CapacitySnapshot{}, &ReconcileError{Code: "host_resource_reconcile_failed", Reason: err.Error()}
	}
	return c.Snapshot(), nil
}

// Close stops new admissions while retaining durable reservations for normal
// expiry/recovery. A process restart must not silently erase ownership that a
// still-running workload may hold.
func (c *Coordinator) Close() error {
	if c == nil || c.closed.Swap(true) {
		return nil
	}
	c.mu.Lock()
	c.signal()
	c.mu.Unlock()
	return nil
}

func validateAdmissionRequest(request AdmissionRequest) error {
	for field, value := range map[string]float64{"cpuCores": request.CPUCores} {
		if value < 0 {
			return &RequestError{Code: "host_resource_request_invalid", Field: field, Reason: "resource cost cannot be negative"}
		}
	}
	for field, value := range map[string]int64{"memoryBytes": request.MemoryBytes, "diskBytes": request.DiskBytes, "tasks": request.Tasks} {
		if value < 0 {
			return &RequestError{Code: "host_resource_request_invalid", Field: field, Reason: "resource cost cannot be negative"}
		}
	}
	switch request.Class {
	case ClassControl, ClassNormal, ClassHeavy:
	default:
		return &RequestError{Code: "host_resource_request_invalid", Field: "class", Reason: "class must be control, normal, or heavy"}
	}
	return nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (c *Coordinator) admissionError(request AdmissionRequest, pressure PressureSnapshot, code, reason string) error {
	return &AdmissionError{Code: code, Class: request.Class, Pressure: pressure.Pressure, Reason: reason, RetryAfterMs: 1000}
}

func (c *Coordinator) fits(request AdmissionRequest, records map[string]persistedReservation, pressure PressureSnapshot) bool {
	classCounts := reservationClassCounts(records)
	c.mu.Lock()
	heavyActive, normalActive, heavyQueued := c.heavyActive, c.normalActive, c.heavyQueued
	c.mu.Unlock()
	switch request.Class {
	case ClassHeavy:
		if classCounts.heavy >= c.config.MaxHeavy || classCounts.normal > 0 || heavyActive > 0 || normalActive > 0 {
			return false
		}
	case ClassNormal:
		if classCounts.heavy > 0 || heavyActive > 0 || classCounts.normal+normalActive >= c.config.MaxNormal || heavyQueued > 0 {
			return false
		}
	}
	stats := heartbeat.ReadHostSystemStatsForPaths(c.config.DiskPaths)
	limits := c.effectiveLimits(stats)
	totals := reservationTotals(records)
	if limits.CPUCores > 0 && totals.CPUCores+request.CPUCores > limits.CPUCores {
		return false
	}
	if limits.MemoryBytes > 0 {
		available := limits.MemoryBytes - observedMemoryUsage(stats)
		if available < 0 || totals.MemoryBytes+request.MemoryBytes > available {
			return false
		}
	}
	if limits.DiskBytes > 0 {
		available := limits.DiskBytes - (stats.DiskTotalBytes - stats.DiskAvailableBytes)
		if available < 0 || totals.DiskBytes+request.DiskBytes > available {
			return false
		}
	}
	if limits.Tasks > 0 && totals.Tasks+request.Tasks+stats.TasksCurrent > limits.Tasks {
		return false
	}
	if c.config.MinAvailableMemoryBytes > 0 && pressure.MemoryAvailableBytes > 0 && pressure.MemoryAvailableBytes < c.config.MinAvailableMemoryBytes {
		return false
	}
	if c.config.MinAvailableDiskBytes > 0 && pressure.DiskAvailableBytes > 0 && pressure.DiskAvailableBytes < c.config.MinAvailableDiskBytes {
		return false
	}
	return true
}

func (c *Coordinator) capacitySnapshot(pressure PressureSnapshot) CapacitySnapshot {
	lockRelease, err := c.reservationLock.acquire(context.Background(), true)
	if err != nil {
		return CapacitySnapshot{PressureSnapshot: pressure, PolicyRevision: c.config.PolicyRevision, Enforcement: enforcementFor(c.config, pressure)}
	}
	defer lockRelease()
	records, err := c.readReservations()
	if err != nil {
		records = nil
	}
	if removeExpired(records, time.Now().UTC()) {
		_ = c.writeReservations(records)
	}
	stats := heartbeat.ReadHostSystemStatsForPaths(c.config.DiskPaths)
	observed := ResourceLimits{CPUCores: stats.CPUQuotaCores, MemoryBytes: stats.MemoryLimitBytes, DiskBytes: stats.DiskTotalBytes, Tasks: stats.TasksLimit}
	effective := c.effectiveLimits(stats)
	c.mu.Lock()
	heavyQueued := c.heavyQueued
	c.mu.Unlock()
	return CapacitySnapshot{
		PressureSnapshot: pressure,
		PolicyRevision:   c.config.PolicyRevision,
		ObservedLimits:   observed,
		EffectiveLimits:  effective,
		CurrentUsage:     ResourceUsage{MemoryBytes: observedMemoryUsage(stats), MemoryAvailableBytes: stats.MemoryAvailableBytes, DiskBytes: stats.DiskTotalBytes - stats.DiskAvailableBytes, DiskAvailableBytes: stats.DiskAvailableBytes, Tasks: stats.TasksCurrent},
		Reservations:     reservationTotals(records),
		Queue:            QueueState{Queued: pressure.Queued, NormalActive: pressure.NormalInFlight, HeavyActive: pressure.HeavyInFlight, HeavyQueued: heavyQueued},
		Enforcement:      enforcementFor(c.config, pressure),
	}
}

func enforcementFor(config Config, pressure PressureSnapshot) string {
	if config.EnforcementMode == EnforcementEnforced || config.EnforcementMode == EnforcementUnsupported || config.EnforcementMode == EnforcementUnknown {
		return config.EnforcementMode
	}
	if pressure.CgroupEnforcement != "" {
		return pressure.CgroupEnforcement
	}
	return EnforcementUnknown
}

func (c *Coordinator) effectiveLimits(stats heartbeat.HostSystemStats) ResourceLimits {
	limits := ResourceLimits{CPUCores: stats.CPUQuotaCores, MemoryBytes: stats.MemoryLimitBytes, DiskBytes: stats.DiskTotalBytes, Tasks: stats.TasksLimit}
	if limits.CPUCores <= 0 {
		limits.CPUCores = float64(stats.CPUCount)
	}
	if limits.MemoryBytes <= 0 {
		limits.MemoryBytes = stats.MemoryTotalBytes
	}
	if c.config.CPUCapacityCores > 0 && (limits.CPUCores <= 0 || c.config.CPUCapacityCores < limits.CPUCores) {
		limits.CPUCores = c.config.CPUCapacityCores
	}
	if c.config.MemoryCapacityBytes > 0 && (limits.MemoryBytes <= 0 || c.config.MemoryCapacityBytes < limits.MemoryBytes) {
		limits.MemoryBytes = c.config.MemoryCapacityBytes
	}
	if c.config.DiskCapacityBytes > 0 && (limits.DiskBytes <= 0 || c.config.DiskCapacityBytes < limits.DiskBytes) {
		limits.DiskBytes = c.config.DiskCapacityBytes
	}
	if c.config.TaskCapacity > 0 && (limits.Tasks <= 0 || c.config.TaskCapacity < limits.Tasks) {
		limits.Tasks = c.config.TaskCapacity
	}
	return limits
}

func observedMemoryUsage(stats heartbeat.HostSystemStats) int64 {
	// cgroup memory.current covers the agent's current hierarchy, while
	// /proc/meminfo covers sibling workload slices and Incus guests. Admission
	// must account for both; using the larger observed value is conservative and
	// avoids allowing a new reservation merely because it lives in a sibling
	// cgroup.
	if stats.MemoryUsedBytes > stats.MemoryUsageBytes {
		return stats.MemoryUsedBytes
	}
	return stats.MemoryUsageBytes
}

func reservationTotals(records map[string]persistedReservation) ReservationTotals {
	totals := ReservationTotals{}
	for _, record := range records {
		totals.Count++
		totals.CPUCores += record.Request.CPUCores
		totals.MemoryBytes += record.Request.MemoryBytes
		totals.DiskBytes += record.Request.DiskBytes
		totals.Tasks += record.Request.Tasks
	}
	return totals
}

type classCounts struct{ normal, heavy int }

func reservationClassCounts(records map[string]persistedReservation) classCounts {
	counts := classCounts{}
	for _, record := range records {
		switch record.Request.Class {
		case ClassHeavy:
			counts.heavy++
		case ClassNormal:
			counts.normal++
		}
	}
	return counts
}

func removeExpired(records map[string]persistedReservation, now time.Time) bool {
	changed := false
	for id, record := range records {
		expires, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
		if err == nil && !expires.After(now) {
			delete(records, id)
			changed = true
		}
	}
	return changed
}

func sameReservationOwner(left, right AdmissionRequest) bool {
	if left.AgentID != right.AgentID && (left.AgentID != "" || right.AgentID != "") {
		return false
	}
	if left.OperationID != right.OperationID && (left.OperationID != "" || right.OperationID != "") {
		return false
	}
	if left.TaskID != right.TaskID && (left.TaskID != "" || right.TaskID != "") {
		return false
	}
	return true
}

// reservationOwnerCanInherit permits a nested provider callback to use the
// parent's capacity reservation while still preventing a different agent from
// smuggling work through a reservation it does not own. Nested operation names
// intentionally differ; task identity, when present, remains exact.
func reservationOwnerCanInherit(parent, nested AdmissionRequest) bool {
	if parent.AgentID != nested.AgentID {
		return false
	}
	if parent.TaskID != "" && parent.TaskID != nested.TaskID {
		return false
	}
	return true
}

func (c *Coordinator) readReservations() (map[string]persistedReservation, error) {
	records := make(map[string]persistedReservation)
	raw, err := os.ReadFile(c.reservationPath)
	if os.IsNotExist(err) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return records, nil
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("read host resource reservations: %w", err)
	}
	return records, nil
}

func (c *Coordinator) writeReservations(records map[string]persistedReservation) error {
	if len(records) == 0 {
		if err := os.Remove(c.reservationPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.reservationPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.reservationPath)
}
