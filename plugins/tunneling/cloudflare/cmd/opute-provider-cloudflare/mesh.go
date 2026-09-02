package main

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

type meshBinding struct {
	NodeID      string
	NodeName    string
	NodeToken   string
	InstanceURI string
}

var meshBindings = struct {
	sync.Mutex
	items map[string]meshBinding
}{items: make(map[string]meshBinding)}

type meshNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
	IP     string `json:"ip,omitempty"`
	Token  string `json:"token,omitempty"`
}

type meshHAEndpoint struct {
	TunnelID    string
	DNSRecordID string
	Hostname    string
	TunnelName  string
	ServiceName string
	TargetURIs  []string
}

var meshHAEndpoints = struct {
	sync.Mutex
	items map[string]meshHAEndpoint
}{items: make(map[string]meshHAEndpoint)}

const (
	cloudflaredArtifactURL = "https://github.com/cloudflare/cloudflared/releases/download/2026.8.2/cloudflared-linux-amd64"
	cloudflaredArtifactSHA = "fcfb02b575a52ca1af2e3267af4e1517bcdeb30ac48c834c69abaed3c0576ad2"
	maxCloudflaredArtifact = 128 << 20
	cloudflaredChunkChars  = 2 << 20
)

var cloudflaredArtifactCache struct {
	sync.Mutex
	encoded string
}

func networkOverlayOperations() []providercontract.Operation {
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input, map[string]any{"type": "object"}, []string{"host", "network"}, meshTargetBinding())
	}
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input, map[string]any{"type": "object"}, []string{"host", "network"}, meshTargetBinding())
	}
	destructive := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "destructive", input, map[string]any{"type": "object"}, []string{"host", "network"}, meshTargetBinding())
	}
	return []providercontract.Operation{
		read(capabilitycontract.NetworkOverlayValidateOperation, map[string]any{
			"type":       "object",
			"required":   []string{"targetUri"},
			"properties": map[string]any{"targetUri": targetURISchema()},
		}),
		mutation(capabilitycontract.NetworkOverlayPrepareMembershipOperation, map[string]any{
			"type":     "object",
			"required": []string{"targetUri", "name"},
			"properties": map[string]any{
				"targetUri": targetURISchema(),
				"name":      map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$"},
			},
		}),
		mutation(capabilitycontract.NetworkOverlayAttachTargetOperation, map[string]any{
			"type":     "object",
			"required": []string{"membershipRef", "targetUri"},
			"properties": map[string]any{
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"targetUri":     targetURISchema(),
			},
		}),
		read(capabilitycontract.NetworkOverlayProbeReachabilityOperation, map[string]any{
			"type":     "object",
			"required": []string{"targetUri", "peerMeshIp"},
			"properties": map[string]any{
				"targetUri":  targetURISchema(),
				"peerMeshIp": map[string]any{"type": "string", "format": "ipv4"},
			},
		}),
		mutation(capabilitycontract.NetworkOverlayEnsureHAEndpointOperation, map[string]any{
			"type":     "object",
			"required": []string{"targetUri", "peerTargetUris", "hostname", "tunnelName"},
			"properties": map[string]any{
				"targetUri":      targetURISchema(),
				"peerTargetUris": map[string]any{"type": "array", "minItems": 1, "items": targetURISchema()},
				"hostname":       map[string]any{"type": "string", "pattern": "^[a-z0-9][a-z0-9.-]{0,252}$"},
				"tunnelName":     map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$"},
			},
		}),
		providerOperation(capabilitycontract.NetworkOverlayRemoveHAEndpointOperation, "destructive", map[string]any{
			"type":     "object",
			"required": []string{"endpointRef"},
			"properties": map[string]any{
				"endpointRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
			},
		}, map[string]any{"type": "object"}, []string{"host", "network"}, nil),
		destructive(capabilitycontract.NetworkOverlayRemoveMembershipOperation, map[string]any{
			"type":     "object",
			"required": []string{"membershipRef", "targetUri"},
			"properties": map[string]any{
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"targetUri":     targetURISchema(),
			},
		}),
	}
}

