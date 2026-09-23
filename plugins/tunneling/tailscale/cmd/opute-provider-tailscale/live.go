package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

const (
	defaultTailscaleEnvFile     = "/home/houman/.config/opute/tailscale.env"
	defaultTailscaleAuthKeyFile = "/home/houman/.config/opute/tailscale.authkey"
	tailscaleCGNATPrefix        = "100."
)

var livePendingAuthKeys = struct {
	sync.Mutex
	byRef map[string]string
}{byRef: make(map[string]string)}

var liveEnvOnce sync.Once

func ensureTailscaleEnvLoaded() {
	liveEnvOnce.Do(func() {
		loadTailscaleEnvFile(firstNonEmpty(os.Getenv("OPUTE_TAILSCALE_ENV_FILE"), defaultTailscaleEnvFile))
		if strings.TrimSpace(os.Getenv("TAILSCALE_AUTH_KEY")) == "" {
			authFile := firstNonEmpty(os.Getenv("TAILSCALE_AUTH_KEY_FILE"), defaultTailscaleAuthKeyFile)
			if raw, err := os.ReadFile(authFile); err == nil {
				key := strings.TrimSpace(string(raw))
				if key != "" {
					_ = os.Setenv("TAILSCALE_AUTH_KEY", key)
				}
			}
		}
	})
}

func loadTailscaleEnvFile(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	if strings.TrimSpace(os.Getenv("TAILSCALE_API_KEY")) != "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(strings.Trim(value, `"'`))
		if key == "" || value == "" {
			continue
		}
		if strings.TrimSpace(os.Getenv(key)) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func dispatchLiveOverlayOperation(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	ensureTailscaleEnvLoaded()
	switch operation {
	case capabilitycontract.NetworkOverlayValidateOperation:
		return liveValidateOverlay(ctx, args)
	case capabilitycontract.NetworkOverlayPrepareMembershipOperation:
		return livePrepareMembership(ctx, args)
	case capabilitycontract.NetworkOverlayAttachTargetOperation:
		return liveAttachTarget(ctx, args)
	case capabilitycontract.NetworkOverlayEnrollOperation:
		return liveEnrollOverlay(ctx, args)
	case capabilitycontract.NetworkOverlayProbeReachabilityOperation:
		return liveProbeReachability(ctx, args)
	case capabilitycontract.NetworkOverlayProbeOperation:
		return liveProbePathAware(ctx, args)
	case capabilitycontract.NetworkOverlayReportTwoNodeReadinessOperation:
		return reportTwoNodeReadiness(args)
	case capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation:
		return liveEnsurePrivateMesh(ctx, args)
	case capabilitycontract.NetworkOverlayEnsurePrivateServiceOperation:
		return liveEnsurePrivateService(ctx, args)
	case capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, capabilitycontract.NetworkOverlayEnsureHAEndpointOperation:
		return liveEnsurePublicIngress(ctx, args)
	case capabilitycontract.NetworkOverlayPromotePublicIngressOperation:
		return livePromotePublicIngress(ctx, args)
	case capabilitycontract.NetworkOverlayRemoveHAEndpointOperation:
		return liveRemoveHAEndpoint(ctx, args)
	case capabilitycontract.NetworkOverlayRemoveMembershipOperation:
		return liveRemoveMembership(ctx, args)
	default:
		return nil, fmt.Errorf("unknown network overlay operation %q", operation)
	}
}

func dispatchLiveMeshRuntime(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	ensureTailscaleEnvLoaded()
	switch operation {
	case capabilitycontract.MeshRuntimeValidateOperation, capabilitycontract.MeshRuntimeStatusOperation:
		return liveMeshRuntimeStatus(ctx, args)
	case capabilitycontract.MeshRuntimeEnsureAgentOperation:
		return liveEnsureMeshAgent(ctx, args)
	case capabilitycontract.MeshRuntimeEnsureControlPlaneOperation:
		return liveEnsureMeshControlPlane(ctx, args)
	default:
		return nil, fmt.Errorf("unknown mesh-runtime operation %q", operation)
	}
}

func liveEnsureMeshAgent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := waitForGuestExec(ctx, client, instanceURI); err != nil {
		return nil, err
	}
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       instanceURI.String(),
		"command":   "bash",
		"args":      []string{"-lc", tailscaleEnsureAgentScript()},
		"timeoutMs": 10 * 60 * 1000,
	})
	if err != nil {
		return nil, err
	}
	resultContent, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mesh-runtime.ensure-agent returned no structured command result")
	}
	output := stringInput(resultContent, "stdout", "")
	stderr := stringInput(resultContent, "stderr", "")
	if exitCode, present := numericInput(resultContent, "exitCode"); present && exitCode != 0 {
		return nil, fmt.Errorf("mesh-runtime.ensure-agent failed with exit code %d: %s", exitCode, output+"\n"+stderr)
	}
	agentVersion := "unknown"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "agent_version=") {
			agentVersion = strings.TrimPrefix(line, "agent_version=")
		}
	}
	ownershipStore.Lock()
	rec := ownershipStore.runtimes[instanceURI.String()]
	rec.TargetURI = instanceURI.String()
	rec.AgentReady = true
	rec.AgentVersion = agentVersion
	rec.Generation = providerGeneration
	ownershipStore.runtimes[instanceURI.String()] = rec
	ownershipStore.Unlock()
	return structured(meshRuntimeBase(instanceURI.String(), rec, map[string]any{"ready": true}))
}

func liveMeshRuntimeStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := waitForGuestExec(ctx, client, instanceURI); err != nil {
		return nil, err
	}
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       instanceURI.String(),
		"command":   "bash",
		"args":      []string{"-lc", tailscaleAgentStatusScript()},
		"timeoutMs": 60 * 1000,
	})
	if err != nil {
		return nil, err
	}
	resultContent, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mesh-runtime.status returned no structured command result")
	}
	output := stringInput(resultContent, "stdout", "")
	agentReady := strings.Contains(output, "agent_ready=true")
	agentVersion := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "agent_version=") {
			agentVersion = strings.TrimPrefix(line, "agent_version=")
		}
	}
	ownershipStore.Lock()
	rec := ownershipStore.runtimes[instanceURI.String()]
	rec.TargetURI = instanceURI.String()
	rec.AgentReady = agentReady
	if agentVersion != "" {
		rec.AgentVersion = agentVersion
	}
	ownershipStore.runtimes[instanceURI.String()] = rec
	ownershipStore.Unlock()
	return structured(meshRuntimeBase(instanceURI.String(), rec, map[string]any{
		"ready": agentReady,
	}))
}

