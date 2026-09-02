package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// membershipExpectationSchema contains only provider-neutral observations and
// expectations. Join material and endpoint credentials are intentionally not
// part of this schema.
func membershipExpectationSchema() map[string]any {
	return map[string]any{
		"expectedRole":        map[string]any{"type": "string", "enum": []string{"server"}},
		"expectedServers":     map[string]any{"type": "integer", "minimum": 1, "maximum": 9},
		"requireEmbeddedEtcd": map[string]any{"type": "boolean"},
		"endpoint":            map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
	}
}

type membershipObservation struct {
	TargetURI            string           `json:"targetUri"`
	ClusterIdentity      string           `json:"clusterIdentity,omitempty"`
	ClusterIdentityKnown bool             `json:"clusterIdentityKnown"`
	DatastoreMode        string           `json:"datastoreMode"`
	Version              string           `json:"version"`
	Hostname             string           `json:"hostname,omitempty"`
	Nodes                []map[string]any `json:"nodes"`
	ServerCount          int              `json:"serverCount"`
	ReadyServers         int              `json:"readyServers"`
	Ready                bool             `json:"ready"`
	EndpointConfigured   bool             `json:"endpointConfigured"`
}

type joinInvitation struct {
	sourceTargetURI   string
	sourceInstance    string
	destinationHostID string
	clusterIdentity   string
	k3sVersion        string
	joinToken         string
	expiresAt         time.Time
	redeemed          bool
	consumed          bool
}

var joinInvitations = struct {
	sync.Mutex
	items map[string]joinInvitation
}{items: make(map[string]joinInvitation)}

var joinReceiverKey = struct {
	sync.Once
	key *rsa.PrivateKey
	err error
}{}

var haEndpointHTTPClient = &http.Client{Timeout: 20 * time.Second}

type sealedJoinMaterial struct {
	RedemptionRef     string `json:"redemptionRef"`
	SourceTargetURI   string `json:"sourceTargetUri"`
	DestinationHostID string `json:"destinationHostId"`
	ClusterIdentity   string `json:"clusterIdentity"`
	K3sVersion        string `json:"k3sVersion"`
	JoinToken         string `json:"joinToken"`
	ExpiresAt         string `json:"expiresAt"`
}