func meshTargetBinding() []providercontract.ResourceBinding {
	return []providercontract.ResourceBinding{{Argument: "targetUri", ResourceType: resourceid.TypeVM, Required: true}}
}

func targetURISchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^vm:[a-z][a-z0-9-]{0,31}:.+$"}
}

func dispatchNetworkOverlayOperation(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	switch operation {
	case capabilitycontract.NetworkOverlayValidateOperation:
		return validateNetworkOverlay(args)
	case capabilitycontract.NetworkOverlayPrepareMembershipOperation:
		return prepareNetworkOverlay(ctx, args)
	case capabilitycontract.NetworkOverlayAttachTargetOperation:
		return attachNetworkOverlay(ctx, args)
	case capabilitycontract.NetworkOverlayProbeReachabilityOperation:
		return probeNetworkOverlay(ctx, args)
	case capabilitycontract.NetworkOverlayEnsureHAEndpointOperation:
		return ensureNetworkOverlayHAEndpoint(ctx, args)
	case capabilitycontract.NetworkOverlayRemoveHAEndpointOperation:
		return removeNetworkOverlayHAEndpoint(ctx, args)
	case capabilitycontract.NetworkOverlayRemoveMembershipOperation:
		return removeNetworkOverlay(ctx, args)
	default:
		return nil, fmt.Errorf("unknown Cloudflare network overlay operation %q", operation)
	}
}

func validateNetworkOverlay(args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	accountID, _, apiToken, err := cloudflareCreds()
	if err != nil {
		return nil, err
	}
	var nodes []meshNode
	if err := cloudflareJSON(context.Background(), apiToken, http.MethodGet, fmt.Sprintf("%s/accounts/%s/warp_connector", cloudflareAPIBase(), accountID), nil, &nodes); err != nil {
		return nil, fmt.Errorf("list Cloudflare Mesh nodes: %w", err)
	}
	summaries := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		summaries = append(summaries, map[string]any{"id": node.ID, "name": node.Name, "status": node.Status})
	}
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"targetUri":       instanceURI.String(),
		"provider":        "cloudflare-mesh",
		"nodeCount":       len(nodes),
		"nodes":           summaries,
	})
}

func prepareNetworkOverlay(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(stringInput(args, "name", ""))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	accountID, _, apiToken, err := cloudflareCreds()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"name": name, "ha": false})
	if err != nil {
		return nil, err
	}
	var node meshNode
	if err := cloudflareJSON(ctx, apiToken, http.MethodPost, fmt.Sprintf("%s/accounts/%s/warp_connector", cloudflareAPIBase(), accountID), body, &node); err != nil {
		return nil, fmt.Errorf("create Cloudflare Mesh node: %w", err)
	}
	if node.ID == "" {
		return nil, fmt.Errorf("Cloudflare Mesh node response did not include an id")
	}
	if node.Token == "" {
		if err := cloudflareJSON(ctx, apiToken, http.MethodGet, fmt.Sprintf("%s/accounts/%s/warp_connector/%s/token", cloudflareAPIBase(), accountID, node.ID), nil, &node.Token); err != nil {
			return nil, fmt.Errorf("retrieve Cloudflare Mesh node token: %w", err)
		}
	}
	if strings.TrimSpace(node.Token) == "" {
		return nil, fmt.Errorf("Cloudflare Mesh node response did not include a token")
	}
	ref, err := newMeshMembershipRef()
	if err != nil {
		return nil, err
	}
	meshBindings.Lock()
	meshBindings.items[ref] = meshBinding{NodeID: node.ID, NodeName: name, NodeToken: node.Token, InstanceURI: instanceURI.String()}
	meshBindings.Unlock()
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        "cloudflare-mesh",
		"membershipRef":   ref,
		"nodeId":          node.ID,
		"nodeName":        name,
		"targetUri":       instanceURI.String(),
		"tokenExposed":    false,
	})
}

