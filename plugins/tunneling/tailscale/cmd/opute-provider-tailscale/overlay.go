package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	"github.com/wunderous/host-agents/internal/resourceid"
)

const (
	pathClassPrivateMesh   = "private-mesh"
	pathClassPublicIngress = "public-ingress"
	credKindAPIKey         = "api-key"
	credKindAuthKey        = "auth-key"
	providerName           = "network-overlay"
)

type membershipRecord struct {
	Ref           string
	TargetURI     string
	OverlayURI    string
	NodeID        string
	NodeName      string
	MeshIP        string
	MeshInterface string
	Generation    string
	Attached      bool
}

type meshEdge struct {
	SourceURI  string
	PeerURI    string
	PeerMeshIP string
	Ready      bool
	Generation string
}

type privateServiceRecord struct {
	OverlayURI  string
	TargetURI   string
	LocalTarget string
	PathClass   string
	Generation  string
}

type publicIngressRecord struct {
	EndpointRef string
	OverlayURI  string
	TargetURI   string
	LocalTarget string
	Endpoint    string
	Stable      bool
	PathClass   string
	Generation  string
	Hostname    string
}

type meshRuntimeRecord struct {
	TargetURI         string
	AgentReady        bool
	ControlPlaneReady bool
	AgentVersion      string
	ControlPlaneRef   string
	IngressClassName  string
	Generation        string
}

var ownershipStore = struct {
	sync.Mutex
	memberships map[string]membershipRecord
	byTarget    map[string]string
	meshEdges   map[string]meshEdge
	services    map[string]privateServiceRecord
	ingresses   map[string]publicIngressRecord
	runtimes    map[string]meshRuntimeRecord
}{
	memberships: make(map[string]membershipRecord),
	byTarget:    make(map[string]string),
	meshEdges:   make(map[string]meshEdge),
	services:    make(map[string]privateServiceRecord),
	ingresses:   make(map[string]publicIngressRecord),
	runtimes:    make(map[string]meshRuntimeRecord),
}