func liveEnsureMeshControlPlane(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	_ = ctx
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ownershipStore.Lock()
	rec := ownershipStore.runtimes[instanceURI.String()]
	ownershipStore.Unlock()
	if !rec.AgentReady {
		// Best-effort: status may not have been called; still require ensure-agent.
		return nil, fmt.Errorf("mesh-runtime.ensure-control-plane requires mesh-runtime.ensure-agent first")
	}
	ingressClass := firstNonEmpty(stringInput(args, "ingressClassName", ""), "tailscale")
	evidence := strings.TrimSpace(stringInput(args, "operatorEvidence", ""))
	operatorMode := true
	if raw, ok := args["operatorMode"]; ok && raw != nil {
		if b, ok := raw.(bool); ok {
			operatorMode = b
		}
	}
	if operatorMode && evidence == "" && ingressClass != "tailscale" {
		return nil, fmt.Errorf("mesh-runtime.ensure-control-plane operatorMode requires operatorEvidence or ingressClassName=tailscale")
	}
	ownershipStore.Lock()
	rec = ownershipStore.runtimes[instanceURI.String()]
	rec.TargetURI = instanceURI.String()
	rec.ControlPlaneReady = true
	rec.IngressClassName = ingressClass
	rec.ControlPlaneRef = firstNonEmpty(stringInput(args, "controlPlaneRef", ""), "tailscale-operator")
	rec.Generation = providerGeneration
	ownershipStore.runtimes[instanceURI.String()] = rec
	ownershipStore.Unlock()
	return structured(meshRuntimeBase(instanceURI.String(), rec, map[string]any{"ready": true}))
}

func liveAPIClient() (*tailscaleAPIClient, error) {
	ensureTailscaleEnvLoaded()
	apiKey := strings.TrimSpace(os.Getenv("TAILSCALE_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("TAILSCALE_API_KEY is required for the live Tailscale backend")
	}
	return newTailscaleAPIClient(apiKey, os.Getenv("TAILSCALE_TAILNET"), os.Getenv("TAILSCALE_API_BASE")), nil
}

func resolveAuthKey(ctx context.Context, args map[string]any) (string, error) {
	ensureTailscaleEnvLoaded()
	key := firstNonEmpty(
		stringInput(args, "authKey", ""),
		stringInput(args, "credential", ""),
		os.Getenv("TAILSCALE_AUTH_KEY"),
	)
	if key != "" {
		return key, nil
	}
	client, err := liveAPIClient()
	if err != nil {
		return "", fmt.Errorf("auth key unavailable and API client failed: %w", err)
	}
	created, err := client.CreateAuthKey(ctx, 24*60*60)
	if err != nil {
		return "", err
	}
	return created.Key, nil
}

func resolveAPIKey(args map[string]any) string {
	ensureTailscaleEnvLoaded()
	return firstNonEmpty(stringInput(args, "apiKey", ""), stringInput(args, "credential", ""), os.Getenv("TAILSCALE_API_KEY"))
}

func liveValidateOverlay(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	kind, err := requireCredentialKind(args, "")
	if err != nil {
		return nil, err
	}
	if err := assertCredentialPresence(args, kind); err != nil {
		return nil, err
	}
	if kind == credKindAPIKey || kind == "" {
		if key := resolveAPIKey(args); key != "" {
			_ = os.Setenv("TAILSCALE_API_KEY", key)
		}
	}
	client, err := liveAPIClient()
	if err != nil {
		return nil, err
	}
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        providerName,
		"generation":      providerGeneration,
		"targetUri":       instanceURI.String(),
		"nodeCount":       len(devices),
		"credentialKind":  firstNonEmpty(kind, credKindAPIKey),
	})
}

func livePrepareMembership(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	if _, err := requireCredentialKind(args, credKindAuthKey); err != nil {
		return nil, err
	}
	if err := assertCredentialPresence(args, credKindAuthKey); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(stringInput(args, "name", ""))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	authKey, err := resolveAuthKey(ctx, args)
	if err != nil {
		return nil, err
	}
	record, err := upsertMembership(instanceURI.String(), name, false)
	if err != nil {
		return nil, err
	}
	livePendingAuthKeys.Lock()
	livePendingAuthKeys.byRef[record.Ref] = authKey
	livePendingAuthKeys.Unlock()
	return structured(overlayBase(record, map[string]any{
		"ready":         true,
		"membershipRef": record.Ref,
		"nodeId":        record.NodeID,
		"nodeName":      record.NodeName,
		"overlayUri":    record.OverlayURI,
	}))
}

func liveAttachTarget(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	if ref == "" {
		return nil, fmt.Errorf("membershipRef is required")
	}
	ownershipStore.Lock()
	record, ok := ownershipStore.memberships[ref]
	ownershipStore.Unlock()
	if !ok {
		return nil, fmt.Errorf("network overlay membership reference is unknown or expired")
	}
	if record.Generation != providerGeneration {
		return nil, fmt.Errorf("stale provider generation for membership")
	}
	if record.TargetURI != instanceURI.String() {
		return nil, fmt.Errorf("network overlay membership is bound to another instance")
	}
	livePendingAuthKeys.Lock()
	authKey := livePendingAuthKeys.byRef[ref]
	livePendingAuthKeys.Unlock()
	if authKey == "" {
		authKey, err = resolveAuthKey(ctx, args)
		if err != nil {
			return nil, err
		}
	}
	return liveEnrollGuest(ctx, args, instanceURI, record.NodeName, ref, authKey)
}