func attachNetworkOverlay(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	if ref == "" {
		return nil, fmt.Errorf("membershipRef is required")
	}
	meshBindings.Lock()
	binding, ok := meshBindings.items[ref]
	meshBindings.Unlock()
	if !ok {
		return nil, fmt.Errorf("network overlay membership reference is unknown or expired")
	}
	if binding.InstanceURI != instanceURI.String() {
		return nil, fmt.Errorf("network overlay membership is bound to another instance")
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := waitForGuestExec(ctx, client, instanceURI); err != nil {
		return nil, err
	}
	installScript := `set -eu
IFS= read -r mesh_token || true
if [ -z "$mesh_token" ]; then
  echo 'Cloudflare Mesh connector token was not received on stdin' >&2
  exit 1
fi
if ! command -v warp-cli >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg
  install -d -m 0755 /usr/share/keyrings
  curl -fsSL https://pkg.cloudflareclient.com/pubkey.gpg | gpg --yes --dearmor -o /usr/share/keyrings/cloudflare-warp-archive-keyring.gpg
  . /etc/os-release
  printf 'deb [signed-by=/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg] https://pkg.cloudflareclient.com/ %s main\n' "$VERSION_CODENAME" > /etc/apt/sources.list.d/cloudflare-client.list
  apt-get update -qq
  apt-get install -y -qq cloudflare-warp
fi
if command -v systemctl >/dev/null 2>&1; then
  systemctl enable --now warp-svc.service >/dev/null 2>&1 || true
fi
printf 'net.ipv4.ip_forward = 1\nnet.ipv6.conf.all.forwarding = 1\nnet.ipv6.conf.all.accept_ra = 2\n' > /etc/sysctl.d/99-zzz-cloudflare-warp-connector.conf
sysctl --system >/dev/null 2>&1 || true
if ! warp-cli --accept-tos status 2>/dev/null | grep -q 'Connected'; then
  warp-cli --accept-tos connector new "$mesh_token"
  warp-cli --accept-tos connect
fi
for attempt in $(seq 1 60); do
  status="$(warp-cli --accept-tos status 2>/dev/null || true)"
  addresses="$(ip -4 -o addr show scope global 2>/dev/null || true)"
  if printf '%s\n' "$status" | grep -q 'Connected' && printf '%s\n' "$addresses" | grep -q '100\.'; then
    break
  fi
  sleep 2
done
mesh_iface="$(ip -4 -o addr show scope global 2>/dev/null | awk '$4 ~ /^100\./ {print $2; exit}' | sed 's/:$//' )"
if [ -z "$mesh_iface" ]; then
  echo 'Cloudflare Mesh interface could not be identified for the main-table route' >&2
  exit 1
fi
# WARP normally installs its broad routes in a policy table selected by a
# firewall mark. Native daemons such as k3s do not inherit that mark, so their
# ordinary TCP sockets otherwise resolve a Mesh peer through the Incus bridge.
# Publish the provider-owned Mesh CIDR in the main table as well; this is the
# declared overlay route that makes the endpoint usable by k3s and etcd.
ip route replace 100.96.0.0/12 dev "$mesh_iface"
warp-cli --accept-tos status
ip -4 -o addr show scope global`
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       instanceURI.String(),
		"command":   "bash",
		"args":      []string{"-lc", installScript},
		"stdin":     binding.NodeToken + "\n",
		"timeoutMs": 10 * 60 * 1000,
	})
	if err != nil {
		return nil, err
	}
	resultContent, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Mesh enrollment returned no structured command result")
	}
	output := stringInput(resultContent, "stdout", "")
	stderr := stringInput(resultContent, "stderr", "")
	if exitCode, ok := numericInput(resultContent, "exitCode"); ok && exitCode != 0 {
		return nil, fmt.Errorf("Cloudflare Mesh guest enrollment failed with exit code %d: %s", exitCode, meshCommandDiagnostics(output+"\n"+stderr, binding.NodeToken))
	}
	meshIP := findMeshIP(output)
	if meshIP == "" {
		return nil, fmt.Errorf("Cloudflare Mesh node connected but no Mesh IP was observed: %s", meshCommandDiagnostics(output+"\n"+stderr, binding.NodeToken))
	}
	meshInterface := findMeshInterface(output, meshIP)
	if meshInterface == "" {
		return nil, fmt.Errorf("Cloudflare Mesh node connected but no Mesh interface was observed: %s", meshCommandDiagnostics(output+"\n"+stderr, binding.NodeToken))
	}
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        "cloudflare-mesh",
		"membershipRef":   ref,
		"nodeId":          binding.NodeID,
		"targetUri":       instanceURI.String(),
		"meshIp":          meshIP,
		"meshInterface":   meshInterface,
	})
}