func reportTwoNodeReadiness(args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	peerURI := strings.TrimSpace(stringInput(args, "peerTargetUri", ""))
	if peerURI == "" {
		return nil, fmt.Errorf("peerTargetUri is required")
	}
	datastoreMode := strings.TrimSpace(stringInput(args, "datastoreMode", ""))
	if datastoreMode != "external-datastore" && datastoreMode != "embedded-etcd-two-node" {
		return nil, fmt.Errorf("datastoreMode must be external-datastore or embedded-etcd-two-node")
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	record, err := requireOwnedMembershipLocked(args, instanceURI.String())
	if err != nil {
		return nil, err
	}
	peerRef, ok := ownershipStore.byTarget[peerURI]
	if !ok {
		return nil, fmt.Errorf("peer target is not enrolled")
	}
	peer := ownershipStore.memberships[peerRef]
	forwardKey := meshKey(record.TargetURI, peer.TargetURI)
	reverseKey := meshKey(peer.TargetURI, record.TargetURI)
	forward := ownershipStore.meshEdges[forwardKey]
	reverse := ownershipStore.meshEdges[reverseKey]
	meshReady := forward.Ready && reverse.Ready
	var funnelStable *bool
	publicReady := false
	for _, ingress := range ownershipStore.ingresses {
		if ingress.Generation != providerGeneration {
			continue
		}
		if ingress.TargetURI == record.TargetURI || ingress.TargetURI == peer.TargetURI {
			publicReady = true
			stable := ingress.Stable
			funnelStable = &stable
		}
	}
	availabilityClass := "serving-continuity-only"
	recoveryPolicy := "acknowledge-single-member-reset"
	apiWriteReady := false
	durableReady := false
	if datastoreMode == "external-datastore" {
		if !boolInput(args, "datastoreEvidence", false) {
			return nil, fmt.Errorf("external-datastore mode requires datastoreEvidence=true with independent datastore proof")
		}
		availabilityClass = "control-plane-write-continuity"
		recoveryPolicy = "datastore-failover"
		apiWriteReady = meshReady
		durableReady = true
	}
	ready := meshReady && (datastoreMode != "external-datastore" || durableReady)
	out := overlayBase(record, map[string]any{
		"ready":             ready,
		"datastoreMode":     datastoreMode,
		"availabilityClass": availabilityClass,
		"recoveryPolicy":    recoveryPolicy,
		"membership":        map[string]any{"ready": true, "sourceUri": record.TargetURI, "destUri": peer.TargetURI, "generation": providerGeneration},
		"privateMesh":       map[string]any{"ready": meshReady, "pathClass": pathClassPrivateMesh, "sourceUri": record.TargetURI, "destUri": peer.TargetURI, "generation": providerGeneration},
		"publicIngress":     map[string]any{"ready": publicReady, "pathClass": pathClassPublicIngress, "generation": providerGeneration},
		"durableStore":      map[string]any{"ready": durableReady, "generation": providerGeneration},
		"apiWriteAvailable": map[string]any{"ready": apiWriteReady, "generation": providerGeneration},
	})
	if funnelStable != nil {
		out["funnelStable"] = *funnelStable
	}
	if !meshReady {
		out["error"] = "private mesh is not ready in both directions"
	}
	return structured(out)
}

func resetOwnershipStoreForTest() {
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	ownershipStore.memberships = make(map[string]membershipRecord)
	ownershipStore.byTarget = make(map[string]string)
	ownershipStore.meshEdges = make(map[string]meshEdge)
	ownershipStore.services = make(map[string]privateServiceRecord)
	ownershipStore.ingresses = make(map[string]publicIngressRecord)
	ownershipStore.runtimes = make(map[string]meshRuntimeRecord)
}

func requireHostAgentID(args map[string]any) (string, error) {
	hostID := strings.TrimSpace(stringInput(args, "hostAgentId", ""))
	if hostID == "" {
		return "", fmt.Errorf("hostAgentId is required and must be the exact opaque Host Agent identity")
	}
	if strings.Contains(hostID, ".") || strings.Contains(hostID, "/") || strings.Contains(hostID, ":") {
		return "", fmt.Errorf("hostAgentId must be an opaque Host Agent identity, not a network address")
	}
	return hostID, nil
}

func useFakeBackend() bool {
	backend := strings.TrimSpace(os.Getenv("OPUTE_TAILSCALE_BACKEND"))
	if backend == "" {
		return true
	}
	return strings.EqualFold(backend, "fake")
}

func normalizeSeamOperation(operation string) string {
	switch operation {
	case capabilitycontract.MeshMembershipEnrollOperation:
		return capabilitycontract.NetworkOverlayEnrollOperation
	case capabilitycontract.MeshMembershipStatusOperation:
		return capabilitycontract.NetworkOverlayProbeOperation
	case capabilitycontract.MeshMembershipLeaveOperation:
		return capabilitycontract.NetworkOverlayRemoveMembershipOperation
	case capabilitycontract.PrivateMeshEnsureOperation:
		return capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation
	case capabilitycontract.PrivateMeshEnsureServiceOperation:
		return capabilitycontract.NetworkOverlayEnsurePrivateServiceOperation
	case capabilitycontract.PrivateMeshProbeOperation:
		return capabilitycontract.NetworkOverlayProbeOperation
	case capabilitycontract.PublicIngressEnsureOperation:
		return capabilitycontract.NetworkOverlayEnsurePublicIngressOperation
	case capabilitycontract.PublicIngressPromoteOperation:
		return capabilitycontract.NetworkOverlayPromotePublicIngressOperation
	case capabilitycontract.PublicIngressProbeOperation:
		return capabilitycontract.NetworkOverlayProbeOperation
	default:
		return operation
	}
}

func dispatchOverlayOperation(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	original := operation
	if args == nil {
		args = map[string]any{}
	}
	switch original {
	case capabilitycontract.MeshRuntimeValidateOperation,
		capabilitycontract.MeshRuntimeEnsureAgentOperation,
		capabilitycontract.MeshRuntimeEnsureControlPlaneOperation,
		capabilitycontract.MeshRuntimeStatusOperation:
		if useFakeBackend() {
			return dispatchFakeMeshRuntime(ctx, original, args)
		}
		return dispatchLiveMeshRuntime(ctx, original, args)
	}
	operation = normalizeSeamOperation(operation)
	switch original {
	case capabilitycontract.MeshMembershipStatusOperation:
		if _, ok := args["pathClass"]; !ok {
			args["pathClass"] = "private-mesh"
		}
	case capabilitycontract.PrivateMeshProbeOperation:
		args["pathClass"] = "private-mesh"
	case capabilitycontract.PublicIngressProbeOperation:
		args["pathClass"] = "public-ingress"
	}
	if useFakeBackend() {
		return dispatchFakeOverlayOperation(ctx, operation, args)
	}
	return dispatchLiveOverlayOperation(ctx, operation, args)
}

func dispatchFakeOverlayOperation(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	_ = ctx
	switch operation {
	case capabilitycontract.NetworkOverlayValidateOperation:
		return validateOverlay(args)
	case capabilitycontract.NetworkOverlayPrepareMembershipOperation:
		return prepareMembership(args)
	case capabilitycontract.NetworkOverlayAttachTargetOperation:
		return attachTarget(args)
	case capabilitycontract.NetworkOverlayEnrollOperation:
		return enrollOverlay(args)
	case capabilitycontract.NetworkOverlayProbeReachabilityOperation:
		return probeReachability(args)
	case capabilitycontract.NetworkOverlayProbeOperation:
		return probePathAware(args)
	case capabilitycontract.NetworkOverlayReportTwoNodeReadinessOperation:
		return reportTwoNodeReadiness(args)
	case capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation:
		return ensurePrivateMesh(args)
	case capabilitycontract.NetworkOverlayEnsurePrivateServiceOperation:
		return ensurePrivateService(args)
	case capabilitycontract.NetworkOverlayEnsurePublicIngressOperation:
		return ensurePublicIngress(args)
	case capabilitycontract.NetworkOverlayEnsureHAEndpointOperation:
		return ensurePublicIngress(args)
	case capabilitycontract.NetworkOverlayPromotePublicIngressOperation:
		return promotePublicIngress(args)
	case capabilitycontract.NetworkOverlayRemoveHAEndpointOperation:
		return removeHAEndpoint(args)
	case capabilitycontract.NetworkOverlayRemoveMembershipOperation:
		return removeMembership(args)
	default:
		return nil, fmt.Errorf("unknown network overlay operation %q", operation)
	}
}

func validateOverlay(args map[string]any) (*mcp.CallToolResult, error) {
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
	ownershipStore.Lock()
	nodeCount := len(ownershipStore.memberships)
	ownershipStore.Unlock()
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        providerName,
		"generation":      providerGeneration,
		"targetUri":       instanceURI.String(),
		"nodeCount":       nodeCount,
		"credentialKind":  kind,
	})
}