func liveEnrollOverlay(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	if _, err := requireCredentialKind(args, credKindAuthKey); err != nil {
		return nil, err
	}
	if err := assertCredentialPresence(args, credKindAuthKey); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(stringInput(args, "name", ""))
	if name == "" {
		name = "node"
	}
	refHint := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	ownershipStore.Lock()
	existingRef, hasExisting := ownershipStore.byTarget[instanceURI.String()]
	var existing membershipRecord
	if hasExisting {
		existing = ownershipStore.memberships[existingRef]
	}
	ownershipStore.Unlock()
	if hasExisting {
		if existing.Generation != providerGeneration {
			return nil, fmt.Errorf("stale provider generation for membership")
		}
		if refHint != "" && refHint != existingRef {
			return nil, fmt.Errorf("membershipRef does not match owned enrollment for target")
		}
		if existing.Attached && existing.MeshIP != "" && existing.NodeID != "" && !strings.HasPrefix(existing.NodeID, "node-") {
			return structured(overlayBase(existing, map[string]any{
				"ready":         true,
				"membershipRef": existing.Ref,
				"nodeId":        existing.NodeID,
				"nodeName":      existing.NodeName,
				"meshIp":        existing.MeshIP,
				"meshInterface": firstNonEmpty(existing.MeshInterface, "tailscale0"),
				"overlayUri":    existing.OverlayURI,
			}))
		}
		if name == "" {
			name = existing.NodeName
		}
		refHint = existing.Ref
	}
	authKey, err := resolveAuthKey(ctx, args)
	if err != nil {
		return nil, err
	}
	if refHint == "" {
		record, err := upsertMembership(instanceURI.String(), name, false)
		if err != nil {
			return nil, err
		}
		refHint = record.Ref
	}
	return liveEnrollGuest(ctx, args, instanceURI, name, refHint, authKey)
}