func ensureNetworkOverlayHAEndpoint(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	targetURIs, err := targetURIsInput(args)
	if err != nil {
		return nil, err
	}
	hostname := strings.ToLower(strings.TrimSpace(stringInput(args, "hostname", "")))
	if err := refusePlatformHostname(hostname); err != nil {
		return nil, err
	}
	tunnelName := strings.TrimSpace(stringInput(args, "tunnelName", ""))
	if err := refusePlatformTunnelName(tunnelName); err != nil {
		return nil, err
	}
	accountID, zoneID, apiToken, err := cloudflareCreds()
	if err != nil {
		return nil, err
	}
	tunnel, err := ensureCloudflareTunnelByName(ctx, apiToken, accountID, tunnelName)
	if err != nil {
		return nil, err
	}
	rules, err := cloudflareTunnelIngress(ctx, apiToken, accountID, tunnel.ID)
	if err != nil {
		return nil, err
	}
	if err := refusePlatformIngress(rules); err != nil {
		return nil, err
	}
	if err := putCloudflareTunnelIngress(ctx, apiToken, accountID, tunnel.ID, []cloudflareIngressRule{
		{Hostname: hostname, Service: "https://127.0.0.1:6443", OriginRequest: map[string]any{"noTLSVerify": true}},
		{Service: "http_status:404"},
	}); err != nil {
		return nil, err
	}
	record, err := upsertDedicatedCNAME(ctx, apiToken, accountID, zoneID, hostname, tunnel.ID)
	if err != nil {
		return nil, err
	}
	tunnelToken, err := cloudflareTunnelToken(ctx, apiToken, accountID, tunnel.ID)
	if err != nil {
		return nil, err
	}
	serviceName := "opute-cloudflare-mesh-ha-" + tunnelName + ".service"
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	installed := make([]resourceid.URI, 0, len(targetURIs))
	for _, target := range targetURIs {
		if err := installHAConnector(ctx, client, target, serviceName, tunnelToken); err != nil {
			for _, installedTarget := range installed {
				_ = removeHAConnector(ctx, client, installedTarget, serviceName)
			}
			client.Close()
			_ = unpublishDedicatedTunnel(ctx, map[string]any{"hostname": hostname, "tunnelName": tunnelName})
			return nil, err
		}
		installed = append(installed, target)
	}
	client.Close()
	endpoint := "https://" + hostname
	statusCode, err := waitForHAEndpoint(ctx, endpoint, 3*time.Minute)
	if err != nil {
		cleanupClient, connectErr := connectHostAgent(context.Background())
		if connectErr == nil {
			for _, installedTarget := range installed {
				_ = removeHAConnector(context.Background(), cleanupClient, installedTarget, serviceName)
			}
			cleanupClient.Close()
		}
		_ = unpublishDedicatedTunnel(context.Background(), map[string]any{"hostname": hostname, "tunnelName": tunnelName})
		return nil, err
	}
	endpointRef, err := newMeshMembershipRef()
	if err != nil {
		return nil, err
	}
	meshHAEndpoints.Lock()
	meshHAEndpoints.items[endpointRef] = meshHAEndpoint{
		TunnelID: tunnel.ID, DNSRecordID: record.ID, Hostname: hostname, TunnelName: tunnelName,
		ServiceName: serviceName, TargetURIs: append([]string(nil), stringTargetURIs(targetURIs)...),
	}
	meshHAEndpoints.Unlock()
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        "cloudflare-tunnel",
		"endpoint":        endpoint,
		"endpointRef":     endpointRef,
		"targetCount":     len(targetURIs),
		"statusCode":      statusCode,
	})
}