func prepareMembership(args map[string]any) (*mcp.CallToolResult, error) {
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
	record, err := upsertMembership(instanceURI.String(), name, false)
	if err != nil {
		return nil, err
	}
	return structured(overlayBase(record, map[string]any{
		"ready":         true,
		"membershipRef": record.Ref,
		"nodeId":        record.NodeID,
		"nodeName":      record.NodeName,
		"overlayUri":    record.OverlayURI,
	}))
}

func attachTarget(args map[string]any) (*mcp.CallToolResult, error) {
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
	defer ownershipStore.Unlock()
	record, ok := ownershipStore.memberships[ref]
	if !ok {
		return nil, fmt.Errorf("network overlay membership reference is unknown or expired")
	}
	if record.Generation != providerGeneration {
		return nil, fmt.Errorf("stale provider generation for membership")
	}
	if record.TargetURI != instanceURI.String() {
		return nil, fmt.Errorf("network overlay membership is bound to another instance")
	}
	record.Attached = true
	if record.MeshIP == "" {
		record.MeshIP = allocateMeshIP(record.TargetURI)
		record.MeshInterface = "tailscale0"
	}
	ownershipStore.memberships[ref] = record
	ownershipStore.byTarget[record.TargetURI] = ref
	return structured(overlayBase(record, map[string]any{
		"ready":         true,
		"membershipRef": record.Ref,
		"nodeId":        record.NodeID,
		"meshIp":        record.MeshIP,
		"meshInterface": record.MeshInterface,
		"overlayUri":    record.OverlayURI,
	}))
}

