package ops

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

type LocalLLMRelayArgs struct {
	SessionID       string
	ListenHost      string
	ListenPort      int
	TargetHost      string
	TargetPort      int
	RelayToken      string
	AllowedSourceIP string
}

type LocalLLMK3sProxyArgs struct {
	VMName     string
	NodePort   int
	RelayHost  string
	RelayPort  int
	RelayToken string
	BearerKey  string
}

var safeGatewayIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)

func ValidateLocalLLMRelayArgs(args LocalLLMRelayArgs) error {
	if strings.TrimSpace(args.SessionID) == "" || len(args.SessionID) > 128 {
		return fmt.Errorf("sessionId is required")
	}
	listenIP := net.ParseIP(strings.TrimSpace(args.ListenHost))
	if listenIP == nil || listenIP.IsUnspecified() || net.ParseIP(strings.TrimSpace(args.TargetHost)) == nil {
		return fmt.Errorf("relay hosts must be IP addresses")
	}
	if args.TargetPort < 1 || args.TargetPort > 65535 || args.ListenPort < 0 || args.ListenPort > 65535 {
		return fmt.Errorf("relay port is invalid")
	}
	if net.ParseIP(strings.TrimSpace(args.AllowedSourceIP)) == nil {
		return fmt.Errorf("allowedSourceIP must be an IP address")
	}
	if len(args.RelayToken) < 32 || strings.ContainsAny(args.RelayToken, "\r\n") {
		return fmt.Errorf("relayToken is invalid")
	}
	return nil
}

// RenderLocalLLMK3sProxyManifestWithSecrets is used only at the host execution
// boundary. Durable control-plane payloads carry secret references; the agent
// resolves them and injects them into the ephemeral manifest immediately
// before applying it to the selected cluster.
func RenderLocalLLMK3sProxyManifestWithSecrets(args LocalLLMK3sProxyArgs) (string, error) {
	manifest, err := RenderLocalLLMK3sProxyManifest(args)
	if err != nil {
		return "", err
	}
	return strings.NewReplacer(
		"PLACEHOLDER_RELAY_TOKEN", strconv.Quote(args.RelayToken),
		"PLACEHOLDER_BEARER_KEY", strconv.Quote(args.BearerKey),
	).Replace(manifest), nil
}

func RenderLocalLLMK3sProxyManifest(args LocalLLMK3sProxyArgs) (string, error) {
	if strings.TrimSpace(args.VMName) != "opute-llm-gateway" || !safeGatewayIdentifier.MatchString(strings.ToLower(strings.TrimSpace(args.VMName))) {
		return "", fmt.Errorf("vmName is invalid")
	}
	if args.NodePort < 30000 || args.NodePort > 32767 {
		return "", fmt.Errorf("nodePort must be in the NodePort range")
	}
	if net.ParseIP(args.RelayHost) == nil || args.RelayPort < 1 || args.RelayPort > 65535 {
		return "", fmt.Errorf("relay endpoint is invalid")
	}
	if len(args.RelayToken) < 32 || len(args.BearerKey) < 32 || strings.ContainsAny(args.RelayToken+args.BearerKey, "\r\n") {
		return "", fmt.Errorf("proxy credentials are invalid")
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: opute-llm
---
apiVersion: v1
kind: Secret
metadata:
  name: opute-llm-proxy-credentials
  namespace: opute-llm
type: Opaque
stringData:
  relay-token: PLACEHOLDER_RELAY_TOKEN
  bearer-key: PLACEHOLDER_BEARER_KEY
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: opute-llm-proxy-config
  namespace: opute-llm
data:
  nginx.conf.template: |
    events {}
    http {
      server {
        listen 8080;
        location = /healthz { return 200 'ok'; add_header Content-Type text/plain; }
        location ^~ /api/ { return 404; }
        location / {
          if ($http_authorization != "Bearer ${BEARER_KEY}") { return 401; }
          proxy_http_version 1.1;
          proxy_set_header Authorization "Bearer ${RELAY_TOKEN}";
          proxy_set_header Host $host;
          proxy_buffering off;
          proxy_request_buffering off;
          proxy_read_timeout 3600s;
          proxy_pass http://${RELAY_HOST}:${RELAY_PORT};
        }
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: opute-llm-proxy
  namespace: opute-llm
spec:
  replicas: 1
  selector:
    matchLabels: {app: opute-llm-proxy}
  template:
    metadata: {labels: {app: opute-llm-proxy}}
    spec:
      containers:
      - name: nginx
        image: nginx:1.27.0
        ports: [{containerPort: 8080}]
        env:
        - name: RELAY_TOKEN
          valueFrom: {secretKeyRef: {name: opute-llm-proxy-credentials, key: relay-token}}
        - name: BEARER_KEY
          valueFrom: {secretKeyRef: {name: opute-llm-proxy-credentials, key: bearer-key}}
        - name: RELAY_HOST
          value: %q
        - name: RELAY_PORT
          value: %q
        command: ["/bin/sh", "-c"]
        args: ["envsubst '${RELAY_TOKEN} ${BEARER_KEY} ${RELAY_HOST} ${RELAY_PORT}' < /etc/nginx-template/nginx.conf.template > /etc/nginx/nginx.conf && exec nginx -g 'daemon off;'"]
        volumeMounts: [{name: nginx-template, mountPath: /etc/nginx-template, readOnly: true}]
        readinessProbe: {httpGet: {path: /healthz, port: 8080}}
      volumes: [{name: nginx-template, configMap: {name: opute-llm-proxy-config}}]
---
apiVersion: v1
kind: Service
metadata:
  name: opute-llm-proxy
  namespace: opute-llm
spec:
  type: NodePort
  selector: {app: opute-llm-proxy}
  ports:
  - port: 8080
    targetPort: 8080
    nodePort: %d
`, args.RelayHost, args.RelayPort, args.NodePort), nil
}