func removeNetworkOverlayHAEndpoint(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ref := strings.TrimSpace(stringInput(args, "endpointRef", ""))
	if ref == "" {
		return nil, fmt.Errorf("endpointRef is required")
	}
	meshHAEndpoints.Lock()
	binding, ok := meshHAEndpoints.items[ref]
	if ok {
		delete(meshHAEndpoints.items, ref)
	}
	meshHAEndpoints.Unlock()
	if !ok {
		return nil, fmt.Errorf("network overlay HA endpoint reference is unknown or expired")
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	var cleanupErr error
	for _, target := range binding.TargetURIs {
		parsed, parseErr := resourceid.Parse(target)
		if parseErr != nil {
			cleanupErr = errors.Join(cleanupErr, parseErr)
			continue
		}
		cleanupErr = errors.Join(cleanupErr, removeHAConnector(ctx, client, parsed, binding.ServiceName))
	}
	client.Close()
	cleanupErr = errors.Join(cleanupErr, unpublishDedicatedTunnel(ctx, map[string]any{"hostname": binding.Hostname, "tunnelName": binding.TunnelName}))
	if cleanupErr != nil {
		return nil, cleanupErr
	}
	return structured(map[string]any{"contractVersion": capabilitycontract.NetworkOverlay, "ready": true, "deleted": true})
}

func targetURIsInput(args map[string]any) ([]resourceid.URI, error) {
	var rawValues []string
	if primary := strings.TrimSpace(stringInput(args, "targetUri", "")); primary != "" {
		rawValues = append(rawValues, primary)
	} else if values, ok := args["targetUris"].([]string); ok {
		rawValues = append(rawValues, values...)
	}
	switch values := args["peerTargetUris"].(type) {
	case []any:
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("targetUris must contain strings")
			}
			rawValues = append(rawValues, text)
		}
	case []string:
		rawValues = append(rawValues, values...)
	}
	if len(rawValues) < 2 {
		return nil, fmt.Errorf("targetUri and peerTargetUris must contain at least two VM resource URIs")
	}
	seen := make(map[string]struct{}, len(rawValues))
	targets := make([]resourceid.URI, 0, len(rawValues))
	for _, raw := range rawValues {
		target, err := typedTargetURI(raw, resourceid.TypeVM)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[target.String()]; exists {
			return nil, fmt.Errorf("targetUris must not contain duplicates")
		}
		seen[target.String()] = struct{}{}
		targets = append(targets, target)
	}
	return targets, nil
}

func stringTargetURIs(targets []resourceid.URI) []string {
	values := make([]string, 0, len(targets))
	for _, target := range targets {
		values = append(values, target.String())
	}
	return values
}