func enrollOverlay(args map[string]any) (*mcp.CallToolResult, error) {
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
	if err := requireMeshAgentReady(instanceURI.String()); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(stringInput(args, "name", ""))
	if name == "" {
		name = "node"
	}
	refHint := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	if existingRef, ok := ownershipStore.byTarget[instanceURI.String()]; ok {
		record := ownershipStore.memberships[existingRef]
		if record.Generation != providerGeneration {
			return nil, fmt.Errorf("stale provider generation for membership")
		}
		if refHint != "" && refHint != existingRef {
			return nil, fmt.Errorf("membershipRef does not match owned enrollment for target")
		}
		record.Attached = true
		if record.MeshIP == "" {
			record.MeshIP = allocateMeshIP(record.TargetURI)
			record.MeshInterface = "tailscale0"
		}
		if name != "" && record.NodeName == "" {
			record.NodeName = name
		}
		ownershipStore.memberships[existingRef] = record
		out := overlayBase(record, map[string]any{
			"ready":         true,
			"membershipRef": record.Ref,
			"nodeId":        record.NodeID,
			"nodeName":      record.NodeName,
			"meshIp":        record.MeshIP,
			"meshInterface": record.MeshInterface,
			"overlayUri":    record.OverlayURI,
		})
		return structured(out)
	}
	record, err := upsertMembershipLocked(instanceURI.String(), name, true)
	if err != nil {
		return nil, err
	}
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

func probeReachability(args map[string]any) (*mcp.CallToolResult, error) {
	args = cloneArgs(args)
	if stringInput(args, "pathClass", "") == "" {
		args["pathClass"] = pathClassPrivateMesh
	}
	return probePathAware(args)
}

func probePathAware(args map[string]any) (*mcp.CallToolResult, error) {
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
	defer ownershipStore.Unlock()
	ref, ok := ownershipStore.byTarget[instanceURI.String()]
	if !ok {
		return nil, fmt.Errorf("target is not enrolled in the overlay")
	}
	record := ownershipStore.memberships[ref]
	if record.Generation != providerGeneration {
		return nil, fmt.Errorf("stale provider generation for membership")
	}
	result := overlayBase(record, map[string]any{
		"pathClass": pathClass,
		"probe":     map[string]any{"pathClass": pathClass},
	})
	switch pathClass {
	case pathClassPrivateMesh:
		if peerMeshIP == "" {
			return nil, fmt.Errorf("peerMeshIp is required for private-mesh probes")
		}
		if net.ParseIP(peerMeshIP) == nil {
			return nil, fmt.Errorf("peerMeshIp must be an IPv4 address")
		}
		ready := privateMeshReadyLocked(instanceURI.String(), peerMeshIP)
		result["ready"] = ready
		result["peerMeshIp"] = peerMeshIP
		probe := result["probe"].(map[string]any)
		probe["ready"] = ready
		probe["peerMeshIp"] = peerMeshIP
		if !ready {
			// Public ingress presence must never satisfy a private probe.
			if publicReadyLocked(instanceURI.String()) {
				probe["publicIngressPresent"] = true
				probe["privateSatisfiedByPublic"] = false
			}
			result["error"] = "private-mesh peer reachability is not ready"
		}
		return structured(result)
	case pathClassPublicIngress:
		ready := publicReadyLocked(instanceURI.String())
		result["ready"] = ready
		result["stable"] = false
		if ready {
			for _, ingress := range ownershipStore.ingresses {
				if ingress.TargetURI == instanceURI.String() && ingress.Generation == providerGeneration {
					result["endpoint"] = ingress.Endpoint
					result["stable"] = ingress.Stable
					break
				}
			}
		}
		probe := result["probe"].(map[string]any)
		probe["ready"] = ready
		if !ready {
			result["error"] = "public-ingress endpoint is not ready"
		}
		return structured(result)
	default:
		return nil, fmt.Errorf("unsupported pathClass %q", pathClass)
	}
}

func ensurePrivateMesh(args map[string]any) (*mcp.CallToolResult, error) {
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
	defer ownershipStore.Unlock()
	sourceRef, ok := ownershipStore.byTarget[instanceURI.String()]
	if !ok {
		return nil, fmt.Errorf("source target is not enrolled")
	}
	source := ownershipStore.memberships[sourceRef]
	if source.Generation != providerGeneration || !source.Attached {
		return nil, fmt.Errorf("source membership is not attached for this generation")
	}
	peerRef, peerOK := ownershipStore.byTarget[peerURI.String()]
	if !peerOK {
		return nil, fmt.Errorf("peer target is not enrolled")
	}
	peer := ownershipStore.memberships[peerRef]
	if peer.Generation != providerGeneration || !peer.Attached {
		return nil, fmt.Errorf("peer membership is not attached for this generation")
	}
	if peer.MeshIP != peerMeshIP {
		return nil, fmt.Errorf("ambiguous peer: peerMeshIp does not match enrolled peer")
	}
	if countPeersWithMeshIPLocked(peerMeshIP) != 1 {
		return nil, fmt.Errorf("ambiguous peer mesh address")
	}
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
	}))
}