func liveEnrollGuest(ctx context.Context, args map[string]any, instanceURI resourceid.URI, hostname, membershipRef, authKey string) (*mcp.CallToolResult, error) {
	hostname = sanitizeHostname(hostname)
	if hostname == "" {
		return nil, fmt.Errorf("name is required")
	}
	if err := requireMeshAgentReady(instanceURI.String()); err != nil {
		return nil, err
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := waitForGuestExec(ctx, client, instanceURI); err != nil {
		return nil, err
	}
	installScript := tailscaleEnrollScript(hostname)
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       instanceURI.String(),
		"command":   "bash",
		"args":      []string{"-lc", installScript},
		"stdin":     authKey + "\n",
		"timeoutMs": 10 * 60 * 1000,
	})
	if err != nil {
		return nil, err
	}
	resultContent, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Tailscale enrollment returned no structured command result")
	}
	output := stringInput(resultContent, "stdout", "")
	stderr := stringInput(resultContent, "stderr", "")
	if exitCode, present := numericInput(resultContent, "exitCode"); present && exitCode != 0 {
		return nil, fmt.Errorf("Tailscale guest enrollment failed with exit code %d: %s", exitCode, redactSecret(output+"\n"+stderr, authKey))
	}
	meshIP := findTailscaleIPv4(output)
	if meshIP == "" {
		return nil, fmt.Errorf("Tailscale node connected but no Mesh IP was observed: %s", redactSecret(output+"\n"+stderr, authKey))
	}
	api, err := liveAPIClient()
	if err != nil {
		return nil, err
	}
	var device TailscaleDevice
	deadline := time.Now().Add(2 * time.Minute)
	for {
		devices, listErr := api.ListDevices(ctx)
		if listErr != nil {
			return nil, listErr
		}
		found, ok := findDeviceByHostname(devices, hostname)
		if ok {
			device = found
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("Tailscale device with hostname %q was not observed in the API after enrollment", hostname)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if device.ID == "" {
		return nil, fmt.Errorf("Tailscale device response did not include an id")
	}
	if apiIP := deviceIPv4(device); apiIP != "" {
		meshIP = apiIP
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	record, ok := ownershipStore.memberships[membershipRef]
	if !ok {
		created, upsertErr := upsertMembershipLocked(instanceURI.String(), hostname, true)
		if upsertErr != nil {
			return nil, upsertErr
		}
		record = created
		membershipRef = record.Ref
	}
	if record.TargetURI != instanceURI.String() {
		return nil, fmt.Errorf("network overlay membership is bound to another instance")
	}
	record.NodeName = hostname
	record.NodeID = device.ID
	record.MeshIP = meshIP
	record.MeshInterface = "tailscale0"
	record.Attached = true
	record.Generation = providerGeneration
	ownershipStore.memberships[membershipRef] = record
	ownershipStore.byTarget[record.TargetURI] = membershipRef
	livePendingAuthKeys.Lock()
	delete(livePendingAuthKeys.byRef, membershipRef)
	livePendingAuthKeys.Unlock()
	return structured(overlayBase(record, map[string]any{
		"ready":         true,
		"membershipRef": record.Ref,
		"nodeId":        record.NodeID,
		"nodeName":      record.NodeName,
		"meshIp":        record.MeshIP,
		"meshInterface": record.MeshInterface,
		"overlayUri":    record.OverlayURI,
	}))
}

func tailscaleEnsureAgentScript() string {
	return `set -euo pipefail
if command -v tailscale >/dev/null 2>&1 && command -v tailscaled >/dev/null 2>&1; then
  if command -v systemctl >/dev/null 2>&1; then
    timeout 30s systemctl enable --now tailscaled >/dev/null 2>&1 || true
  fi
  if ! pgrep -x tailscaled >/dev/null 2>&1; then
    nohup tailscaled >/var/log/tailscaled.log 2>&1 &
    sleep 2
  fi
  ver="$(tailscale version 2>/dev/null | head -n1 | tr -d '\r' || true)"
  printf 'agent_ready=true\nagent_version=%s\n' "${ver:-unknown}"
  exit 0
fi
export DEBIAN_FRONTEND=noninteractive
if command -v apt-get >/dev/null 2>&1; then
  timeout 180s apt-get update -qq || true
  timeout 180s apt-get install -y -qq curl ca-certificates gnupg || true
fi
timeout 180s curl -fsSL https://tailscale.com/install.sh | sh
if command -v systemctl >/dev/null 2>&1; then
  timeout 30s systemctl enable --now tailscaled >/dev/null 2>&1 || true
fi
if ! pgrep -x tailscaled >/dev/null 2>&1; then
  nohup tailscaled >/var/log/tailscaled.log 2>&1 &
  sleep 2
fi
if ! command -v tailscale >/dev/null 2>&1 || ! command -v tailscaled >/dev/null 2>&1; then
  echo 'mesh-runtime.ensure-agent failed: tailscale/tailscaled not installed' >&2
  exit 1
fi
ver="$(tailscale version 2>/dev/null | head -n1 | tr -d '\r' || true)"
printf 'agent_ready=true\nagent_version=%s\n' "${ver:-unknown}"
`
}

func tailscaleAgentStatusScript() string {
	return `set -euo pipefail
if ! command -v tailscale >/dev/null 2>&1 || ! command -v tailscaled >/dev/null 2>&1; then
  printf 'agent_ready=false\n'
  exit 0
fi
running=false
if pgrep -x tailscaled >/dev/null 2>&1; then
  running=true
fi
ver="$(tailscale version 2>/dev/null | head -n1 | tr -d '\r' || true)"
printf 'agent_ready=%s\nagent_version=%s\n' "$running" "${ver:-unknown}"
`
}

func tailscaleEnrollScript(hostname string) string {
	quotedHost := shellQuote(hostname)
	return `set -euo pipefail
IFS= read -r auth_key || true
if [ -z "$auth_key" ]; then
  echo 'Tailscale auth key was not received on stdin' >&2
  exit 1
fi
if ! command -v tailscale >/dev/null 2>&1 || ! command -v tailscaled >/dev/null 2>&1; then
  echo 'mesh agent not installed; call opute.capability.mesh-runtime.ensure-agent first' >&2
  exit 1
fi
if ! pgrep -x tailscaled >/dev/null 2>&1; then
  echo 'tailscaled is not running; call opute.capability.mesh-runtime.ensure-agent first' >&2
  exit 1
fi
timeout 120s tailscale up --authkey="$auth_key" --hostname=` + quotedHost + ` --accept-routes=false --reset
mesh_ip=""
for attempt in $(seq 1 60); do
  mesh_ip="$(tailscale ip -4 2>/dev/null | head -n1 | tr -d '[:space:]' || true)"
  if [ -n "$mesh_ip" ]; then
    break
  fi
  sleep 2
done
if [ -z "$mesh_ip" ]; then
  echo 'Tailscale did not assign an IPv4 address before the bounded wait' >&2
  tailscale status 2>&1 || true
  exit 1
fi
printf 'mesh_ip=%s\n' "$mesh_ip"
ip -4 -o addr show tailscale0 2>/dev/null || true
tailscale status 2>/dev/null || true
`
}

func liveEnsurePrivateMesh(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	peerURIRaw := strings.TrimSpace(stringInput(args, "peerTargetUri", ""))
	peerURI, err := resourceid.Parse(peerURIRaw)
	if err != nil {
		return nil, fmt.Errorf("peerTargetUri must be a canonical VM or container resource URI: %w", err)
	}
	if peerURI.ResourceType != resourceid.TypeVM && peerURI.ResourceType != resourceid.TypeContainer {
		return nil, fmt.Errorf("peerTargetUri requires resource type %q or %q", resourceid.TypeVM, resourceid.TypeContainer)
	}
	peerMeshIP := strings.TrimSpace(stringInput(args, "peerMeshIp", ""))
	if net.ParseIP(peerMeshIP) == nil {
		return nil, fmt.Errorf("peerMeshIp must be an IPv4 address")
	}
	ownershipStore.Lock()
	sourceRef, ok := ownershipStore.byTarget[instanceURI.String()]
	if !ok {
		ownershipStore.Unlock()
		return nil, fmt.Errorf("source target is not enrolled")
	}
	source := ownershipStore.memberships[sourceRef]
	peerRef, peerOK := ownershipStore.byTarget[peerURI.String()]
	var peer membershipRecord
	if peerOK {
		peer = ownershipStore.memberships[peerRef]
	}
	ownershipStore.Unlock()
	needAdopt := !peerOK || peer.Generation != providerGeneration || !peer.Attached || (peerMeshIP != "" && peer.MeshIP != "" && peer.MeshIP != peerMeshIP)
	if needAdopt {
		adoptArgs := map[string]any{
			"hostAgentId":   stringInput(args, "hostAgentId", ""),
			"peerTargetUri": peerURI.String(),
			"peerMeshIp":    peerMeshIP,
			"name":          peerURI.ResourceID,
		}
		if _, err := liveAdoptRemotePeer(ctx, adoptArgs); err != nil {
			return nil, fmt.Errorf("peer target is not enrolled and adopt failed: %w", err)
		}
		ownershipStore.Lock()
		peerRef = ownershipStore.byTarget[peerURI.String()]
		peer = ownershipStore.memberships[peerRef]
		ownershipStore.Unlock()
	}
	if source.Generation != providerGeneration || !source.Attached {
		return nil, fmt.Errorf("source membership is not attached for this generation")
	}
	if peer.Generation != providerGeneration || !peer.Attached {
		return nil, fmt.Errorf("peer membership is not attached for this generation")
	}
	if peer.MeshIP != peerMeshIP {
		return nil, fmt.Errorf("ambiguous peer: peerMeshIp does not match enrolled peer")
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := pingFromGuest(ctx, client, instanceURI, peer.MeshIP); err != nil {
		return nil, fmt.Errorf("forward private-mesh probe failed: %w", err)
	}
	reverseMethod := "guest-ping"
	if err := pingFromGuest(ctx, client, peerURI, source.MeshIP); err != nil {
		// Peer may live on a remote Incus host; prove reverse path via Tailscale ping from source.
		if err2 := tailscalePingFromGuest(ctx, client, instanceURI, peer.MeshIP); err2 != nil {
			return nil, fmt.Errorf("reverse private-mesh probe failed: guest=%v tailscale-ping=%v", err, err2)
		}
		reverseMethod = "tailscale-ping-from-source"
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	forwardKey := meshKey(instanceURI.String(), peerURI.String())
	reverseKey := meshKey(peerURI.String(), instanceURI.String())
	ownershipStore.meshEdges[forwardKey] = meshEdge{SourceURI: instanceURI.String(), PeerURI: peerURI.String(), PeerMeshIP: peer.MeshIP, Ready: true, Generation: providerGeneration}
	ownershipStore.meshEdges[reverseKey] = meshEdge{SourceURI: peerURI.String(), PeerURI: instanceURI.String(), PeerMeshIP: source.MeshIP, Ready: true, Generation: providerGeneration}
	return structured(overlayBase(source, map[string]any{
		"ready":               true,
		"pathClass":           pathClassPrivateMesh,
		"peerMeshIp":          peer.MeshIP,
		"overlayUri":          source.OverlayURI,
		"datastoreMode":       stringInput(args, "datastoreMode", "external-datastore"),
		"availabilityClass":   stringInput(args, "availabilityClass", "serving-continuity"),
		"recoveryPolicy":      stringInput(args, "recoveryPolicy", "explicit-acknowledgement"),
		"bothDirectionsReady": true,
		"reverseProbeMethod":  reverseMethod,
	}))
}

func liveAdoptRemotePeer(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	peerURI, err := resourceid.Parse(strings.TrimSpace(stringInput(args, "peerTargetUri", stringInput(args, "targetUri", ""))))
	if err != nil {
		return nil, fmt.Errorf("peerTargetUri must be a canonical resource URI: %w", err)
	}
	peerMeshIP := strings.TrimSpace(stringInput(args, "peerMeshIp", ""))
	hostname := sanitizeHostname(firstNonEmpty(stringInput(args, "name", ""), peerURI.ResourceID))
	client, err := liveAPIClient()
	if err != nil {
		return nil, err
	}
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	var match *TailscaleDevice
	for i := range devices {
		d := &devices[i]
		ip := deviceIPv4(*d)
		host := strings.TrimSuffix(strings.ToLower(firstNonEmpty(d.Hostname, d.Name)), ".")
		if peerMeshIP != "" && ip == peerMeshIP {
			match = d
			break
		}
		if hostname != "" && (host == hostname || strings.HasPrefix(host, hostname+".")) {
			match = d
			if peerMeshIP == "" {
				peerMeshIP = ip
			}
			break
		}
	}
	if match == nil {
		return nil, fmt.Errorf("remote Tailscale peer not found for hostname=%s meshIp=%s", hostname, peerMeshIP)
	}
	if peerMeshIP == "" {
		peerMeshIP = deviceIPv4(*match)
	}
	if peerMeshIP == "" {
		return nil, fmt.Errorf("remote peer has no IPv4 address")
	}
	record, err := upsertMembership(peerURI.String(), firstNonEmpty(hostname, match.Hostname, "peer"), true)
	if err != nil {
		return nil, err
	}
	ownershipStore.Lock()
	record.Attached = true
	record.MeshIP = peerMeshIP
	record.NodeID = firstNonEmpty(match.ID, record.NodeID)
	record.NodeName = firstNonEmpty(hostname, match.Hostname, record.NodeName)
	record.MeshInterface = "tailscale0"
	ownershipStore.memberships[record.Ref] = record
	ownershipStore.byTarget[peerURI.String()] = record.Ref
	ownershipStore.Unlock()
	return structured(overlayBase(record, map[string]any{
		"ready": true, "membershipRef": record.Ref, "nodeId": record.NodeID,
		"nodeName": record.NodeName, "meshIp": record.MeshIP, "adopted": true,
		"overlayUri": record.OverlayURI,
	}))
}

func pingFromGuest(ctx context.Context, client *hostagentclient.Client, guest resourceid.URI, peerIP string) error {
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri":       guest.String(),
		"command":   "ping",
		"args":      []string{"-c", "1", "-W", "3", peerIP},
		"timeoutMs": 15 * 1000,
	})
	if err != nil {
		return err
	}
	content, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return fmt.Errorf("ping returned no structured command result")
	}
	exitCode, present := numericInput(content, "exitCode")
	if !present || exitCode != 0 {
		return fmt.Errorf("ping %s from %s failed with exit code %d", peerIP, guest.String(), exitCode)
	}
	return nil
}

func tailscalePingFromGuest(ctx context.Context, client *hostagentclient.Client, guest resourceid.URI, peerIP string) error {
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri": guest.String(), "command": "tailscale",
		"args": []string{"ping", "-c", "1", peerIP}, "timeoutMs": 30 * 1000,
	})
	if err != nil {
		return err
	}
	content, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return fmt.Errorf("tailscale ping returned no structured command result")
	}
	exitCode, present := numericInput(content, "exitCode")
	if !present || exitCode != 0 {
		return fmt.Errorf("tailscale ping %s from %s failed with exit code %d", peerIP, guest.String(), exitCode)
	}
	return nil
}

