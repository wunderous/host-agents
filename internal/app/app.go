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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opute-io/host-agents/internal/config"
	"github.com/opute-io/host-agents/internal/fingerprint"
	"github.com/opute-io/host-agents/internal/heartbeat"
	"github.com/opute-io/host-agents/internal/hostmcp"
	"github.com/opute-io/host-agents/internal/ops"
	"github.com/opute-io/host-agents/internal/provider"
	"github.com/opute-io/host-agents/internal/tools"
	"github.com/opute-io/host-agents/internal/transport"
)

// Run starts the host agent and blocks until shutdown.
func Run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.Load()
	if err := provider.RequireSupportedPlatform(provider.ID(cfg.ProviderID)); err != nil {
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
		ProviderID:             provider.NormalizeProviderID(cfg.ProviderID),
		AllowInsecureDownloads: cfg.AgentMode == "standalone" && cfg.StandaloneAllowInsecureDownloads,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})

	hostServer, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID:     cfg.ProviderID,
		Ops:            svc,
		Logger:         logger,
		Standalone:     cfg.AgentMode == "standalone",
		AllowMutations: cfg.StandaloneAllowMutations,
		StateDir:       cfg.StandaloneStateDir,
	})
	if err != nil {
		return err
	}

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
				running, total, err := svc.VMInventoryStats()
				if err != nil {
					return heartbeat.HostVMStats{}, err
				}
				return heartbeat.HostVMStats{
					RunningVMCount: running,
					TotalVMCount:   total,
				}, nil
			}
			hb = heartbeat.Start(heartbeat.Options{
				AgentID:              cfg.RemoteAgentID,
				MCPURL:               cfg.MCPURL,
				BridgeToken:          cfg.BridgeToken,
				RemoteAgentAuthToken: cfg.RemoteAgentAuthToken,
				OnboardingToken:      cfg.OnboardingToken,
				OnboardingSessionID:  cfg.OnboardingSessionID,
				EnvFile:              cfg.EnvFile,
				HostMCPEndpoint:      endpointFor(cfg),
				HostName:             hostNameFor(cfg),
				AgentVersion:         "go-host-agent/1.0.0",
				ProviderID:           cfg.ProviderID,
				Fingerprint:          fp,
				TestMode:             cfg.TestMode,
				Logger:               logger,
				CollectVMStats:       collectVMStats,
				HostCapabilities:     toolNames,
			})
		}
	}

	if cfg.AgentMode == "standalone" && cfg.TransportMode == "stdio" {
		logger.Info("standalone stdio mode enabled")
		return hostServer.MCP().Run(ctx, &mcp.StdioTransport{})
	}

	if cfg.IsReverseTunnel {
		logger.Info("reverse tunnel mode enabled", "agentId", cfg.RemoteAgentID)
		healthSrv := transport.NewHealthOnlyServer(cfg.HostMCPBindHost, cfg.HostMCPPort, logger)
		go func() {
			if err := healthSrv.Start(); err != nil && err != http.ErrServerClosed {
				logger.Warn("health listener stopped", "err", err)
			}
		}()
		go transport.RunReverseTunnelLoop(ctx, hostServer, cfg.HostWSURL, cfg.RemoteAgentID, cfg.RemoteAgentAuthToken, cfg.MCPHealthURL, logger)
		<-ctx.Done()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		_ = healthSrv.Shutdown(shutdownCtx)
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
