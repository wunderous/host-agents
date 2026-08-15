package resource

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wunderous/host-agents/internal/heartbeat"
)

// Class describes the host-level cost of a tool invocation.
type Class string

const (
	ClassControl Class = "control"
	ClassNormal  Class = "normal"
	ClassHeavy   Class = "heavy"
)

// Config is the host-wide admission policy. LockDir must be shared by all
// co-resident host-agent instances; it must not be an instance-private state
// directory.
type Config struct {
	LockDir                 string
	MaxNormal               int
	MaxHeavy                int
	MaxQueued               int
	MinAvailableMemoryBytes int64
	MinAvailableDiskBytes   int64
	DiskPaths               []string
}

// DefaultConfig returns a conservative policy for a WSL/Incus development
// host. The caller should override LockDir and thresholds from configuration.
func DefaultConfig(lockDir string) Config {
	return Config{
		LockDir:   lockDir,
		MaxNormal: 2,
		MaxHeavy:  1,
		MaxQueued: 16,
		DiskPaths: []string{"/"},
	}
}

// PressureSnapshot is safe to expose in diagnostics and heartbeat metadata.
type PressureSnapshot struct {
	Pressure             string `json:"pressure"`
	Reason               string `json:"reason,omitempty"`
	MemoryAvailableBytes int64  `json:"memoryAvailableBytes,omitempty"`
	DiskAvailableBytes   int64  `json:"diskAvailableBytes,omitempty"`
	DiskPressure         string `json:"diskPressure,omitempty"`
	NormalInFlight       int    `json:"normalInFlight"`
	HeavyInFlight        int    `json:"heavyInFlight"`
	Queued               int    `json:"queued"`
	CheckedAt            string `json:"checkedAt"`
}