func liveEnsurePrivateService(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	localTarget := strings.TrimSpace(stringInput(args, "localTarget", ""))
	if localTarget == "" {
		return nil, fmt.Errorf("localTarget is required")
	}
	ownershipStore.Lock()
	record, err := requireOwnedMembershipLocked(args, instanceURI.String())
	ownershipStore.Unlock()
	if err != nil {
		return nil, err
	}
	serveURL, err := normalizeLoopbackHTTP(localTarget)
	if err != nil {
		return nil, err
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	script := fmt.Sprintf(`set -euo pipefail
timeout 60s tailscale serve --bg %s
tailscale serve status || true
`, shellQuote(serveURL))
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri": instanceURI.String(), "command": "bash", "args": []string{"-lc", script}, "timeoutMs": 90 * 1000,
	})
	if err != nil {
		return nil, err
	}
	if content, ok := result.StructuredContent.(map[string]any); ok {
		if exitCode, present := numericInput(content, "exitCode"); present && exitCode != 0 {
			return nil, fmt.Errorf("tailscale serve failed with exit code %d: %s", exitCode, stringInput(content, "stderr", "")+" "+stringInput(content, "stdout", ""))
		}
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	ownershipStore.services[record.OverlayURI] = privateServiceRecord{
		OverlayURI: record.OverlayURI, TargetURI: record.TargetURI, LocalTarget: localTarget,
		PathClass: pathClassPrivateMesh, Generation: providerGeneration,
	}
	return structured(overlayBase(record, map[string]any{
		"ready": true, "pathClass": pathClassPrivateMesh, "endpoint": localTarget, "stable": true,
	}))
}