func installHAConnector(ctx context.Context, client *hostagentclient.Client, target resourceid.URI, serviceName, token string) error {
	if err := waitForGuestExec(ctx, client, target); err != nil {
		return err
	}
	artifact, err := verifiedCloudflaredArtifact(ctx)
	if err != nil {
		return err
	}
	if err := stageCloudflaredArtifact(ctx, client, target, artifact); err != nil {
		return fmt.Errorf("stage verified Cloudflare connector in guest: %w", err)
	}
	script := `set -eu
IFS= read -r tunnel_token || true
if [ -z "$tunnel_token" ]; then
  echo 'Cloudflare Tunnel token was not received on stdin' >&2
  exit 1
fi
if ! command -v cloudflared >/dev/null 2>&1; then
	install -m 0755 /tmp/opute-cloudflared /usr/local/bin/cloudflared
fi
rm -f /tmp/opute-cloudflared
printf '%s\n' '[Unit]' 'After=network-online.target' '' '[Service]' "ExecStart=/usr/local/bin/cloudflared tunnel --no-autoupdate run --token $tunnel_token" 'Restart=on-failure' > /etc/systemd/system/SERVICE_NAME
unset tunnel_token
systemctl daemon-reload
systemctl enable --now SERVICE_NAME
cloudflared --version >/dev/null`
	script = strings.ReplaceAll(script, "SERVICE_NAME", serviceName)
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri": target.String(), "command": "bash", "args": []string{"-lc", script}, "stdin": token + "\n", "timeoutMs": 10 * 60 * 1000,
	})
	if err != nil {
		return err
	}
	if result.StructuredContent == nil {
		return fmt.Errorf("Cloudflare HA connector returned no command result")
	}
	if output, ok := result.StructuredContent.(map[string]any); ok {
		if code, ok := numericInput(output, "exitCode"); ok && code != 0 {
			return fmt.Errorf("Cloudflare HA connector failed with exit code %d: %s", code, meshCommandDiagnostics(stringInput(output, "stdout", "")+"\n"+stringInput(output, "stderr", ""), token))
		}
	}
	return nil
}

func stageCloudflaredArtifact(ctx context.Context, client *hostagentclient.Client, target resourceid.URI, encoded string) error {
	for start := 0; start < len(encoded); start += cloudflaredChunkChars {
		end := start + cloudflaredChunkChars
		if end > len(encoded) {
			end = len(encoded)
		}
		redirect := ">"
		if start > 0 {
			redirect = ">>"
		}
		result, err := callHost(ctx, client, "run_instance_command", map[string]any{
			"uri":       target.String(),
			"command":   "bash",
			"args":      []string{"-lc", "set -euo pipefail; base64 --decode " + redirect + " /tmp/opute-cloudflared.gz"},
			"stdin":     encoded[start:end],
			"timeoutMs": 10 * 60 * 1000,
		})
		if err != nil {
			return err
		}
		if content, ok := result.StructuredContent.(map[string]any); ok {
			if exitCode, present := numericInput(content, "exitCode"); present && exitCode != 0 {
				return fmt.Errorf("Cloudflare connector artifact chunk %d failed with exit code %d", start/cloudflaredChunkChars+1, exitCode)
			}
		}
	}
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       target.String(),
		"command":   "bash",
		"args":      []string{"-lc", "set -euo pipefail; gzip --decompress -c /tmp/opute-cloudflared.gz > /tmp/opute-cloudflared; rm -f /tmp/opute-cloudflared.gz; observed=$(sha256sum /tmp/opute-cloudflared | awk '{print $1}'); [ \"$observed\" = \"" + cloudflaredArtifactSHA + "\" ]; chmod 0755 /tmp/opute-cloudflared"},
		"timeoutMs": 10 * 60 * 1000,
	})
	if err != nil {
		return err
	}
	if content, ok := result.StructuredContent.(map[string]any); ok {
		if exitCode, present := numericInput(content, "exitCode"); present && exitCode != 0 {
			return fmt.Errorf("Cloudflare connector artifact reconstruction failed with exit code %d", exitCode)
		}
	}
	return nil
}

