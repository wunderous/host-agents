package app

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/wunderous/host-agents/internal/authz"
	"github.com/wunderous/host-agents/internal/config"
	"github.com/wunderous/host-agents/internal/fingerprint"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/transport"
)

// Run starts the host agent and blocks until shutdown.
func Run(ctx context.Context, logger *slog.Logger) error {
	runtime, err := NewRuntime(logger)
	if err != nil {
		return err
	}
	defer runtime.Close()
	cfg := runtime.cfg
	logger = runtime.logger
	hostServer := runtime.host

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	authzDir := cfg.StandaloneStateDir
	if authzDir == "" {
		authzDir = cfg.InstanceRoot
	}
	authorizer, err := authz.Open(authz.Options{
		StateDir:       authzDir,
		BootstrapToken: cfg.BootstrapToken(),
		OputeSecret:    cfg.OputeClientSecret,
	})
	if err != nil {
		return err
	}
	defer authorizer.Close()

	httpSrv := transport.NewHTTPServer(transport.HTTPOptions{
		HostServer:                  hostServer,
		BindHost:                    cfg.HostMCPBindHost,
		Port:                        cfg.HostMCPPort,
		Authz:                       authorizer,
		InstanceID:                  cfg.InstanceID,
		AgentID:                     cfg.RemoteAgentID,
		PhysicalFingerprint:         cfg.PhysicalFingerprint,
		FingerprintVersion:          cfg.FingerprintVersion,
		FingerprintSource:           cfg.FingerprintSource,
		ExecutionContextID:          cfg.ExecutionContextID,
		ExecutionContextKind:        cfg.ExecutionContextKind,
		ExecutionContextDisplayName: cfg.ExecutionContextDisplayName,
		AllowLegacyHandshake:        cfg.AllowLegacyHandshake,
		HealthObserver: func() map[string]any {
			capacity, err := runtime.svc.Incus().VMInventoryCapacity()
			capabilities := fingerprint.DetectCapabilities()
			result := map[string]any{
				"capabilities": map[string]any{
					"host": capabilities,
				},
			}
			if err == nil {
				result["runningVmCount"] = capacity.RunningVMCount
				result["totalVmCount"] = capacity.TotalVMCount
			}
			return result
		},
		Logger: logger,
	})
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Start() }()
	select {
	case <-ctx.Done():
		_ = httpSrv.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		return err
	}
}

// Check validates startup configuration without opening a listener or
// emitting MCP protocol data. It is intended for installers and client setup
// diagnostics.
func Check() error {
	cfg := config.Load()
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if cfg.AgentMode == "standalone" {
		store, err := state.Open(cfg.StandaloneStateDir)
		if err != nil {
			return err
		}
		return store.Close()
	}
	return nil
}

func validateConfig(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return hostruntime.RequireSupportedPlatform(hostruntime.ID(cfg.ProviderID))
}
