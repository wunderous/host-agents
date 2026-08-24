// Package mcp contains the transport-specific edge of the Cordis provider
// architecture. The parent cordis package never imports MCP.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

const requiredProtocolVersion = "2026-07-28"

type Options struct {
	HTTPClient    *http.Client
	BearerToken   string
	ClientName    string
	ClientVersion string
}

type Adapter struct {
	mu         sync.RWMutex
	descriptor providercontract.PluginDescriptor
	session    *mcp.ClientSession
	tools      map[string]*mcp.Tool
}

func Connect(ctx context.Context, descriptor providercontract.PluginDescriptor, options Options) (*Adapter, error) {
	if err := providercontract.ValidateDescriptor(descriptor); err != nil {
		return nil, err
	}
	clientName := strings.TrimSpace(options.ClientName)
	if clientName == "" {
		clientName = "opute-host-agent"
	}
	clientVersion := strings.TrimSpace(options.ClientVersion)
	if clientVersion == "" {
		clientVersion = "dev"
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Transport: authTransport{base: http.DefaultTransport, token: options.BearerToken}}
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             descriptor.Server.Endpoint,
		HTTPClient:           httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect provider MCP: %w", err)
	}
	initialize := session.InitializeResult()
	if initialize == nil || initialize.ProtocolVersion != requiredProtocolVersion {
		_ = session.Close()
		version := "unknown"
		if initialize != nil {
			version = initialize.ProtocolVersion
		}
		return nil, fmt.Errorf("provider MCP negotiated unsupported protocol %q; require %s", version, requiredProtocolVersion)
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list provider tools: %w", err)
	}
	byName := make(map[string]*mcp.Tool, len(list.Tools))
	for _, tool := range list.Tools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			_ = session.Close()
			return nil, fmt.Errorf("provider returned an invalid tool definition")
		}
		if _, exists := byName[tool.Name]; exists {
			_ = session.Close()
			return nil, fmt.Errorf("provider returned duplicate tool %q", tool.Name)
		}
		byName[tool.Name] = tool
	}
	if _, ok := byName["opute.provider.get_install_manifest"]; !ok {
		_ = session.Close()
		return nil, fmt.Errorf("provider does not expose opute.provider.get_install_manifest")
	}
	return &Adapter{descriptor: descriptor, session: session, tools: byName}, nil
}

func (a *Adapter) Descriptor() providercontract.PluginDescriptor { return a.descriptor }

func (a *Adapter) ToolNames() []string {
	result := make([]string, 0, len(a.tools))
	for name := range a.tools {
		result = append(result, name)
	}
	return result
}

func (a *Adapter) Call(ctx context.Context, operation string, arguments map[string]any) (*mcp.CallToolResult, error) {
	if a == nil {
		return nil, fmt.Errorf("provider adapter is closed")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.session == nil {
		return nil, fmt.Errorf("provider adapter is closed")
	}
	if _, ok := a.tools[operation]; !ok {
		return nil, fmt.Errorf("provider operation %q is not registered", operation)
	}
	result, err := a.session.CallTool(ctx, &mcp.CallToolParams{Name: operation, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *Adapter) InstallManifest(ctx context.Context) (providercontract.InstallManifest, error) {
	result, err := a.Call(ctx, "opute.provider.get_install_manifest", nil)
	if err != nil {
		return providercontract.InstallManifest{}, err
	}
	if result == nil || result.IsError || result.StructuredContent == nil {
		return providercontract.InstallManifest{}, fmt.Errorf("provider install manifest call failed")
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return providercontract.InstallManifest{}, fmt.Errorf("encode provider install manifest: %w", err)
	}
	var manifest providercontract.InstallManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return providercontract.InstallManifest{}, fmt.Errorf("decode provider install manifest: %w", err)
	}
	expected := providercontract.ProviderRef{ID: a.descriptor.PluginID, Version: a.descriptor.Version}
	if err := providercontract.ValidateInstallManifest(manifest, expected); err != nil {
		return providercontract.InstallManifest{}, err
	}
	return manifest, nil
}

func (a *Adapter) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return nil
	}
	err := a.session.Close()
	a.session = nil
	return err
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t authTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := request.Clone(request.Context())
	if strings.TrimSpace(t.token) != "" {
		clone.Header.Set("Authorization", "Bearer "+t.token)
	}
	return base.RoundTrip(clone)
}
