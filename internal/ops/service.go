package ops

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/domain/cluster"
	"github.com/wunderous/host-agents/internal/domain/incus"
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/domain/llm"
	"github.com/wunderous/host-agents/internal/domain/oci"
	"github.com/wunderous/host-agents/internal/domain/postgres"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// HostOperationsService implements host MCP operations against Incus on Linux.
type HostOperationsService struct {
	// shared carries the identity, configuration, and execution handles that
	// every domain needs and none owns (plan sec. 9.2). Domain packages take a
	// `*hostruntime.Shared` directly; this service delegates to it so the move
	// can happen one domain at a time.
	shared               hostruntime.Shared
	toolsFn              func(providerID string) []string
	resetCheckpointPath  string
	ociStoragePolicyPath string
	sqliteDatabaseRoot   string

	// oci is the oci domain, built lazily -- see oci_delegate.go. It holds the
	// container storage policy lock, so it is one instance per service.
	ociSvc  *oci.Service
	ociOnce sync.Once

	// cluster is the cluster domain, built lazily -- see cluster_delegate.go. It
	// owns the guest bridge relay listener, so it is one instance per service.
	clusterSvc  *cluster.Service
	clusterOnce sync.Once
	// incus is the incus domain, built lazily -- see incus_delegate.go.
	incusSvc  *incus.Service
	incusOnce sync.Once
	// llm is the llm domain, built lazily -- see llm_delegate.go. It owns live
	// relay listeners, so it is one instance per service.
	llmSvc    *llm.Service
	llmOnce   sync.Once
	relayDirs [2]string
	// postgres is the postgres domain, built lazily -- see postgres_delegate.go.
	// It owns live relay listeners, so it is one instance per service.
	postgresSvc            *postgres.Service
	postgresOnce           sync.Once
	postgresRelayConfigDir string
	// container command seams keep runtime adapter tests independent of an
	// installed host runtime. They are intentionally scoped to this service.
	containerCommandFn          func(context.Context, string, ...string) ([]byte, error)
	containerStreamingCommandFn func(context.Context, string, []string, func(string)) error
	// k8s is the kubernetes domain, built lazily -- see kubernetes_delegate.go.
	k8s     *kubernetes.Service
	k8sOnce sync.Once
}

type Options struct {
	ProviderID                hostruntime.ID
	ToolsForProvider          func(providerID string) []string
	InstanceID                string
	AgentID                   string
	OwnershipMode             string
	RelayConfigDir            string
	SharedHostResourceLockDir string
	ResetCheckpointPath       string
	OciStoragePolicyPath      string
	SQLiteDatabaseRoot        string
	SharedHostOwnerInstance   string
	TenantID                  string
	ResourceRegistry          ResourceRegistry
}

func NewHostOperationsService(opts Options) *HostOperationsService {
	cfg := hostruntime.ResolveConfig(opts.ProviderID)
	rt := hostruntime.NewRuntime(cfg)
	toolsFn := opts.ToolsForProvider
	if toolsFn == nil {
		toolsFn = func(string) []string { return nil }
	}
	ownershipMode := strings.TrimSpace(opts.OwnershipMode)
	if ownershipMode != "enforce" {
		ownershipMode = "audit"
	}
	postgresRelayConfigDir := strings.TrimSpace(opts.RelayConfigDir)
	if postgresRelayConfigDir != "" {
		postgresRelayConfigDir = filepath.Join(postgresRelayConfigDir, "postgresql-service-relays")
	}
	tenantID := strings.TrimSpace(opts.TenantID)
	if tenantID == "" {
		tenantID = "local"
	}
	registry := opts.ResourceRegistry
	if registry == nil {
		registry = hostruntime.NewInMemoryResourceRegistry()
	}
	return &HostOperationsService{
		shared: hostruntime.Shared{
			Runtime:                 rt,
			TenantID:                tenantID,
			ResourceRegistry:        registry,
			InstanceID:              strings.TrimSpace(opts.InstanceID),
			AgentID:                 strings.TrimSpace(opts.AgentID),
			OwnershipMode:           ownershipMode,
			SharedHostOwnerInstance: strings.TrimSpace(opts.SharedHostOwnerInstance),
		},
		toolsFn:                toolsFn,
		resetCheckpointPath:    resolveResetCheckpointPath(opts.ResetCheckpointPath, opts.RelayConfigDir),
		ociStoragePolicyPath:   strings.TrimSpace(opts.OciStoragePolicyPath),
		sqliteDatabaseRoot:     strings.TrimSpace(opts.SQLiteDatabaseRoot),
		relayDirs:              [2]string{opts.RelayConfigDir, opts.SharedHostResourceLockDir},
		postgresRelayConfigDir: postgresRelayConfigDir,
	}
}

func (s *HostOperationsService) TenantID() string {
	if s == nil {
		return ""
	}
	return s.shared.TenantID
}

func (s *HostOperationsService) effectiveTenantID() string {
	if s == nil {
		return "local"
	}
	return s.shared.EffectiveTenantID()
}

func resolveResetCheckpointPath(explicitPath, relayConfigDir string) string {
	if path := strings.TrimSpace(explicitPath); path != "" {
		return path
	}
	if dir := strings.TrimSpace(relayConfigDir); dir != "" {
		return filepath.Join(dir, "incus-reset-checkpoint.json")
	}
	return ""
}

func (s *HostOperationsService) ReadProviderID() string {
	return string(s.shared.Runtime.ReadProviderID())
}

// SetResourceSnapshot connects host-local admission telemetry to direct
// diagnostics such as get_host_info and get_local_status.
func (s *HostOperationsService) SetResourceSnapshot(snapshot func() map[string]any) {
	s.shared.ResourceSnapshot = snapshot
}

// These forward to internal/textutil, which every domain shares. The local
// spellings stay so the ~76 call sites in this package do not churn while it is
// being dismantled.
var ()

// VMInfo lives in the vminfo contract package: incus produces it and cluster
// and host read it. The alias keeps the dispatch layer unchanged.
type VMInfo = vminfo.VMInfo