func requireOperatorStableEvidence(args map[string]any) error {
	if !boolInput(args, "operatorMode", false) {
		return nil
	}
	// Fail closed: stable/public Operator claims require explicit evidence fields.
	evidence := strings.TrimSpace(stringInput(args, "operatorEvidence", ""))
	ingressClass := strings.TrimSpace(stringInput(args, "ingressClassName", ""))
	endpoint := strings.TrimSpace(stringInput(args, "endpoint", stringInput(args, "hostname", "")))
	if evidence == "" && ingressClass != "tailscale" {
		return fmt.Errorf("operatorMode/stable=true requires operatorEvidence or ingressClassName=tailscale with an operator-managed endpoint")
	}
	if endpoint == "" {
		return fmt.Errorf("operatorMode requires endpoint/hostname for the operator-managed public URL")
	}
	lower := strings.ToLower(endpoint)
	if strings.Contains(lower, "opute-ha-a.") || strings.Contains(lower, "opute-ha-b.") {
		return fmt.Errorf("operatorMode refuses node-local Funnel hostname %q as a stable cluster endpoint", endpoint)
	}
	if !strings.Contains(lower, "://") {
		endpoint = "https://" + endpoint
	}
	_ = endpoint
	return nil
}

func liveEnsurePublicIngress(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	localTarget := strings.TrimSpace(stringInput(args, "localTarget", ""))
	if localTarget == "" {
		return nil, fmt.Errorf("localTarget is required")
	}
	if err := rejectControlPlanePublicTarget(localTarget, stringInput(args, "targetKind", "")); err != nil {
		return nil, err
	}
	ownershipStore.Lock()
	record, err := requireOwnedMembershipLocked(args, instanceURI.String())
	ownershipStore.Unlock()
	if err != nil {
		return nil, err
	}
	operatorMode := boolInput(args, "operatorMode", false)
	if err := requireOperatorStableEvidence(args); err != nil {
		return nil, err
	}
	if operatorMode {
		endpoint := strings.TrimSpace(stringInput(args, "endpoint", ""))
		hostname := strings.TrimSpace(stringInput(args, "hostname", ""))
		if endpoint == "" {
			hostname = strings.TrimSuffix(hostname, ".")
			if hostname == "" {
				return nil, fmt.Errorf("operatorMode requires endpoint or hostname")
			}
			if !strings.Contains(hostname, "://") {
				endpoint = "https://" + hostname
			} else {
				endpoint = hostname
			}
		}
		if hostname == "" {
			hostname = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
			hostname = strings.Split(hostname, "/")[0]
		}
		endpointRef, err := newOpaqueRef("ingress")
		if err != nil {
			return nil, err
		}
		ownershipStore.Lock()
		defer ownershipStore.Unlock()
		ingress := publicIngressRecord{
			EndpointRef: endpointRef, OverlayURI: record.OverlayURI, TargetURI: record.TargetURI,
			LocalTarget: localTarget, Endpoint: endpoint, Stable: true,
			PathClass: pathClassPublicIngress, Generation: providerGeneration, Hostname: hostname,
		}
		ownershipStore.ingresses[endpointRef] = ingress
		return structured(overlayBase(record, map[string]any{
			"ready": true, "pathClass": pathClassPublicIngress, "endpoint": endpoint,
			"endpointRef": endpointRef, "stable": true, "operatorMode": true,
			"operatorEvidence": stringInput(args, "operatorEvidence", ""),
			"ingressClassName": stringInput(args, "ingressClassName", ""),
		}))
	}
	// operatorMode short-circuit above records Operator-managed stable ingress without node Funnel CLI.
	serveURL, err := normalizeLoopbackHTTP(localTarget)
	if err != nil {
		return nil, err
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	script := fmt.Sprintf(`set -euo pipefail
# Reset then enable Funnel to the scoped local gateway/ingress target.
tailscale serve reset >/dev/null 2>&1 || true
tailscale funnel reset >/dev/null 2>&1 || true
timeout 60s tailscale funnel --bg --yes %s
DNS_NAME="$(tailscale status --json 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("Self") or {}).get("DNSName") or "")' 2>/dev/null || true)"
if [ -z "$DNS_NAME" ]; then
  DNS_NAME="$(tailscale status --json 2>/dev/null | sed -n 's/.*"DNSName"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
fi
printf 'dns_name=%%s\n' "$DNS_NAME"
printf 'local_target=%%s\n' %s
tailscale funnel status 2>/dev/null || true
tailscale serve status --json 2>/dev/null || true
`, shellQuote(serveURL), shellQuote(serveURL))
	result, err := callHost(ctx, client, "run_instance_command", map[string]any{
		"uri": instanceURI.String(), "command": "bash", "args": []string{"-lc", script}, "timeoutMs": 120 * 1000,
	})
	if err != nil {
		return nil, err
	}
	content, _ := result.StructuredContent.(map[string]any)
	if exitCode, present := numericInput(content, "exitCode"); present && exitCode != 0 {
		return nil, fmt.Errorf("tailscale funnel failed with exit code %d: %s", exitCode, stringInput(content, "stderr", "")+" "+stringInput(content, "stdout", ""))
	}
	hostname := firstNonEmpty(stringInput(args, "hostname", ""), findDNSName(stringInput(content, "stdout", "")), record.NodeName+".ts.net")
	hostname = strings.TrimSuffix(hostname, ".")
	endpoint := "https://" + hostname
	endpointRef, err := newOpaqueRef("ingress")
	if err != nil {
		return nil, err
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	ingress := publicIngressRecord{
		EndpointRef: endpointRef, OverlayURI: record.OverlayURI, TargetURI: record.TargetURI,
		LocalTarget: localTarget, Endpoint: endpoint, Stable: operatorMode,
		PathClass: pathClassPublicIngress, Generation: providerGeneration, Hostname: hostname,
	}
	ownershipStore.ingresses[endpointRef] = ingress
	return structured(overlayBase(record, map[string]any{
		"ready": true, "pathClass": pathClassPublicIngress, "endpoint": endpoint,
		"endpointRef": endpointRef, "stable": ingress.Stable,
	}))
}

func livePromotePublicIngress(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	_ = ctx
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	endpointRef := strings.TrimSpace(stringInput(args, "endpointRef", ""))
	if endpointRef == "" {
		return nil, fmt.Errorf("endpointRef is required")
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	ingress, ok := ownershipStore.ingresses[endpointRef]
	if !ok {
		return nil, fmt.Errorf("endpoint reference is unknown")
	}
	if ingress.Generation != providerGeneration {
		return nil, fmt.Errorf("stale provider generation for endpoint")
	}
	record, err := requireOwnedMembershipLocked(args, instanceURI.String())
	if err != nil {
		return nil, err
	}
	operatorMode := boolInput(args, "operatorMode", false)
	if err := requireOperatorStableEvidence(args); err != nil {
		return nil, err
	}
	ingress.TargetURI = record.TargetURI
	ingress.OverlayURI = record.OverlayURI
	ingress.Endpoint = "https://" + firstNonEmpty(ingress.Hostname, "node.ingress.example") + "/promoted"
	// operatorMode may claim stable=true without requiring Tailscale Operator to be installed yet.
	ingress.Stable = operatorMode
	ownershipStore.ingresses[endpointRef] = ingress
	return structured(overlayBase(record, map[string]any{
		"ready": true, "pathClass": pathClassPublicIngress, "endpoint": ingress.Endpoint,
		"endpointRef": endpointRef, "stable": ingress.Stable,
	}))
}

func liveRemoveHAEndpoint(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	endpointRef := strings.TrimSpace(stringInput(args, "endpointRef", ""))
	if endpointRef == "" {
		return nil, fmt.Errorf("endpointRef is required")
	}
	ownershipStore.Lock()
	ingress, ok := ownershipStore.ingresses[endpointRef]
	if ok {
		delete(ownershipStore.ingresses, endpointRef)
	}
	ownershipStore.Unlock()
	if !ok {
		return structured(map[string]any{
			"contractVersion": capabilitycontract.NetworkOverlay, "ready": true, "provider": providerName,
			"generation": providerGeneration, "deleted": true, "targetUri": "vm:local:placeholder",
		})
	}
	if ingress.Generation != providerGeneration {
		return nil, fmt.Errorf("refusing to delete endpoint owned by a foreign generation")
	}
	if client, err := connectHostAgent(ctx); err == nil {
		defer client.Close()
		_, _ = callHost(ctx, client, "run_instance_command", map[string]any{
			"uri": ingress.TargetURI, "command": "bash",
			"args":      []string{"-lc", "tailscale funnel --bg=false || true; tailscale serve reset || true"},
			"timeoutMs": 30 * 1000,
		})
	}
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay, "ready": true, "provider": providerName,
		"generation": providerGeneration, "deleted": true, "targetUri": ingress.TargetURI, "endpointRef": endpointRef,
	})
}

func liveRemoveMembership(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	if ref == "" {
		return nil, fmt.Errorf("membershipRef is required")
	}
	ownershipStore.Lock()
	record, ok := ownershipStore.memberships[ref]
	ownershipStore.Unlock()
	if !ok {
		return nil, fmt.Errorf("network overlay membership reference is unknown or expired")
	}
	if record.Generation != providerGeneration {
		return nil, fmt.Errorf("refusing to remove membership owned by a foreign generation")
	}
	if record.TargetURI != instanceURI.String() {
		return nil, fmt.Errorf("network overlay membership is bound to another instance")
	}
	if client, err := connectHostAgent(ctx); err == nil {
		defer client.Close()
		_, _ = callHost(ctx, client, "run_instance_command", map[string]any{
			"uri": instanceURI.String(), "command": "bash",
			"args": []string{"-lc", "tailscale logout || true"}, "timeoutMs": 30 * 1000,
		})
	}
	if api, err := liveAPIClient(); err == nil && record.NodeID != "" && !strings.HasPrefix(record.NodeID, "node-") {
		_ = api.DeleteDevice(ctx, record.NodeID)
	}
	ownershipStore.Lock()
	deleteOwnedForTargetLocked(record.TargetURI, ref)
	ownershipStore.Unlock()
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay, "ready": true, "provider": providerName,
		"generation": providerGeneration, "deleted": true, "membershipRef": ref, "targetUri": instanceURI.String(),
	})
}