// AdmissionError is a retryable host-local capacity response.
type AdmissionError struct {
	Code         string `json:"code"`
	Class        Class  `json:"class"`
	Pressure     string `json:"pressure"`
	Reason       string `json:"reason"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

func (e *AdmissionError) Error() string {
	if e == nil {
		return "host resource admission failed"
	}
	return fmt.Sprintf("%s: class=%s pressure=%s reason=%s retryAfterMs=%d", e.Code, e.Class, e.Pressure, e.Reason, e.RetryAfterMs)
}

type Coordinator struct {
	config Config
	lock   hostLock

	mu           sync.Mutex
	normalActive int
	heavyActive  int
	queued       int
	heavyQueued  int
	notify       chan struct{}
}

func NewCoordinator(config Config) (*Coordinator, error) {
	if config.MaxNormal <= 0 {
		config.MaxNormal = 2
	}
	if config.MaxHeavy <= 0 {
		config.MaxHeavy = 1
	}
	if config.MaxQueued <= 0 {
		config.MaxQueued = 16
	}
	if len(config.DiskPaths) == 0 {
		config.DiskPaths = []string{"/"}
	}
	lock, err := newHostLock(config.LockDir)
	if err != nil {
		return nil, err
	}
	return &Coordinator{config: config, lock: lock, notify: make(chan struct{})}, nil
}

func ClassifyTool(tool string) Class {
	name := strings.ToLower(strings.TrimSpace(tool))
	if name == "" || name == "health" || name == "ping" || name == "host_agent_heartbeat" ||
		name == "register_host_agent" || name == "get_host_health" || name == "cancel_operation" ||
		name == "get_host_info" || name == "get_local_status" || name == "list_vms" ||
		name == "list_clusters" || name == "list_agents" || name == "get_agent" ||
		name == "inspect_container_storage" ||
		strings.HasPrefix(name, "get_") || strings.HasPrefix(name, "list_") ||
		strings.HasPrefix(name, "check_") {
		return ClassControl
	}
	if isHeavyTool(name) {
		return ClassHeavy
	}
	return ClassNormal
}

func isHeavyTool(name string) bool {
	for _, candidate := range []string{
		"install_incus_stack", "reset_incus_stack", "provision_container", "provision_vm", "create_vm",
		"install_k3s", "install_postgresql", "ensure_oci_builder", "configure_oci_storage", "cleanup_container_storage",
		"reconcile_postgresql_service", "reconcile_postgresql_service", "ensure_pgvector", "remove_postgresql_service", "remove_postgresql_service",
		"build_and_push_oci_image", "prepare_host_agent_artifacts", "stage_build_context",
		"ensure_host_tool", "install_host_agent", "install_local_llm_model",
		"configure_local_llm_model", "start_local_llm_runtime", "configure_local_llm_runtime",
	} {
		if name == candidate {
			return true
		}
	}
	return false
}

// Acquire waits for a host-local permit. The returned function must be
// called exactly once, including on operation cancellation.
func (c *Coordinator) Acquire(ctx context.Context, tool string) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	class := ClassifyTool(tool)
	if class == ClassControl {
		return func() {}, nil
	}

	for {
		pressure := c.pressure()
		if class == ClassHeavy && pressure.Pressure == "critical" {
			return nil, &AdmissionError{
				Code:         "host_resource_pressure",
				Class:        class,
				Pressure:     pressure.Pressure,
				Reason:       pressure.Reason,
				RetryAfterMs: 5000,
			}
		}

		c.mu.Lock()
		if c.canReserve(class) {
			c.reserve(class)
			c.mu.Unlock()
			releaseLock, err := c.lock.acquire(ctx, class == ClassHeavy)
			if err != nil {
				c.release(class)
				return nil, err
			}
			return func() {
				releaseLock()
				c.release(class)
			}, nil
		}
		if c.queued >= c.config.MaxQueued {
			c.mu.Unlock()
			return nil, &AdmissionError{
				Code:         "host_capacity_saturated",
				Class:        class,
				Pressure:     pressure.Pressure,
				Reason:       "host admission queue is full",
				RetryAfterMs: 1000,
			}
		}
		c.queued++
		if class == ClassHeavy {
			c.heavyQueued++
		}
		notify := c.notify
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.dequeue(class)
			c.signal()
			c.mu.Unlock()
			return nil, ctx.Err()
		case <-notify:
			c.mu.Lock()
			c.dequeue(class)
			c.mu.Unlock()
		}
	}
}

func (c *Coordinator) Snapshot() PressureSnapshot {
	pressure := c.pressure()
	c.mu.Lock()
	pressure.NormalInFlight = c.normalActive
	pressure.HeavyInFlight = c.heavyActive
	pressure.Queued = c.queued
	c.mu.Unlock()
	return pressure
}

// Metadata returns a JSON-safe snapshot for heartbeat and diagnostics.
func (c *Coordinator) Metadata() map[string]any {
	snapshot := c.Snapshot()
	return map[string]any{
		"pressure":             snapshot.Pressure,
		"reason":               snapshot.Reason,
		"memoryAvailableBytes": snapshot.MemoryAvailableBytes,
		"diskAvailableBytes":   snapshot.DiskAvailableBytes,
		"diskPressure":         snapshot.DiskPressure,
		"normalInFlight":       snapshot.NormalInFlight,
		"heavyInFlight":        snapshot.HeavyInFlight,
		"queued":               snapshot.Queued,
		"checkedAt":            snapshot.CheckedAt,
	}
}

func (c *Coordinator) pressure() PressureSnapshot {
	stats := heartbeat.ReadHostSystemStatsForPaths(c.config.DiskPaths)
	pressure := "normal"
	reason := ""
	if stats.MemoryPressure == "critical" || (c.config.MinAvailableMemoryBytes > 0 && stats.MemoryAvailableBytes > 0 && stats.MemoryAvailableBytes < c.config.MinAvailableMemoryBytes) {
		pressure = "critical"
		reason = "available memory is below the host admission threshold"
	} else if stats.MemoryPressure == "warning" {
		pressure = "warning"
		reason = "available memory is low"
	}
	if stats.DiskPressure == "critical" || (c.config.MinAvailableDiskBytes > 0 && stats.DiskAvailableBytes > 0 && stats.DiskAvailableBytes < c.config.MinAvailableDiskBytes) {
		pressure = "critical"
		reason = "available disk is below the host admission threshold"
	} else if pressure != "critical" && stats.DiskPressure == "warning" {
		pressure = "warning"
		reason = "available disk is low"
	}
	return PressureSnapshot{
		Pressure:             pressure,
		Reason:               reason,
		MemoryAvailableBytes: stats.MemoryAvailableBytes,
		DiskAvailableBytes:   stats.DiskAvailableBytes,
		DiskPressure:         stats.DiskPressure,
		CheckedAt:            time.Now().UTC().Format(time.RFC3339),
	}
}

func (c *Coordinator) canReserve(class Class) bool {
	if class == ClassHeavy {
		return c.heavyActive < c.config.MaxHeavy && c.normalActive == 0
	}
	return c.heavyActive == 0 && c.normalActive < c.config.MaxNormal && c.heavyQueued == 0
}

func (c *Coordinator) reserve(class Class) {
	if class == ClassHeavy {
		c.heavyActive++
	} else {
		c.normalActive++
	}
}

func (c *Coordinator) release(class Class) {
	c.mu.Lock()
	if class == ClassHeavy {
		c.heavyActive--
	} else {
		c.normalActive--
	}
	c.signal()
	c.mu.Unlock()
}

func (c *Coordinator) dequeue(class Class) {
	if c.queued > 0 {
		c.queued--
	}
	if class == ClassHeavy && c.heavyQueued > 0 {
		c.heavyQueued--
	}
}

func (c *Coordinator) signal() {
	close(c.notify)
	c.notify = make(chan struct{})
}