func ensurePrivateService(args map[string]any) (*mcp.CallToolResult, error) {
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
	defer ownershipStore.Unlock()
	record, err := requireOwnedMembershipLocked(args, instanceURI.String())
	if err != nil {
		return nil, err
	}
	svc := privateServiceRecord{
		OverlayURI:  record.OverlayURI,
		TargetURI:   record.TargetURI,
		LocalTarget: localTarget,
		PathClass:   pathClassPrivateMesh,
		Generation:  providerGeneration,
	}
	ownershipStore.services[record.OverlayURI] = svc
	return structured(overlayBase(record, map[string]any{
		"ready":     true,
		"pathClass": pathClassPrivateMesh,
		"endpoint":  localTarget,
		"stable":    true,
	}))
}

func ensurePublicIngress(args map[string]any) (*mcp.CallToolResult, error) {
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
	defer ownershipStore.Unlock()
	record, err := requireOwnedMembershipLocked(args, instanceURI.String())
	if err != nil {
		return nil, err
	}
	operatorMode := boolInput(args, "operatorMode", false)
	if err := requireOperatorStableEvidence(args); err != nil {
		return nil, err
	}
	hostname := firstNonEmpty(stringInput(args, "hostname", ""), "node.ingress.example")
	endpointRef, err := newOpaqueRef("ingress")
	if err != nil {
		return nil, err
	}
	endpoint := "https://" + hostname
	ingress := publicIngressRecord{
		EndpointRef: endpointRef,
		OverlayURI:  record.OverlayURI,
		TargetURI:   record.TargetURI,
		LocalTarget: localTarget,
		Endpoint:    endpoint,
		Stable:      operatorMode,
		PathClass:   pathClassPublicIngress,
		Generation:  providerGeneration,
		Hostname:    hostname,
	}
	ownershipStore.ingresses[endpointRef] = ingress
	return structured(overlayBase(record, map[string]any{
		"ready":       true,
		"pathClass":   pathClassPublicIngress,
		"endpoint":    endpoint,
		"endpointRef": endpointRef,
		"stable":      ingress.Stable,
	}))
}

func promotePublicIngress(args map[string]any) (*mcp.CallToolResult, error) {
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
	ingress.TargetURI = record.TargetURI
	ingress.OverlayURI = record.OverlayURI
	ingress.Endpoint = "https://" + firstNonEmpty(ingress.Hostname, "node.ingress.example") + "/promoted"
	ingress.Stable = operatorMode
	ownershipStore.ingresses[endpointRef] = ingress
	return structured(overlayBase(record, map[string]any{
		"ready":       true,
		"pathClass":   pathClassPublicIngress,
		"endpoint":    ingress.Endpoint,
		"endpointRef": endpointRef,
		"stable":      ingress.Stable,
	}))
}

func removeHAEndpoint(args map[string]any) (*mcp.CallToolResult, error) {
	endpointRef := strings.TrimSpace(stringInput(args, "endpointRef", ""))
	if endpointRef == "" {
		return nil, fmt.Errorf("endpointRef is required")
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	ingress, ok := ownershipStore.ingresses[endpointRef]
	if !ok {
		return structured(map[string]any{
			"contractVersion": capabilitycontract.NetworkOverlay,
			"ready":           true,
			"provider":        providerName,
			"generation":      providerGeneration,
			"deleted":         true,
			"targetUri":       "vm:local:placeholder",
		})
	}
	if ingress.Generation != providerGeneration {
		return nil, fmt.Errorf("refusing to delete endpoint owned by a foreign generation")
	}
	delete(ownershipStore.ingresses, endpointRef)
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        providerName,
		"generation":      providerGeneration,
		"deleted":         true,
		"targetUri":       ingress.TargetURI,
		"endpointRef":     endpointRef,
	})
}

func removeMembership(args map[string]any) (*mcp.CallToolResult, error) {
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	if ref == "" {
		return nil, fmt.Errorf("membershipRef is required")
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	record, ok := ownershipStore.memberships[ref]
	if !ok {
		return nil, fmt.Errorf("network overlay membership reference is unknown or expired")
	}
	if record.Generation != providerGeneration {
		return nil, fmt.Errorf("refusing to remove membership owned by a foreign generation")
	}
	if record.TargetURI != instanceURI.String() {
		return nil, fmt.Errorf("network overlay membership is bound to another instance")
	}
	deleteOwnedForTargetLocked(record.TargetURI, ref)
	return structured(map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"ready":           true,
		"provider":        providerName,
		"generation":      providerGeneration,
		"deleted":         true,
		"membershipRef":   ref,
		"targetUri":       instanceURI.String(),
	})
}