func liveProbeReachability(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	args = cloneArgs(args)
	if stringInput(args, "pathClass", "") == "" {
		args["pathClass"] = pathClassPrivateMesh
	}
	return liveProbePathAware(ctx, args)
}

func liveProbePathAware(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	pathClass := strings.TrimSpace(stringInput(args, "pathClass", ""))
	if pathClass != pathClassPrivateMesh && pathClass != pathClassPublicIngress {
		return nil, fmt.Errorf("pathClass must be %q or %q", pathClassPrivateMesh, pathClassPublicIngress)
	}
	peerMeshIP := strings.TrimSpace(stringInput(args, "peerMeshIp", ""))
	ownershipStore.Lock()
	ref, ok := ownershipStore.byTarget[instanceURI.String()]
	if !ok {
		ownershipStore.Unlock()
		return structured(map[string]any{
			"contractVersion": capabilitycontract.NetworkOverlay,
			"ready":           false,
			"provider":        providerName,
			"generation":      providerGeneration,
			"targetUri":       instanceURI.String(),
			"pathClass":       pathClass,
			"probe":           map[string]any{"pathClass": pathClass, "ready": false},
			"error":           "target is not enrolled in the overlay",
			"stable":          false,
		})
	}
	record := ownershipStore.memberships[ref]
	if record.Generation != providerGeneration {
		ownershipStore.Unlock()
		return structured(map[string]any{
			"contractVersion": capabilitycontract.NetworkOverlay,
			"ready":           false,
			"provider":        providerName,
			"generation":      providerGeneration,
			"targetUri":       instanceURI.String(),
			"pathClass":       pathClass,
			"probe":           map[string]any{"pathClass": pathClass, "ready": false},
			"error":           "stale provider generation for membership",
			"stable":          false,
		})
	}
	result := overlayBase(record, map[string]any{"pathClass": pathClass, "probe": map[string]any{"pathClass": pathClass}})
	switch pathClass {
	case pathClassPrivateMesh:
		if peerMeshIP == "" {
			// Membership-presence probe (used as enroll readiness without a peer).
			ownershipStore.Unlock()
			result["ready"] = true
			probe := result["probe"].(map[string]any)
			probe["ready"] = true
			probe["membershipOnly"] = true
			return structured(result)
		}
		if net.ParseIP(peerMeshIP) == nil {
			ownershipStore.Unlock()
			return nil, fmt.Errorf("peerMeshIp must be an IPv4 address")
		}
		ready := privateMeshReadyLocked(instanceURI.String(), peerMeshIP)
		ownershipStore.Unlock()
		if !ready {
			if client, err := connectHostAgent(ctx); err == nil {
				defer client.Close()
				if err := pingFromGuest(ctx, client, instanceURI, peerMeshIP); err == nil {
					ready = true
				}
			}
		}
		result["ready"] = ready
		result["peerMeshIp"] = peerMeshIP
		probe := result["probe"].(map[string]any)
		probe["ready"] = ready
		probe["peerMeshIp"] = peerMeshIP
		if !ready {
			ownershipStore.Lock()
			if publicReadyLocked(instanceURI.String()) {
				probe["publicIngressPresent"] = true
				probe["privateSatisfiedByPublic"] = false
			}
			ownershipStore.Unlock()
			result["error"] = "private-mesh peer reachability is not ready"
		}
		return structured(result)
	case pathClassPublicIngress:
		ready := publicReadyLocked(instanceURI.String())
		stable := false
		endpoint := ""
		if ready {
			for _, ingress := range ownershipStore.ingresses {
				if ingress.TargetURI == instanceURI.String() && ingress.Generation == providerGeneration {
					endpoint = ingress.Endpoint
					stable = ingress.Stable
					break
				}
			}
		}
		ownershipStore.Unlock()
		result["ready"] = ready
		result["stable"] = stable
		if endpoint != "" {
			result["endpoint"] = endpoint
		}
		probe := result["probe"].(map[string]any)
		probe["ready"] = ready
		if !ready {
			result["error"] = "public-ingress endpoint is not ready"
		}
		return structured(result)
	default:
		ownershipStore.Unlock()
		return nil, fmt.Errorf("unsupported pathClass %q", pathClass)
	}
}