type sealedJoinEnvelope struct {
	Key        string `json:"key"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func getJoinReceiverKey(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	key, err := currentJoinReceiverKey()
	if err != nil {
		return nil, fmt.Errorf("create join receiver key: %w", err)
	}
	der := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	digest := sha256.Sum256(der)
	return structured(map[string]any{
		"targetUri":        stringInput(args, "targetUri"),
		"publicKey":        base64.RawURLEncoding.EncodeToString(der),
		"fingerprint":      "sha256:" + hex.EncodeToString(digest[:]),
		"keyAlgorithm":     "RSA-OAEP-SHA256",
		"materialDelivery": "sealed-transient-handoff",
	})
}

func inspectMembership(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	observation, err := readMembership(ctx, args)
	if err != nil {
		return nil, err
	}
	if expectedRole := stringInput(args, "expectedRole"); expectedRole != "" && expectedRole != "server" {
		return nil, fmt.Errorf("unsupported expectedRole %q", expectedRole)
	}
	if expectedServers := integerInput(args, "expectedServers"); expectedServers > 0 && observation.ServerCount < expectedServers {
		return nil, fmt.Errorf("K3s membership has %d server(s), expected at least %d", observation.ServerCount, expectedServers)
	}
	if boolInput(args, "requireEmbeddedEtcd") && observation.DatastoreMode != "embedded-etcd" {
		return nil, fmt.Errorf("K3s target is not using embedded-etcd (observed %s)", observation.DatastoreMode)
	}
	if endpoint := stringInput(args, "endpoint"); endpoint != "" {
		if err := validateEndpoint(endpoint); err != nil {
			return nil, err
		}
		observation.EndpointConfigured = true
	}
	return structured(observation)
}

func prepareHA(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	args = cloneMap(args)
	args["requireEmbeddedEtcd"] = true
	observation, err := readMembership(ctx, args)
	if err != nil {
		return nil, err
	}
	if observation.DatastoreMode != "embedded-etcd" {
		return nil, fmt.Errorf("server promotion requires embedded-etcd; observed %s", observation.DatastoreMode)
	}
	if observation.ServerCount < 1 {
		return nil, fmt.Errorf("K3s target has no ready server membership")
	}
	if endpoint := stringInput(args, "endpoint"); endpoint != "" {
		if err := validateEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	expectedServers := integerInput(args, "expectedServers")
	if expectedServers == 0 {
		expectedServers = 2
	}
	return structured(map[string]any{
		"targetUri":               observation.TargetURI,
		"clusterIdentity":         observation.ClusterIdentity,
		"version":                 observation.Version,
		"datastoreMode":           observation.DatastoreMode,
		"currentServerCount":      observation.ServerCount,
		"expectedServerCount":     expectedServers,
		"readyForServerPromotion": true,
		"endpointConfigured":      observation.EndpointConfigured,
	})
}

func prepareJoin(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	destination := stringInput(args, "destinationHostId")
	if destination == "" {
		return nil, fmt.Errorf("destinationHostId is required")
	}
	role := firstNonEmpty(stringInput(args, "role"), "server")
	if role != "server" {
		return nil, fmt.Errorf("only server joins are supported")
	}
	observation, err := readMembership(ctx, args)
	if err != nil {
		return nil, err
	}
	if observation.DatastoreMode != "embedded-etcd" {
		return nil, fmt.Errorf("server join requires embedded-etcd; observed %s", observation.DatastoreMode)
	}
	instance := stringInput(args, "providerInstanceName")
	tokenOutput, tokenErr := runCommand(ctx, []string{"exec", instance, "--", "cat", "/var/lib/rancher/k3s/server/token"}, nil)
	if tokenErr != nil {
		return nil, fmt.Errorf("read K3s join material for handshake: %w", tokenErr)
	}
	joinToken := strings.TrimSpace(string(tokenOutput))
	if joinToken == "" || strings.ContainsAny(joinToken, "\x00\r\n") {
		return nil, fmt.Errorf("K3s source returned empty or invalid join material")
	}
	expiresIn := integerInput(args, "expiresInMs")
	if expiresIn == 0 {
		expiresIn = 15 * 60 * 1000
	}
	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Millisecond).UTC()
	redemptionRef, err := newJoinRedemptionRef()
	if err != nil {
		return nil, fmt.Errorf("create join redemption reference: %w", err)
	}
	joinInvitations.Lock()
	joinInvitations.items[redemptionRef] = joinInvitation{
		sourceTargetURI:   observation.TargetURI,
		sourceInstance:    instance,
		destinationHostID: destination,
		clusterIdentity:   observation.ClusterIdentity,
		k3sVersion:        observation.Version,
		joinToken:         joinToken,
		expiresAt:         expiresAt,
	}
	joinInvitations.Unlock()
	result := map[string]any{
		"targetUri":           observation.TargetURI,
		"destinationHostId":   destination,
		"role":                role,
		"clusterIdentity":     observation.ClusterIdentity,
		"expiresAt":           expiresAt.Format(time.RFC3339Nano),
		"redemptionRef":       redemptionRef,
		"redemptionRequired":  true,
		"secretDelivery":      "host-agent-handshake",
		"joinMaterialExposed": false,
	}
	if publicKey := stringInput(args, "destinationPublicKey"); publicKey != "" {
		sealed, sealErr := sealJoinMaterial(publicKey, sealedJoinMaterial{
			RedemptionRef:     redemptionRef,
			SourceTargetURI:   observation.TargetURI,
			DestinationHostID: destination,
			ClusterIdentity:   observation.ClusterIdentity,
			K3sVersion:        observation.Version,
			JoinToken:         joinToken,
			ExpiresAt:         expiresAt.Format(time.RFC3339Nano),
		})
		if sealErr != nil {
			return nil, fmt.Errorf("seal join material for destination Host Agent: %w", sealErr)
		}
		result["sealedJoinMaterial"] = sealed
		result["joinMaterialExposed"] = false
	}
	return structured(result)
}

func currentJoinReceiverKey() (*rsa.PrivateKey, error) {
	joinReceiverKey.Do(func() {
		joinReceiverKey.key, joinReceiverKey.err = rsa.GenerateKey(rand.Reader, 2048)
	})
	return joinReceiverKey.key, joinReceiverKey.err
}

func sealJoinMaterial(publicKey string, material sealedJoinMaterial) (string, error) {
	der, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil {
		return "", fmt.Errorf("decode destination public key: %w", err)
	}
	key, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return "", fmt.Errorf("parse destination public key: %w", err)
	}
	payload, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("encode join material: %w", err)
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return "", fmt.Errorf("create join material key: %w", err)
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return "", fmt.Errorf("create join material cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create join material envelope: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("create join material nonce: %w", err)
	}
	encryptedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, key, dataKey, []byte("opute-k3s-join-v1"))
	if err != nil {
		return "", fmt.Errorf("encrypt join material key: %w", err)
	}
	envelope, err := json.Marshal(sealedJoinEnvelope{
		Key:        base64.RawURLEncoding.EncodeToString(encryptedKey),
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(gcm.Seal(nil, nonce, payload, nil)),
	})
	if err != nil {
		return "", fmt.Errorf("encode join material envelope: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(envelope), nil
}

func unsealJoinMaterial(sealed string) (sealedJoinMaterial, error) {
	envelopeBytes, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decode sealed join material: %w", err)
	}
	var envelope sealedJoinEnvelope
	if err := json.Unmarshal(envelopeBytes, &envelope); err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decode sealed join material envelope: %w", err)
	}
	encryptedKey, err := base64.RawURLEncoding.DecodeString(envelope.Key)
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decode sealed join material key: %w", err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decode sealed join material nonce: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decode sealed join material ciphertext: %w", err)
	}
	key, err := currentJoinReceiverKey()
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("load join receiver key: %w", err)
	}
	dataKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, encryptedKey, []byte("opute-k3s-join-v1"))
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decrypt sealed join material key: %w", err)
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("create sealed join material cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("create sealed join material envelope: %w", err)
	}
	payload, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decrypt sealed join material: %w", err)
	}
	var material sealedJoinMaterial
	if err := json.Unmarshal(payload, &material); err != nil {
		return sealedJoinMaterial{}, fmt.Errorf("decode sealed join material: %w", err)
	}
	return material, nil
}

// redeemJoin records the authenticated destination-side redemption. The
// source token remains memory-only and is made available to joinNode exactly
// once. A deployment with separate provider processes may satisfy this same
// contract by supplying the transient token from its Host Agent handshake.
func redeemJoin(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ref := stringInput(args, "redemptionRef")
	if ref == "" {
		return nil, fmt.Errorf("redemptionRef is required")
	}
	joinInvitations.Lock()
	defer joinInvitations.Unlock()
	invitation, ok := joinInvitations.items[ref]
	if !ok {
		sealed := stringInput(args, "sealedJoinMaterial")
		if sealed == "" {
			return nil, fmt.Errorf("join redemption reference is unknown or belongs to another Host Agent")
		}
		material, err := unsealJoinMaterial(sealed)
		if err != nil {
			return nil, err
		}
		if material.RedemptionRef != ref || material.SourceTargetURI == "" || material.JoinToken == "" || material.K3sVersion == "" {
			return nil, fmt.Errorf("sealed join material does not match the redemption reference")
		}
		if destination := stringInput(args, "destinationHostId"); destination == "" || destination != material.DestinationHostID {
			return nil, fmt.Errorf("sealed join material is bound to another destination Host Agent")
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, material.ExpiresAt)
		if err != nil || time.Now().After(expiresAt) {
			return nil, fmt.Errorf("sealed join material is expired")
		}
		if sourceTarget := stringInput(args, "sourceTargetUri"); sourceTarget != "" && sourceTarget != material.SourceTargetURI {
			return nil, fmt.Errorf("sealed join material is bound to another source target")
		}
		invitation = joinInvitation{
			sourceTargetURI:   material.SourceTargetURI,
			destinationHostID: material.DestinationHostID,
			clusterIdentity:   material.ClusterIdentity,
			k3sVersion:        material.K3sVersion,
			joinToken:         material.JoinToken,
			expiresAt:         expiresAt,
		}
		ok = true
	}
	if invitation.consumed || time.Now().After(invitation.expiresAt) {
		return nil, fmt.Errorf("join redemption reference is expired or already consumed")
	}
	if destination := stringInput(args, "destinationHostId"); destination != "" && destination != invitation.destinationHostID {
		return nil, fmt.Errorf("join redemption reference is bound to another destination Host Agent")
	}
	invitation.redeemed = true
	joinInvitations.items[ref] = invitation
	return structured(map[string]any{
		"redemptionRef":       ref,
		"redeemed":            true,
		"expiresAt":           invitation.expiresAt.Format(time.RFC3339Nano),
		"joinMaterialExposed": false,
	})
}

func joinNode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if stringInput(args, "role") != "server" {
		return nil, fmt.Errorf("join-node requires role=server")
	}
	if boolInput(args, "clusterInit") {
		return nil, fmt.Errorf("join-node refuses cluster-init=true")
	}
	if stringInput(args, "redemptionRef") == "" {
		return nil, fmt.Errorf("redemptionRef is required")
	}
	token, invitationRef, version, err := joinTokenForNode(args)
	if err != nil {
		return nil, err
	}
	if version == "" {
		return nil, fmt.Errorf("K3s version is required when join material comes from an external Host Agent handshake")
	}
	if strings.ContainsAny(version, "\x00\r\n ';&|`$()") {
		return nil, fmt.Errorf("K3s version contains unsafe characters")
	}
	address := stringInput(args, "serverAddress")
	if err := validateEndpoint(address); err != nil {
		return nil, err
	}
	instance := stringInput(args, "providerInstanceName")
	// Refuse a non-empty target before any installer side effect. The token is
	// supplied to the child shell only as a transient environment value and is
	// never included in the returned observation.
	installExec, err := k3sServerInstallExec(args, false)
	if err != nil {
		return nil, err
	}
	if err := installK3sServer(ctx, instance, version, installExec, address, token); err != nil {
		return nil, fmt.Errorf("join K3s server: %w", err)
	}
	if invitationRef != "" {
		joinInvitations.Lock()
		delete(joinInvitations.items, invitationRef)
		joinInvitations.Unlock()
	}
	observation, err := readMembership(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("verify joined K3s server: %w", err)
	}
	return structured(map[string]any{
		"targetUri":       observation.TargetURI,
		"joined":          true,
		"clusterIdentity": observation.ClusterIdentity,
		"version":         observation.Version,
		"datastoreMode":   observation.DatastoreMode,
		"serverCount":     observation.ServerCount,
		"readyServers":    observation.ReadyServers,
	})
}

