// Package hostagent is the composition root. It owns no operations: it holds
// the shared runtime seam, constructs each domain with the cross-domain seams
// that domain declared, and hands the domains out. Callers reach an operation
// through the domain that owns it -- s.Incus().StartVM, s.Kubernetes().ListPods
// -- so adding an operation never touches this package.
package hostagent

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/wunderous/host-agents/internal/domain/cluster"
	"github.com/wunderous/host-agents/internal/domain/incus"
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/domain/llm"
	"github.com/wunderous/host-agents/internal/domain/oci"
	"github.com/wunderous/host-agents/internal/domain/postgres"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resource"
)

// Service is the composition root's handle. One per host agent process.
type Service struct {
	// shared carries the identity, configuration, and execution handles that
	// every domain needs and none owns (plan sec. 9.2). Domain packages take a
	// `*hostruntime.Shared` directly; it is the one piece of state this package
	// owns.
	shared               hostruntime.Shared
	toolsFn              func(providerID string) []string
	resetCheckpointPath  string
	ociStoragePolicyPath string
	sqliteDatabaseRoot   string

	// oci is the oci domain, built lazily -- built in oci_domain.go. It holds the
	// container storage policy lock, so it is one instance per service.
	ociSvc  *oci.Service
	ociOnce sync.Once

	// cluster is the cluster domain, built lazily -- built in cluster_domain.go. It
	// owns the guest bridge relay listener, so it is one instance per service.
	clusterSvc  *cluster.Service
	clusterOnce sync.Once
	// incus is the incus domain, built lazily -- built in incus_domain.go.
	incusSvc  *incus.Service
	incusOnce sync.Once
	// llm is the llm domain, built lazily -- built in llm_domain.go. It owns live
	// relay listeners, so it is one instance per service.
	llmSvc    *llm.Service
	llmOnce   sync.Once
	relayDirs [2]string
	// postgres is the postgres domain, built lazily -- built in postgres_domain.go.
	// It owns live relay listeners, so it is one instance per service.
	postgresSvc            *postgres.Service
	postgresOnce           sync.Once
	postgresRelayConfigDir string
	resourceSvc            resource.HostResourceService
	// k8s is the kubernetes domain, built lazily -- built in kubernetes_domain.go.
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
	ResourceService           resource.HostResourceService
}

func New(opts Options) *Service {
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
	return &Service{
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
		resourceSvc:            opts.ResourceService,
	}
}

func (s *Service) TenantID() string {
	if s == nil {
		return ""
	}
	return s.shared.TenantID
}

func (s *Service) AgentID() string {
	if s == nil {
		return ""
	}
	return s.shared.AgentID
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

func (s *Service) ReadProviderID() string {
	return string(s.shared.Runtime.ReadProviderID())
}

// SetResourceSnapshot connects host-local admission telemetry to direct
// diagnostics such as get_host_info and get_local_status.
func (s *Service) SetResourceSnapshot(snapshot func() map[string]any) {
	s.shared.ResourceSnapshot = snapshot
}

// ResourceService returns the one host-wide typed capacity service mounted by
// the runtime. Domains and provider callbacks reuse its reservation context;
// they do not create another coordinator.
func (s *Service) ResourceService() resource.HostResourceService {
	if s == nil {
		return nil
	}
	return s.resourceSvc
}

func (s *Service) SetResourceService(service resource.HostResourceService) {
	if s != nil {
		s.resourceSvc = service
	}
}
