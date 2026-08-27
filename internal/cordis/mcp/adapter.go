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

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/mcphttp"
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
	client     mcphttp.Client
	tools      map[string]bool
	closed     bool
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
	client := mcphttp.Client{
		Endpoint:   descriptor.Server.Endpoint,
		Token:      options.BearerToken,
		HTTPClient: httpClient,
		Name:       clientName,
		Version:    clientVersion,
	}
	discover, err := client.Call(ctx, "server/discover", "", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("discover provider MCP: %w", err)
	}
	if !supportsModern(discover) {
		return nil, fmt.Errorf("provider MCP negotiated unsupported protocol; require %s", requiredProtocolVersion)
	}
	listed, err := client.Call(ctx, "tools/list", "", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("list provider tools: %w", err)
	}
	tools, err := toolNames(listed)
	if err != nil {
		return nil, err
	}
	if _, ok := tools["opute.provider.get_install_manifest"]; !ok {
		return nil, fmt.Errorf("provider does not expose opute.provider.get_install_manifest")
	}
	return &Adapter{descriptor: descriptor, client: client, tools: tools}, nil
}

func (a *Adapter) Descriptor() providercontract.PluginDescriptor { return a.descriptor }

func (a *Adapter) ToolNames() []string {
	result := make([]string, 0, len(a.tools))
	for name := range a.tools {
		result = append(result, name)
	}
	return result
}

func (a *Adapter) Call(ctx context.Context, operation string, arguments map[string]any) (*sdkmcp.CallToolResult, error) {
	if a == nil {
		return nil, fmt.Errorf("provider adapter is closed")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.closed {
		return nil, fmt.Errorf("provider adapter is closed")
	}
	if _, ok := a.tools[operation]; !ok {
		return nil, fmt.Errorf("provider operation %q is not registered", operation)
	}
	return a.client.CallTool(ctx, operation, arguments)
}

// CallSynchronousOnly is the explicit provider task contract used until a
// Host Agent task bridge owns downstream task creation, polling, and
// cancellation. MCP discovery alone must not make a provider task portable.
func (a *Adapter) CallSynchronousOnly(ctx context.Context, operation string, arguments map[string]any) (*sdkmcp.CallToolResult, error) {
	result, err := a.Call(ctx, operation, arguments)
	if err != nil || result == nil {
		return result, err
	}
	if content, ok := result.StructuredContent.(map[string]any); ok {
		if content["resultType"] == "task" || content["taskId"] != nil {
			return nil, fmt.Errorf("provider operation %q returned a task, but this adapter is synchronous-only", operation)
		}
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
	a.closed = true
	return nil
}

func supportsModern(discover map[string]any) bool {
	versions, _ := discover["supportedVersions"].([]any)
	for _, version := range versions {
		if version == requiredProtocolVersion {
			return true
		}
	}
	return false
}

func toolNames(listed map[string]any) (map[string]bool, error) {
	raw, _ := listed["tools"].([]any)
	byName := make(map[string]bool, len(raw))
	for _, item := range raw {
		tool, _ := item.(map[string]any)
		name, _ := tool["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("provider returned an invalid tool definition")
		}
		if byName[name] {
			return nil, fmt.Errorf("provider returned duplicate tool %q", name)
		}
		byName[name] = true
	}
	return byName, nil
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
