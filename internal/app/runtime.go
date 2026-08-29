package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wunderous/host-agents/internal/config"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/version"
)

// Runtime is the shared host execution runtime. It owns the typed MCP server
// and the local operation services, but does not choose a transport. The same
// runtime can therefore be exposed over HTTP or connected to the TUI through
// an in-memory MCP transport.
type Runtime struct {
	cfg       config.Config
	logger    *slog.Logger
	toolNames []string
	svc       *hostagent.Service
	admission *resource.Coordinator
	host      *hostmcp.Server
}

// NewRuntime builds the host executor without opening a listener or contacting
// the Platform control plane. Call Serve for the server process, or use Host
// with an in-process MCP transport for the standalone TUI.
func NewRuntime(logger *slog.Logger) (*Runtime, error) {
	cfg := config.Load()
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	toolNames, svc, admission, host, err := buildHostRuntime(cfg, logger)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		cfg:       cfg,
		logger:    logger,
		toolNames: toolNames,
		svc:       svc,
		admission: admission,
		host:      host,
	}, nil
}

func buildHostRuntime(cfg config.Config, logger *slog.Logger) ([]string, *hostagent.Service, *resource.Coordinator, *hostmcp.Server, error) {
	toolNames, err := tools.HostToolNamesForProvider(cfg.ProviderID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	svc := hostagent.New(hostagent.Options{
		ProviderID:                hostruntime.NormalizeProviderID(cfg.ProviderID),
		TenantID:                  cfg.TenantID,
		InstanceID:                cfg.InstanceID,
		AgentID:                   cfg.RemoteAgentID,
		OwnershipMode:             cfg.OwnershipMode,
		RelayConfigDir:            cfg.RelayConfigDir,
		SharedHostResourceLockDir: cfg.HostResourceLockDir,
		OciStoragePolicyPath:      filepath.Join(cfg.HostResourceLockDir, "oci-storage-policy.json"),
		SQLiteDatabaseRoot:        cfg.SQLiteDatabaseRoot,
		SharedHostOwnerInstance:   cfg.SharedHostOwnerInstance,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	admission, err := resource.NewCoordinator(resource.Config{
		LockDir:                 cfg.HostResourceLockDir,
		MaxNormal:               cfg.HostResourceMaxNormal,
		MaxHeavy:                cfg.HostResourceMaxHeavy,
		MaxQueued:               cfg.HostResourceMaxQueued,
		MinAvailableMemoryBytes: cfg.HostResourceMinMemoryBytes,
		MinAvailableDiskBytes:   cfg.HostResourceMinDiskBytes,
		DiskPaths:               cfg.HostResourceDiskPaths,
		PolicyRevision:          cfg.HostResourcePolicyRevision,
		FailClosedOnUnknown:     cfg.HostResourceFailClosed,
		CPUCapacityCores:        cfg.HostResourceCPUCapacity,
		MemoryCapacityBytes:     cfg.HostResourceMemoryCapacity,
		DiskCapacityBytes:       cfg.HostResourceDiskCapacity,
		TaskCapacity:            cfg.HostResourceTaskCapacity,
		TenantID:                cfg.TenantID,
		ReconcilePolicy: func(ctx context.Context, target resourceid.URI) error {
			return svc.Host().ReconcileHostResourcePolicy(ctx, target)
		},
		EnforcementProbe: svc.Host().ObserveHostResourceEnforcement,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	svc.SetResourceSnapshot(admission.Metadata)
	svc.SetResourceService(admission)

	host, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID:     cfg.ProviderID,
		Ops:            svc,
		Logger:         logger,
		Standalone:     cfg.AgentMode == "standalone",
		AllowMutations: cfg.StandaloneAllowMutations,
		StateDir:       cfg.StandaloneStateDir,
		Version:        version.Version,
		Admission:      admission,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return toolNames, svc, admission, host, nil
}

// Host returns the typed MCP server backing this runtime.
func (r *Runtime) Host() *hostmcp.Server {
	if r == nil {
		return nil
	}
	return r.host
}

// Config returns the resolved runtime configuration.
func (r *Runtime) Config() config.Config {
	if r == nil {
		return config.Config{}
	}
	return r.cfg
}

// Close stops host-owned durable work and releases state resources. Transport
// shutdown remains owned by the caller that started that transport.
func (r *Runtime) Close() error {
	if r == nil || r.host == nil {
		return nil
	}
	return r.host.Close()
}