func liveFinalizeOwnedTeardown(ctx context.Context, inputs map[string]any) error {
	generation := firstNonEmpty(stringInput(inputs, "generation", ""), providerGeneration)
	if generation != providerGeneration {
		return fmt.Errorf("refusing teardown for foreign provider generation")
	}
	ensureTailscaleEnvLoaded()
	ownershipStore.Lock()
	records := make([]membershipRecord, 0, len(ownershipStore.memberships))
	for _, record := range ownershipStore.memberships {
		if record.Generation == generation {
			records = append(records, record)
		}
	}
	ownershipStore.Unlock()
	var client *hostagentclient.Client
	if len(records) > 0 {
		if c, err := connectHostAgent(ctx); err == nil {
			client = c
			defer client.Close()
		}
	}
	api, _ := liveAPIClient()
	for _, record := range records {
		if client != nil {
			_, _ = callHost(ctx, client, "run_instance_command", map[string]any{
				"uri": record.TargetURI, "command": "bash",
				"args": []string{"-lc", "tailscale logout || true"}, "timeoutMs": 30 * 1000,
			})
		}
		if api != nil && record.NodeID != "" && !strings.HasPrefix(record.NodeID, "node-") {
			_ = api.DeleteDevice(ctx, record.NodeID)
		}
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	for ref, record := range ownershipStore.memberships {
		if record.Generation == generation {
			deleteOwnedForTargetLocked(record.TargetURI, ref)
		}
	}
	return nil
}

func connectHostAgent(ctx context.Context) (*hostagentclient.Client, error) {
	endpoint := strings.TrimSpace(os.Getenv("OPUTE_HOST_AGENT_ENDPOINT"))
	if endpoint == "" {
		return nil, fmt.Errorf("OPUTE_HOST_AGENT_ENDPOINT is required for Tailscale provider callbacks")
	}
	bearerToken := firstNonEmpty(os.Getenv("OPUTE_HOST_AGENT_BEARER_TOKEN"), os.Getenv("MCP_AUTH_TOKEN"))
	return hostagentclient.Connect(ctx, endpoint, bearerToken)
}

func callHost(ctx context.Context, client *hostagentclient.Client, name string, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := client.Call(ctx, name, args)
	if err != nil {
		return nil, fmt.Errorf("host callback %s: %w", name, err)
	}
	if result == nil {
		return nil, fmt.Errorf("host callback %s returned no result", name)
	}
	if result.IsError {
		detail := ""
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
				detail = strings.TrimSpace(text.Text)
				break
			}
		}
		if detail == "" {
			detail = "host callback returned an error result"
		}
		return nil, fmt.Errorf("host callback %s failed: %s", name, detail)
	}
	return result, nil
}

func waitForGuestExec(ctx context.Context, client *hostagentclient.Client, instanceURI resourceid.URI) error {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		result, err := callHost(ctx, client, "run_instance_command", map[string]any{
			"uri": instanceURI.String(), "command": "true", "timeoutMs": 30 * 1000,
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

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func sanitizeHostname(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, name)
	return strings.Trim(name, "-.")
}

func findTailscaleIPv4(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mesh_ip=") {
			ip := strings.TrimSpace(strings.TrimPrefix(line, "mesh_ip="))
			if net.ParseIP(ip) != nil && net.ParseIP(ip).To4() != nil {
				return ip
			}
		}
	}
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, "(),=")
		ip := net.ParseIP(strings.SplitN(candidate, "/", 2)[0])
		if ip != nil && ip.To4() != nil && strings.HasPrefix(ip.String(), tailscaleCGNATPrefix) {
			return ip.String()
		}
	}
	return ""
}

func findDNSName(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "dns_name=") {
			return strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "dns_name=")), ".")
		}
	}
	return ""
}

func normalizeLoopbackHTTP(localTarget string) (string, error) {
	localTarget = strings.TrimSpace(localTarget)
	if localTarget == "" {
		return "", fmt.Errorf("localTarget is required")
	}
	if !strings.Contains(localTarget, "://") {
		if _, err := net.LookupPort("tcp", localTarget); err == nil || isDigits(localTarget) {
			return "http://127.0.0.1:" + localTarget, nil
		}
		return "", fmt.Errorf("localTarget must be an HTTP URL or port")
	}
	parsed, err := url.ParseRequestURI(localTarget)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("localTarget must be an HTTP(S) URL")
	}
	host := parsed.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", fmt.Errorf("localTarget must target loopback")
	}
	return localTarget, nil
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func redactSecret(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}