func finalizeOwnedTeardown(inputs map[string]any) error {
	if !useFakeBackend() {
		return liveFinalizeOwnedTeardown(context.Background(), inputs)
	}
	generation := firstNonEmpty(stringInput(inputs, "generation", ""), providerGeneration)
	if generation != providerGeneration {
		return fmt.Errorf("refusing teardown for foreign provider generation")
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

func deleteOwnedForTargetLocked(targetURI, ref string) {
	delete(ownershipStore.memberships, ref)
	if ownershipStore.byTarget[targetURI] == ref {
		delete(ownershipStore.byTarget, targetURI)
	}
	for key, edge := range ownershipStore.meshEdges {
		if edge.Generation == providerGeneration && (edge.SourceURI == targetURI || edge.PeerURI == targetURI) {
			delete(ownershipStore.meshEdges, key)
		}
	}
	for key, svc := range ownershipStore.services {
		if svc.Generation == providerGeneration && svc.TargetURI == targetURI {
			delete(ownershipStore.services, key)
		}
	}
	for key, ingress := range ownershipStore.ingresses {
		if ingress.Generation == providerGeneration && ingress.TargetURI == targetURI {
			delete(ownershipStore.ingresses, key)
		}
	}
}

func upsertMembership(targetURI, name string, attached bool) (membershipRecord, error) {
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	return upsertMembershipLocked(targetURI, name, attached)
}

func upsertMembershipLocked(targetURI, name string, attached bool) (membershipRecord, error) {
	if existingRef, ok := ownershipStore.byTarget[targetURI]; ok {
		record := ownershipStore.memberships[existingRef]
		if record.Generation != providerGeneration {
			return membershipRecord{}, fmt.Errorf("target already owned by a foreign generation")
		}
		if name != "" {
			record.NodeName = name
		}
		if attached {
			record.Attached = true
			if record.MeshIP == "" {
				record.MeshIP = allocateMeshIP(targetURI)
				record.MeshInterface = "tailscale0"
			}
		}
		ownershipStore.memberships[existingRef] = record
		return record, nil
	}
	ref, err := newOpaqueRef("mesh")
	if err != nil {
		return membershipRecord{}, err
	}
	nodeID, err := newOpaqueRef("node")
	if err != nil {
		return membershipRecord{}, err
	}
	sum := sha256.Sum256([]byte(targetURI))
	overlayURI := "overlay:local:" + hex.EncodeToString(sum[:8])
	record := membershipRecord{
		Ref:        ref,
		TargetURI:  targetURI,
		OverlayURI: overlayURI,
		NodeID:     nodeID,
		NodeName:   name,
		Generation: providerGeneration,
		Attached:   attached,
	}
	if attached {
		record.MeshIP = allocateMeshIP(targetURI)
		record.MeshInterface = "tailscale0"
	}
	ownershipStore.memberships[ref] = record
	ownershipStore.byTarget[targetURI] = ref
	return record, nil
}

func requireOwnedMembershipLocked(args map[string]any, targetURI string) (membershipRecord, error) {
	refHint := strings.TrimSpace(stringInput(args, "membershipRef", ""))
	ref, ok := ownershipStore.byTarget[targetURI]
	if !ok {
		return membershipRecord{}, fmt.Errorf("target is not enrolled")
	}
	if refHint != "" && refHint != ref {
		return membershipRecord{}, fmt.Errorf("membershipRef does not match owned enrollment")
	}
	record := ownershipStore.memberships[ref]
	if record.Generation != providerGeneration {
		return membershipRecord{}, fmt.Errorf("stale provider generation for membership")
	}
	if !record.Attached {
		return membershipRecord{}, fmt.Errorf("membership is not attached")
	}
	return record, nil
}

func privateMeshReadyLocked(sourceURI, peerMeshIP string) bool {
	for _, edge := range ownershipStore.meshEdges {
		if edge.Generation == providerGeneration && edge.SourceURI == sourceURI && edge.PeerMeshIP == peerMeshIP && edge.Ready {
			// require reverse edge too
			reverse := ownershipStore.meshEdges[meshKey(edge.PeerURI, edge.SourceURI)]
			return reverse.Ready && reverse.Generation == providerGeneration
		}
	}
	return false
}

func publicReadyLocked(targetURI string) bool {
	for _, ingress := range ownershipStore.ingresses {
		if ingress.TargetURI == targetURI && ingress.Generation == providerGeneration && ingress.Endpoint != "" {
			return true
		}
	}
	return false
}

func countPeersWithMeshIPLocked(meshIP string) int {
	count := 0
	for _, record := range ownershipStore.memberships {
		if record.MeshIP == meshIP && record.Generation == providerGeneration {
			count++
		}
	}
	return count
}

func meshKey(source, peer string) string { return source + "->" + peer }

func allocateMeshIP(targetURI string) string {
	sum := sha256.Sum256([]byte(targetURI))
	return fmt.Sprintf("100.64.%d.%d", int(sum[0])%250+1, int(sum[1])%250+1)
}

func overlayBase(record membershipRecord, extra map[string]any) map[string]any {
	out := map[string]any{
		"contractVersion": capabilitycontract.NetworkOverlay,
		"provider":        providerName,
		"generation":      providerGeneration,
		"targetUri":       record.TargetURI,
		"overlayUri":      record.OverlayURI,
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func parseTargetURI(args map[string]any) (resourceid.URI, error) {
	instanceURI, err := resourceid.Parse(strings.TrimSpace(stringInput(args, "targetUri", "")))
	if err != nil {
		return resourceid.URI{}, fmt.Errorf("targetUri must be a canonical VM or container resource URI: %w", err)
	}
	if instanceURI.ResourceType != resourceid.TypeVM && instanceURI.ResourceType != resourceid.TypeContainer {
		return resourceid.URI{}, fmt.Errorf("targetUri requires resource type %q or %q, got %q", resourceid.TypeVM, resourceid.TypeContainer, instanceURI.ResourceType)
	}
	return instanceURI, nil
}

func requireCredentialKind(args map[string]any, expected string) (string, error) {
	kind := strings.TrimSpace(stringInput(args, "credentialKind", ""))
	if kind == "" {
		if expected != "" {
			return "", fmt.Errorf("credentialKind is required")
		}
		// validate may omit kind when no credential fields are supplied
		if hasAnyCredential(args) {
			return "", fmt.Errorf("credentialKind is required when a credential is supplied")
		}
		return "", nil
	}
	if kind != credKindAPIKey && kind != credKindAuthKey {
		return "", fmt.Errorf("unsupported credentialKind %q", kind)
	}
	if expected != "" && kind != expected {
		return "", fmt.Errorf("credential kind mismatch: operation requires %s", expected)
	}
	return kind, nil
}

func assertCredentialPresence(args map[string]any, kind string) error {
	if kind == "" {
		return nil
	}
	cred := strings.TrimSpace(stringInput(args, "credential", ""))
	apiKey := strings.TrimSpace(stringInput(args, "apiKey", ""))
	authKey := strings.TrimSpace(stringInput(args, "authKey", ""))
	switch kind {
	case credKindAPIKey:
		if authKey != "" && apiKey == "" && cred == "" {
			return fmt.Errorf("credential kind mismatch: api-key required, auth-key supplied")
		}
		if apiKey == "" && cred == "" && authKey == "" {
			// fake backend allows absence for read validate after kind declared
			return nil
		}
		if authKey != "" && apiKey == "" {
			return fmt.Errorf("credential kind mismatch: api-key required, auth-key supplied")
		}
	case credKindAuthKey:
		if apiKey != "" && authKey == "" && cred == "" {
			return fmt.Errorf("credential kind mismatch: auth-key required, api-key supplied")
		}
	}
	return nil
}

func hasAnyCredential(args map[string]any) bool {
	return stringInput(args, "credential", "") != "" || stringInput(args, "apiKey", "") != "" || stringInput(args, "authKey", "") != ""
}

func rejectControlPlanePublicTarget(localTarget, targetKind string) error {
	combined := strings.ToLower(localTarget + " " + targetKind)
	for _, forbidden := range []string{"etcd", "k8s-api", "kubernetes-api", "cni", "host-admin"} {
		if strings.Contains(combined, forbidden) {
			return fmt.Errorf("public ingress rejects control-plane target %q", forbidden)
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(localTarget)), "http://") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(localTarget)), "https://") {
		if strings.Contains(combined, "remote://") {
			return fmt.Errorf("public ingress rejects remote arbitrary targets")
		}
		return nil
	}
	if strings.Contains(combined, "remote://") {
		return fmt.Errorf("public ingress rejects remote arbitrary targets")
	}
	return nil
}

func newOpaqueRef(prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create opaque reference: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(raw[:]), nil
}

func cloneArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

// seedForeignMembershipForTest inserts a membership owned by another generation.
func seedForeignMembershipForTest(targetURI, ref string) {
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	ownershipStore.memberships[ref] = membershipRecord{
		Ref: ref, TargetURI: targetURI, OverlayURI: "overlay:local:foreign", NodeID: "node-foreign",
		NodeName: "foreign", MeshIP: "100.64.9.9", MeshInterface: "tailscale0", Generation: "foreign@0.0.0", Attached: true,
	}
	ownershipStore.byTarget[targetURI] = ref
}


func dispatchFakeMeshRuntime(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	_ = ctx
	switch operation {
	case capabilitycontract.MeshRuntimeValidateOperation, capabilitycontract.MeshRuntimeStatusOperation:
		return fakeMeshRuntimeStatus(args)
	case capabilitycontract.MeshRuntimeEnsureAgentOperation:
		return fakeEnsureMeshAgent(args)
	case capabilitycontract.MeshRuntimeEnsureControlPlaneOperation:
		return fakeEnsureMeshControlPlane(args)
	default:
		return nil, fmt.Errorf("unknown mesh-runtime operation %q", operation)
	}
}

func fakeMeshRuntimeStatus(args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	rec := ownershipStore.runtimes[instanceURI.String()]
	ready := rec.AgentReady && (rec.ControlPlaneReady || !boolInput(args, "requireControlPlane", false))
	if !boolInput(args, "requireControlPlane", false) {
		ready = rec.AgentReady
	}
	return structured(meshRuntimeBase(instanceURI.String(), rec, map[string]any{
		"ready": ready,
	}))
}

func fakeEnsureMeshAgent(args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	rec := ownershipStore.runtimes[instanceURI.String()]
	rec.TargetURI = instanceURI.String()
	rec.AgentReady = true
	rec.AgentVersion = "fake-tailscale"
	rec.Generation = providerGeneration
	ownershipStore.runtimes[instanceURI.String()] = rec
	return structured(meshRuntimeBase(instanceURI.String(), rec, map[string]any{
		"ready": true,
	}))
}

func fakeEnsureMeshControlPlane(args map[string]any) (*mcp.CallToolResult, error) {
	if _, err := requireHostAgentID(args); err != nil {
		return nil, err
	}
	instanceURI, err := parseTargetURI(args)
	if err != nil {
		return nil, err
	}
	ingressClass := firstNonEmpty(stringInput(args, "ingressClassName", ""), "tailscale")
	evidence := strings.TrimSpace(stringInput(args, "operatorEvidence", ""))
	operatorMode := boolInput(args, "operatorMode", true)
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	rec := ownershipStore.runtimes[instanceURI.String()]
	if !rec.AgentReady {
		return nil, fmt.Errorf("mesh-runtime.ensure-control-plane requires mesh-runtime.ensure-agent first")
	}
	if operatorMode && evidence == "" && ingressClass != "tailscale" {
		return nil, fmt.Errorf("operatorMode requires operatorEvidence or ingressClassName=tailscale")
	}
	rec.TargetURI = instanceURI.String()
	rec.ControlPlaneReady = true
	rec.IngressClassName = ingressClass
	rec.ControlPlaneRef = firstNonEmpty(stringInput(args, "controlPlaneRef", ""), "fake-tailscale-operator")
	rec.Generation = providerGeneration
	ownershipStore.runtimes[instanceURI.String()] = rec
	return structured(meshRuntimeBase(instanceURI.String(), rec, map[string]any{
		"ready": true,
	}))
}

func meshRuntimeBase(targetURI string, rec meshRuntimeRecord, extra map[string]any) map[string]any {
	out := map[string]any{
		"contractVersion":   capabilitycontract.MeshRuntime,
		"provider":          providerPluginID,
		"targetUri":         targetURI,
		"agentReady":        rec.AgentReady,
		"controlPlaneReady": rec.ControlPlaneReady,
		"agentVersion":      rec.AgentVersion,
		"controlPlaneRef":   rec.ControlPlaneRef,
		"ingressClassName":  rec.IngressClassName,
		"generation":        firstNonEmpty(rec.Generation, providerGeneration),
		"probe": map[string]any{
			"agentReady":        rec.AgentReady,
			"controlPlaneReady": rec.ControlPlaneReady,
		},
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func requireMeshAgentReady(targetURI string) error {
	ownershipStore.Lock()
	defer ownershipStore.Unlock()
	rec, ok := ownershipStore.runtimes[targetURI]
	if !ok || !rec.AgentReady {
		return fmt.Errorf("mesh agent not ready on %s; call opute.capability.mesh-runtime.ensure-agent first", targetURI)
	}
	return nil
}
