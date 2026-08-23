package tasks

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultTTL     = time.Hour
	PollIntervalMs = 3000
)

type Status string

const (
	StatusWorking       Status = "working"
	StatusCompleted     Status = "completed"
	StatusFailed        Status = "failed"
	StatusCancelled     Status = "cancelled"
	StatusInputRequired Status = "input_required"
)

type ToolResult struct {
	Content           []map[string]any `json:"content,omitempty"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

type Record struct {
	TaskID        string         `json:"taskId"`
	ToolName      string         `json:"toolName"`
	ToolArgs      map[string]any `json:"toolArgs"`
	Status        Status         `json:"status"`
	StatusMessage string         `json:"statusMessage,omitempty"`
	Description   string         `json:"description,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     string         `json:"createdAt"`
	LastUpdatedAt string         `json:"lastUpdatedAt"`
	TTL           int64          `json:"ttl"`
	PollInterval  int            `json:"pollInterval"`
	Logs          []string       `json:"logs,omitempty"`
	ToolResult    *ToolResult    `json:"-"`
	resultCh      chan ToolResult
	cancel        func()
}

type Registry struct {
	mu    sync.RWMutex
	tasks map[string]*Record
}

func NewRegistry() *Registry {
	return &Registry{tasks: make(map[string]*Record)}
}

func (r *Registry) Create(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any) *Record {
	return r.create(toolName, toolArgs, ttl, description, metadata, nil)
}

func (r *Registry) CreateWithCancel(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	return r.create(toolName, toolArgs, ttl, description, metadata, cancel)
}

// CreateWithID restores a durable operation identity after a process restart.
// A working in-memory record is reused; a completed/failed/cancelled record is
// replaced only when the caller explicitly resumes it.
func (r *Registry) CreateWithID(taskID, toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.tasks[taskID]; ok && existing.Status == StatusWorking {
		return existing
	}
	rec := newRecord(taskID, toolName, toolArgs, ttl, description, metadata, cancel)
	r.tasks[taskID] = rec
	return rec
}

func (r *Registry) create(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := uuid.NewString()
	rec := newRecord(id, toolName, toolArgs, ttl, description, metadata, cancel)
	r.tasks[id] = rec
	return rec
}

func newRecord(taskID, toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return &Record{
		TaskID:        taskID,
		ToolName:      toolName,
		ToolArgs:      toolArgs,
		Status:        StatusWorking,
		Description:   description,
		Metadata:      metadata,
		CreatedAt:     now,
		LastUpdatedAt: now,
		TTL:           int64(ttl / time.Millisecond),
		PollInterval:  PollIntervalMs,
		resultCh:      make(chan ToolResult, 1),
		cancel:        cancel,
	}
}

func (r *Registry) Get(taskID string) (*Record, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.tasks[taskID]
	return rec, ok
}

func (r *Registry) List() []*Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Record, 0, len(r.tasks))
	for _, rec := range r.tasks {
		out = append(out, rec)
	}
	return out
}