func newJoinRedemptionRef() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "join-" + hex.EncodeToString(raw[:]), nil
}

func joinTokenForNode(args map[string]any) (string, string, string, error) {
	ref := stringInput(args, "redemptionRef")
	if ref == "" {
		return "", "", "", fmt.Errorf("redemptionRef is required")
	}
	provided := stringInput(args, "joinToken")
	providedVersion := stringInput(args, "version")
	joinInvitations.Lock()
	defer joinInvitations.Unlock()
	invitation, ok := joinInvitations.items[ref]
	if ok {
		if invitation.consumed || time.Now().After(invitation.expiresAt) {
			return "", "", "", fmt.Errorf("join redemption reference is expired or already consumed")
		}
		if !invitation.redeemed {
			return "", "", "", fmt.Errorf("join redemption reference has not been redeemed by the destination Host Agent")
		}
		if provided != "" && provided != invitation.joinToken {
			return "", "", "", fmt.Errorf("join token does not match the redeemed invitation")
		}
		if providedVersion != "" && providedVersion != invitation.k3sVersion {
			return "", "", "", fmt.Errorf("K3s version does not match the source invitation")
		}
		invitation.consumed = true
		joinInvitations.items[ref] = invitation
		return invitation.joinToken, ref, invitation.k3sVersion, nil
	}
	if provided == "" || strings.ContainsAny(provided, "\x00\r\n") {
		return "", "", "", fmt.Errorf("joinToken is required as transient secret material")
	}
	return provided, "", providedVersion, nil
}