func verifiedCloudflaredArtifact(ctx context.Context) (string, error) {
	cloudflaredArtifactCache.Lock()
	if cloudflaredArtifactCache.encoded != "" {
		artifact := cloudflaredArtifactCache.encoded
		cloudflaredArtifactCache.Unlock()
		return artifact, nil
	}
	cloudflaredArtifactCache.Unlock()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cloudflaredArtifactURL, nil)
	if err != nil {
		return "", fmt.Errorf("create Cloudflare connector artifact request: %w", err)
	}
	response, err := (&http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(next *http.Request, _ []*http.Request) error {
			if next.URL.Scheme != "https" {
				return fmt.Errorf("Cloudflare connector artifact redirect must remain HTTPS")
			}
			return nil
		},
	}).Do(request)
	if err != nil {
		return "", fmt.Errorf("download Cloudflare connector artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download Cloudflare connector artifact: HTTP %s", response.Status)
	}
	artifact, err := io.ReadAll(io.LimitReader(response.Body, maxCloudflaredArtifact+1))
	if err != nil {
		return "", fmt.Errorf("read Cloudflare connector artifact: %w", err)
	}
	if len(artifact) > maxCloudflaredArtifact {
		return "", fmt.Errorf("Cloudflare connector artifact exceeds %d bytes", maxCloudflaredArtifact)
	}
	digest := sha256.Sum256(artifact)
	observed := hex.EncodeToString(digest[:])
	if !strings.EqualFold(observed, cloudflaredArtifactSHA) {
		return "", fmt.Errorf("Cloudflare connector artifact checksum mismatch: expected %s, observed %s", cloudflaredArtifactSHA, observed)
	}

	var compressed strings.Builder
	compressed.Grow(len(artifact) / 2)
	encoder := base64.NewEncoder(base64.StdEncoding, &compressed)
	zipper := gzip.NewWriter(encoder)
	if _, err := zipper.Write(artifact); err != nil {
		_ = zipper.Close()
		_ = encoder.Close()
		return "", fmt.Errorf("compress Cloudflare connector artifact: %w", err)
	}
	if err := zipper.Close(); err != nil {
		_ = encoder.Close()
		return "", fmt.Errorf("finish Cloudflare connector artifact compression: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("encode Cloudflare connector artifact: %w", err)
	}
	encoded := compressed.String()

	cloudflaredArtifactCache.Lock()
	if cloudflaredArtifactCache.encoded == "" {
		cloudflaredArtifactCache.encoded = encoded
	} else {
		encoded = cloudflaredArtifactCache.encoded
	}
	cloudflaredArtifactCache.Unlock()
	return encoded, nil
}

func removeHAConnector(ctx context.Context, client *hostagentclient.Client, target resourceid.URI, serviceName string) error {
	_, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri": target.String(), "command": "bash", "args": []string{"-lc", "systemctl disable --now " + serviceName + " >/dev/null 2>&1 || true; rm -f /etc/systemd/system/" + serviceName + "; systemctl daemon-reload"}, "timeoutMs": 120_000,
	})
	return err
}

func waitForHAEndpoint(ctx context.Context, endpoint string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/version", nil)
		if err == nil {
			response, requestErr := http.DefaultClient.Do(request)
			if requestErr == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				response.Body.Close()
				if endpointHTTPStatusAcceptable(response.StatusCode) {
					return response.StatusCode, nil
				}
				lastErr = fmt.Errorf("stable endpoint returned HTTP %d", response.StatusCode)
			} else {
				lastErr = requestErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("stable endpoint did not become reachable")
	}
	return 0, fmt.Errorf("stable endpoint %s: %w", endpoint, lastErr)
}

func endpointHTTPStatusAcceptable(statusCode int) bool {
	return (statusCode >= 200 && statusCode < 400) || statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
}

func waitForGuestExec(ctx context.Context, client *hostagentclient.Client, instanceURI resourceid.URI) error {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		result, err := callHost(ctx, client, "run_instance_command", map[string]any{
			"uri":       instanceURI.String(),
			"command":   "true",
			"timeoutMs": 30 * 1000,
		})
		if err == nil {
			if content, ok := result.StructuredContent.(map[string]any); ok {
				if exitCode, present := numericInput(content, "exitCode"); present && exitCode == 0 {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for Incus guest agent on %s", instanceURI.String())
}

func numericInput(values map[string]any, key string) (int, bool) {
	value, ok := values[key]
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), true
	case float64:
		return int(number), true
	default:
		return 0, ok && value != nil
	}
}

func probeNetworkOverlay(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	peerIP := net.ParseIP(strings.TrimSpace(stringInput(args, "peerMeshIp", "")))
	if peerIP == nil || peerIP.To4() == nil {
		return nil, fmt.Errorf("peerMeshIp must be an IPv4 address")
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       instanceURI.String(),
		"command":   "ping",
		"args":      []string{"-c", "1", "-W", "3", peerIP.String()},
		"timeoutMs": 15 * 1000,
	})
	if err != nil {
		return nil, err
	}
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        "cloudflare-mesh",
		"targetUri":       instanceURI.String(),
		"peerMeshIp":      peerIP.String(),
		"probe":           result.StructuredContent,
	})
}