func (r *Registry) AppendLog(taskID, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusWorking {
		return
	}
	rec.Logs = append(rec.Logs, message)
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (r *Registry) Complete(taskID string, result ToolResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusWorking {
		return
	}
	rec.Status = StatusCompleted
	rec.StatusMessage = "The operation completed successfully."
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rec.ToolResult = &result
	select {
	case rec.resultCh <- result:
	default:
	}
}

func (r *Registry) Fail(taskID string, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusWorking {
		return
	}
	rec.Status = StatusFailed
	rec.StatusMessage = message
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	result := ToolResult{
		Content: []map[string]any{{"type": "text", "text": "Error: " + message}},
		IsError: true,
	}
	rec.ToolResult = &result
	select {
	case rec.resultCh <- result:
	default:
	}
}

func (r *Registry) Cancel(taskID string) (*Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok {
		return nil, false
	}
	if rec.Status != StatusWorking && rec.Status != StatusInputRequired {
		return nil, false
	}
	if rec.cancel != nil {
		rec.cancel()
	}
	rec.Status = StatusCancelled
	rec.StatusMessage = "The task was cancelled by request."
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	result := ToolResult{
		Content: []map[string]any{{"type": "text", "text": "Error: " + rec.StatusMessage}},
		IsError: true,
	}
	rec.ToolResult = &result
	select {
	case rec.resultCh <- result:
	default:
	}
	return rec, true
}

func (r *Registry) ToGetTaskResult(rec *Record) map[string]any {
	out := map[string]any{
		"taskId":        rec.TaskID,
		"status":        rec.Status,
		"createdAt":     rec.CreatedAt,
		"lastUpdatedAt": rec.LastUpdatedAt,
		"ttl":           rec.TTL,
		"pollInterval":  rec.PollInterval,
		"logs":          rec.Logs,
	}
	if rec.Description != "" {
		out["description"] = rec.Description
	}
	if rec.Metadata != nil {
		out["metadata"] = rec.Metadata
	}
	if rec.StatusMessage != "" {
		out["statusMessage"] = rec.StatusMessage
	}
	if rec.ToolResult != nil {
		out["result"] = map[string]any{
			"structuredContent": rec.ToolResult.StructuredContent,
			"content":           rec.ToolResult.Content,
			"isError":           rec.ToolResult.IsError,
		}
	}
	return out
}

var TaskAwareTools = map[string]bool{
	// Generic host commands may legitimately outlive a single MCP request
	// (for example, a caller-declared validation or service lifecycle job).
	// Keep them on the standard task/polling contract instead of coupling
	// their lifetime to the transport request.
	"run_host_command":              true,
	"install_incus_stack":           true,
	"reset_incus_stack":             true,
	"create_vm":                     true,
	"provision_container":           true,
	"provision_vm":                  true,
	"delete_vm":                     true,
	"start_vm":                      true,
	"stop_vm":                       true,
	"restart_vm":                    true,
	"install_k3s":                   true,
	"install_postgresql":            true,
	"reconcile_postgresql_service":  true,
	"remove_postgresql_service":     true,
	"apply_manifest":                true,
	"delete_k8s_resource":           true,
	"put_k8s_secret":                true,
	"install_oci_registry":          true,
	"delete_oci_registry":           true,
	"configure_service_domain":      true,
	"remove_service_domain":         true,
	"install_cloudflared_connector": true,
	"delete_cloudflared_connector":  true,
	"ensure_oci_builder":            true,
	"configure_oci_storage":         true,
	"cleanup_container_storage":     true,
	"build_and_push_oci_image":      true,
	"prepare_host_agent_artifacts":  true,
	"stage_build_context":           true,
	"ensure_host_tool":              true,
	"ensure_host_artifact":          true,
	"remove_host_file":              true,
	"delete_postgresql":             true,
	"create_cloudflare_tunnel":      true,
	"delete_cloudflare_tunnel":      true,
	// Host-local serving reconciliation may need to wait for the service
	// manager and connector process. Keep its lifetime on the standard MCP
	// task contract rather than coupling it to the request transport.
	"ensure_cloudflared_tunnel":   true,
	"configure_k3s_load_balancer": true,
	"configure_k3s_ha_servers":    true,
	"uninstall_k3s":               true,
	"restart_cluster":             true,
	"drain_cluster_nodes":         true,
	"configure_network":           true,
	"remove_vm_network_device":    true,
	"install_cluster_agent":       true,
	"install_host_agent":          true,
	"restart_cluster_agent":       true,
	"install_local_llm_model":     true,
	"configure_local_llm_model":   true,
	"start_local_llm_runtime":     true,
	"configure_local_llm_runtime": true,
	"stop_local_llm_runtime":      true,
	"remove_local_llm_model":      true,
	"run_host_plan":               true,
	"run_runtime_recipe":          true,
	"run_tunnel_recipe":           true,
	"opute.provider.install":      true,
	"opute.provider.reload":       true,
	"opute.provider.teardown":     true,
}