func ensureHAEndpoint(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	endpoint := stringInput(args, "endpoint")
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	observation, err := readMembership(ctx, args)
	if err != nil {
		return nil, err
	}
	if observation.DatastoreMode != "embedded-etcd" {
		return nil, fmt.Errorf("stable HA endpoint requires embedded-etcd; observed %s", observation.DatastoreMode)
	}
	if observation.ReadyServers < 1 {
		return nil, fmt.Errorf("stable HA endpoint requires at least one ready server")
	}
	statusCode, err := probeHAEndpoint(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("probe stable HA endpoint: %w", err)
	}
	return structured(map[string]any{
		"targetUri":          observation.TargetURI,
		"endpoint":           strings.TrimRight(endpoint, "/"),
		"endpointAvailable":  true,
		"endpointStatusCode": statusCode,
		"clusterIdentity":    observation.ClusterIdentity,
		"serverCount":        observation.ServerCount,
		"readyServers":       observation.ReadyServers,
		"datastoreMode":      observation.DatastoreMode,
	})
}

// probeHAEndpoint runs from the provider host rather than from a K3s guest.
// Cloudflare Gateway may legitimately TLS-inspect guest egress with a
// workstation-only CA, while the public HA endpoint itself must still be
// verified with the host's normal certificate roots. The provider-owned
// overlay operation separately proves that every connector is installed in a
// guest; this probe proves the resulting public endpoint is reachable.
func probeHAEndpoint(ctx context.Context, endpoint string) (int, error) {
	return probeHAEndpointWithClient(ctx, endpoint, haEndpointHTTPClient)
}