func removeNetworkOverlay(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	if ref == "" {
		return nil, fmt.Errorf("membershipRef is required")
	}
	meshBindings.Lock()
	binding, ok := meshBindings.items[ref]
	meshBindings.Unlock()
	if !ok {
		return structured(map[string]any{"contractVersion": capabilitycontract.NetworkOverlay, "ready": true, "deleted": true, "membershipRef": ref})
	}
	if binding.InstanceURI != instanceURI.String() {
		return nil, fmt.Errorf("network overlay membership is bound to another instance")
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if _, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       instanceURI.String(),
		"command":   "bash",
		"args":      []string{"-lc", "warp-cli disconnect || true; warp-cli registration delete || true"},
		"timeoutMs": 30 * 1000,
	}); err != nil {
		return nil, err
	}
	accountID, _, apiToken, err := cloudflareCreds()
	if err != nil {
		return nil, err
	}
	if err := cloudflareJSON(ctx, apiToken, http.MethodDelete, fmt.Sprintf("%s/accounts/%s/warp_connector/%s", cloudflareAPIBase(), accountID, binding.NodeID), nil, nil); err != nil {
		return nil, fmt.Errorf("delete Cloudflare Mesh node: %w", err)
	}
	meshBindings.Lock()
	delete(meshBindings.items, ref)
	meshBindings.Unlock()
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"deleted":         true,
		"membershipRef":   ref,
		"targetUri":       instanceURI.String(),
	})
}

func parseTargetURI(args map[string]any) (resourceid.URI, error) {
	instanceURI, err := resourceid.Parse(strings.TrimSpace(stringInput(args, "targetUri", "")))
	if err != nil {
		return resourceid.URI{}, fmt.Errorf("targetUri must be a canonical VM resource URI: %w", err)
	}
	if instanceURI.ResourceType != resourceid.TypeVM {
		return resourceid.URI{}, fmt.Errorf("targetUri requires resource type %q, got %q", resourceid.TypeVM, instanceURI.ResourceType)
	}
	return instanceURI, nil
}

func newMeshMembershipRef() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create network overlay membership reference: %w", err)
	}
	return "mesh-" + hex.EncodeToString(raw[:]), nil
}

func findMeshIP(output string) string {
	_, network, _ := net.ParseCIDR("100.96.0.0/12")
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, "(),")
		ip := net.ParseIP(strings.SplitN(candidate, "/", 2)[0])
		if ip != nil && ip.To4() != nil && network.Contains(ip) {
			return ip.String()
		}
	}
	return ""
}

func findMeshInterface(output, meshIP string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != "inet" || !strings.HasPrefix(fields[3], meshIP+"/") {
			continue
		}
		return strings.TrimSuffix(fields[1], ":")
	}
	return ""
}

func meshCommandDiagnostics(output, secret string) string {
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if line == "" || !(strings.Contains(lower, "status") || strings.Contains(lower, "connected") || strings.Contains(lower, "inet") || strings.Contains(lower, "interface") || strings.Contains(lower, "warp") || strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "unable") || strings.Contains(lower, "cannot") || strings.Contains(lower, "registration") || strings.Contains(lower, "terms")) {
			continue
		}
		if secret != "" {
			line = strings.ReplaceAll(line, secret, "[redacted]")
		}
		if len(line) > 240 {
			line = line[:240]
		}
		lines = append(lines, line)
		if len(lines) == 8 {
			break
		}
	}
	if len(lines) == 0 {
		return "guest command returned no diagnostic lines"
	}
	return "guest diagnostics=" + strings.Join(lines, " | ")
}
