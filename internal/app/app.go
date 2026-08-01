package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wunderous/host-agents/internal/config"
	"github.com/wunderous/host-agents/internal/fingerprint"
	"github.com/wunderous/host-agents/internal/heartbeat"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/transport"
	"github.com/wunderous/host-agents/internal/version"
)

// Run starts the host agent and blocks until shutdown.
func Run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.Load()
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	toolNames, err := tools.HostToolNamesForProvider(cfg.ProviderID)
	if err != nil {
		return err
	}

	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID:              provider.NormalizeProviderID(cfg.ProviderID),
		InstanceID:              cfg.InstanceID,
		AgentID:                 cfg.RemoteAgentID,
		OwnershipMode:           cfg.OwnershipMode,
		RelayConfigDir:          cfg.RelayConfigDir,
		SharedHostOwnerInstance: cfg.SharedHostOwnerInstance,
		AllowInsecureDownloads:  cfg.AgentMode == "standalone" && cfg.StandaloneAllowInsecureDownloads,
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
	})
	if err != nil {
		return err
	}
	svc.SetResourceSnapshot(admission.Metadata)

	hostServer, err := hostmcp.NewServer(hostmcp.Options{
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
		return err
	}
	defer hostServer.Close()

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var hb *heartbeat.Service
	if cfg.AgentMode != "standalone" && cfg.MCPURL != "" && cfg.BridgeToken != "" {
		fp, err := fingerprint.ReadIdentity()
		if err != nil {
			logger.Warn("fingerprint unavailable", "err", err)
		} else {
			if cfg.TestMode {
				fp.Fingerprint += ":test"
			}
			collectVMStats := func() (heartbeat.HostVMStats, error) {
				capacity, err := svc.VMInventoryCapacity()
				if err != nil {
					return heartbeat.HostVMStats{}, err
				}
				return heartbeat.HostVMStats{
					RunningVMCount:            capacity.RunningVMCount,
					TotalVMCount:              capacity.TotalVMCount,
					RunningVMCPULimitCores:    capacity.RunningVMCPULimitCores,
					TotalVMCPULimitCores:      capacity.TotalVMCPULimitCores,
					RunningVMMemoryLimitBytes: capacity.RunningVMMemoryLimitBytes,
					TotalVMMemoryLimitBytes:   capacity.TotalVMMemoryLimitBytes,
					RunningVMDiskLimitBytes:   capacity.RunningVMDiskLimitBytes,
					TotalVMDiskLimitBytes:     capacity.TotalVMDiskLimitBytes,
					RunningQEMUCount:          capacity.RunningQEMUCount,
					TotalQEMUCount:            capacity.TotalQEMUCount,
					RunningContainerCount:     capacity.RunningContainerCount,
					TotalContainerCount:       capacity.TotalContainerCount,
				}, nil
			}
			hostCapabilities := append([]string(nil), toolNames...)
			seenLLM := false
			for _, name := range hostCapabilities {
				if name == "llm.ollama" {
					seenLLM = true
					break
				}
			}
			if !seenLLM {
				hostCapabilities = append(hostCapabilities, "llm.ollama")
			}
			// Advertise the host-local runtime envelope during registration and
			// heartbeat so the control plane can reject unsupported work before
			// queueing a mutation. This is deliberately capability metadata only;
			// installation and model state remain owned by host operations.
			var capabilitySummary map[string]any
			if prereqs, prereqErr := svc.CheckLocalLLMPrerequisites(); prereqErr == nil && prereqs != nil {
				capabilitySummary = map[string]any{
					"llm": map[string]any{
						"ollama": map[string]any{
							"supported":            prereqs.Supported,
							"apiBaseUrl":           ops.OllamaLoopbackURL(0),
							"architectures":        []string{prereqs.Architecture},
							"readyForInstall":      prereqs.ReadyForInstall,
							"readyForGpuInference": prereqs.ReadyForGpuInference,
							"gpu": map[string]any{
								"available":          prereqs.NvidiaSmiOk,
								"vendor":             strings.TrimSpace(prereqs.GPU),
								"nvidiaSmiOk":        prereqs.NvidiaSmiOk,
								"cudaLibraryPresent": prereqs.CudaLibraryPresent,
								"dxgDevicePresent":   prereqs.DxgDevicePresent,
								"runtimeAccelerated": prereqs.RuntimeGpuAccelerated,
							},
							"blockers":         prereqs.Blockers,
							"remediationHints": prereqs.RemediationHints,
						},
					},
				}
			}
			hb = heartbeat.Start(heartbeat.Options{
				AgentID:              cfg.RemoteAgentID,
				InstanceID:           cfg.InstanceID,
				MCPURL:               cfg.MCPURL,
				BridgeToken:          cfg.BridgeToken,
				RemoteAgentAuthToken: cfg.RemoteAgentAuthToken,
				OnboardingToken:      cfg.OnboardingToken,
				OnboardingSessionID:  cfg.OnboardingSessionID,
				EnvFile:              cfg.EnvFile,
				HostMCPEndpoint:      endpointFor(cfg),
				HostName:             hostNameFor(cfg),
				AgentVersion:         "go-host-agent/" + version.Version,
				ProviderID:           cfg.ProviderID,
				Fingerprint:          fp,
				TestMode:             cfg.TestMode,
				Logger:               logger,
				CollectVMStats:       collectVMStats,
				HostCapabilities:     hostCapabilities,
				CapabilitySummary:    capabilitySummary,
				ResourceSnapshot:     admission.Metadata,
			})
		}
	}

	if cfg.IsReverseTunnel {
		logger.Info("reverse tunnel mode enabled", "agentId", cfg.RemoteAgentID, "mode", cfg.AgentMode)
		var healthSrv *transport.HealthOnlyServer
		if cfg.HostMCPPort > 0 {
			healthSrv = transport.NewHealthOnlyServer(cfg.HostMCPBindHost, cfg.HostMCPPort, logger, cfg.InstanceID, cfg.RemoteAgentID)
			go func() {
				if err := healthSrv.Start(); err != nil && err != http.ErrServerClosed {
					logger.Warn("health listener stopped", "err", err)
				}
			}()
		} else {
			logger.Info("reverse-tunnel health listener disabled", "instanceId", cfg.InstanceID, "agentId", cfg.RemoteAgentID)
		}
		go transport.RunHostWorkerLoop(ctx, hostServer, cfg.HostWSURL, cfg.RemoteAgentID, cfg.RemoteAgentAuthToken, cfg.MCPHealthURL, logger)
		go transport.RunReverseTunnelLoop(ctx, hostServer, cfg.HostWSURL, cfg.RemoteAgentID, cfg.RemoteAgentAuthToken, cfg.MCPHealthURL, logger)
		<-ctx.Done()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		if healthSrv != nil {
			_ = healthSrv.Shutdown(shutdownCtx)
		}
		cancelShutdown()
		if hb != nil {
			hb.Stop()
		}
		return nil
	}

	httpSrv := transport.NewHTTPServer(transport.HTTPOptions{
		HostServer: hostServer,
		BindHost:   cfg.HostMCPBindHost,
		Port:       cfg.HostMCPPort,
		AuthTokens: cfg.AllowedAuthTokens(),
		InstanceID: cfg.StandaloneInstanceID,
		Logger:     logger,
	})
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Start() }()
	select {
	case <-ctx.Done():
		_ = httpSrv.Shutdown(context.Background())
		if hb != nil {
			hb.Stop()
		}
		return nil
	case err := <-errCh:
		if hb != nil {
			hb.Stop()
		}
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
	return provider.RequireSupportedPlatform(provider.ID(cfg.ProviderID))
}

func endpointFor(cfg config.Config) string {
	if cfg.IsReverseTunnel {
		return "tunnel://mcp-host"
	}
	return "http://" + cfg.HostMCPBindHost + ":" + itoa(cfg.HostMCPPort) + "/mcp"
}

func hostNameFor(cfg config.Config) string {
	if name, err := os.Hostname(); err == nil {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			return trimmed
		}
	}
	return cfg.RemoteAgentID
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