func probeHAEndpointWithClient(ctx context.Context, endpoint string, client *http.Client) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/version", nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if !endpointHTTPStatusAcceptable(response.StatusCode) {
		return response.StatusCode, fmt.Errorf("stable HA endpoint returned invalid HTTP status %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

func endpointHTTPStatusAcceptable(statusCode int) bool {
	return (statusCode >= 200 && statusCode < 400) || statusCode == 401 || statusCode == 403
}

func removeNode(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if stringInput(args, "nodeName") == "" || stringInput(args, "role") != "server" {
		return nil, fmt.Errorf("remove-node requires nodeName and role=server")
	}
	return nil, fmt.Errorf("refusing server removal without source-cluster quorum proof")
}

func readMembership(ctx context.Context, args map[string]any) (membershipObservation, error) {
	if err := requireTarget(args); err != nil {
		return membershipObservation{}, err
	}
	instance := stringInput(args, "providerInstanceName")
	versionOutput, err := runCommand(ctx, []string{"exec", instance, "--", "k3s", "--version"}, nil)
	if err != nil {
		return membershipObservation{}, err
	}
	nodesOutput, err := runCommand(ctx, []string{"exec", instance, "--", "k3s", "kubectl", "get", "nodes", "-o", "json"}, nil)
	if err != nil {
		return membershipObservation{}, fmt.Errorf("read native K3s membership: %w", err)
	}
	nodes, err := parseNativeMembership(nodesOutput)
	if err != nil {
		return membershipObservation{}, err
	}
	tokenOutput, tokenErr := runCommand(ctx, []string{"exec", instance, "--", "sha256sum", "/var/lib/rancher/k3s/server/token"}, nil)
	identity := ""
	if tokenErr == nil {
		fields := strings.Fields(string(tokenOutput))
		if len(fields) > 0 && len(fields[0]) == sha256.Size*2 {
			identity = "sha256:" + fields[0]
		}
	}
	datastoreMode := "unknown"
	if _, etcdErr := runCommand(ctx, []string{"exec", instance, "--", "test", "-d", "/var/lib/rancher/k3s/server/db/etcd/member"}, nil); etcdErr == nil {
		datastoreMode = "embedded-etcd"
	} else if _, sqliteErr := runCommand(ctx, []string{"exec", instance, "--", "test", "-f", "/var/lib/rancher/k3s/server/db/state.db"}, nil); sqliteErr == nil {
		datastoreMode = "sqlite"
	}
	hostnameOutput, _ := runCommand(ctx, []string{"exec", instance, "--", "hostname"}, nil)
	readyServers := 0
	serverCount := 0
	for _, node := range nodes {
		roles, _ := node["roles"].([]string)
		isServer := len(roles) == 0 || containsString(roles, "control-plane") || containsString(roles, "etcd")
		if isServer {
			serverCount++
			if node["ready"] == true {
				readyServers++
			}
		}
	}
	return membershipObservation{
		TargetURI:            stringInput(args, "targetUri"),
		ClusterIdentity:      identity,
		ClusterIdentityKnown: identity != "",
		DatastoreMode:        datastoreMode,
		Version:              parseK3sVersion(string(versionOutput)),
		Hostname:             strings.TrimSpace(string(hostnameOutput)),
		Nodes:                nodes,
		ServerCount:          serverCount,
		ReadyServers:         readyServers,
		Ready:                readyServers > 0,
		EndpointConfigured:   stringInput(args, "endpoint") != "",
	}, nil
}

func parseNativeMembership(raw []byte) ([]map[string]any, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("invalid native K3s membership response: %w", err)
	}
	nodes := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		if strings.TrimSpace(item.Metadata.Name) == "" {
			return nil, fmt.Errorf("native K3s membership contained a node without a name")
		}
		roles := make([]string, 0, 2)
		for key := range item.Metadata.Labels {
			if strings.HasPrefix(key, "node-role.kubernetes.io/") {
				roles = append(roles, strings.TrimPrefix(key, "node-role.kubernetes.io/"))
			}
		}
		ready := false
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Ready" {
				ready = condition.Status == "True"
			}
		}
		node := map[string]any{"name": item.Metadata.Name, "roles": roles, "ready": ready, "status": "NotReady"}
		if ready {
			node["status"] = "Ready"
		}
		if item.Status.NodeInfo.KubeletVersion != "" {
			node["version"] = item.Status.NodeInfo.KubeletVersion
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func validateEndpoint(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("endpoint must be an absolute http or https URL without a path or query")
	}
	return nil
}

func integerInput(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := strconv.Atoi(string(value))
		return parsed
	default:
		return 0
	}
}

func boolInput(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
