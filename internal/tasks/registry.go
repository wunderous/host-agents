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

func (r *Registry) create(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.NewString()
	rec := &Record{
		TaskID:        id,
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
	r.tasks[id] = rec
	return rec
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
	"create_vm":                   true,
	"provision_vm":                true,
	"delete_vm":                   true,
	"start_vm":                    true,
	"stop_vm":                     true,
	"restart_vm":                  true,
	"install_k3s":                 true,
	"install_postgresql":          true,
	"delete_postgresql":           true,
	"create_cloudflare_tunnel":    true,
	"delete_cloudflare_tunnel":    true,
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
}
