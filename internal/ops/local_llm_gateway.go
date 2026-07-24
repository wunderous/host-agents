package ops

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

type LocalLLMRelayArgs struct {
	SessionID          string
	ListenHost         string
	ListenPort         int
	TargetHost         string
	TargetPort         int
	IncomingToken      string
	UpstreamToken      string
	AllowedSourceCIDRs []string
	// Deprecated compatibility fields. New callers must use the generic fields above.
	RelayToken      string
	AllowedSourceIP string
}

type LocalLLMK3sProxyArgs struct {
	VMName         string
	Namespace      string
	SecretName     string
	ConfigMapName  string
	DeploymentName string
	ServiceName    string
	ContainerImage string
	NodePort       int
	RelayHost      string
	RelayPort      int
	RelayToken     string
	BearerKey      string
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
	if len(args.AllowedSourceCIDRs) == 0 && strings.TrimSpace(args.AllowedSourceIP) != "" {
		args.AllowedSourceCIDRs = []string{args.AllowedSourceIP + "/32"}
	}
	if len(args.AllowedSourceCIDRs) == 0 {
		return fmt.Errorf("allowedSourceCIDRs is required")
	}
	for _, cidr := range args.AllowedSourceCIDRs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return fmt.Errorf("allowedSourceCIDRs contains invalid CIDR")
		}
	}
	incoming := strings.TrimSpace(args.IncomingToken)
	if incoming == "" {
		incoming = strings.TrimSpace(args.RelayToken)
	}
	if len(incoming) < 32 || strings.ContainsAny(incoming, "\r\n") {
		return fmt.Errorf("incomingToken is invalid")
	}
	if strings.ContainsAny(args.UpstreamToken, "\r\n") {
		return fmt.Errorf("upstreamToken is invalid")
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
	if !safeGatewayIdentifier.MatchString(strings.ToLower(strings.TrimSpace(args.VMName))) {
		return "", fmt.Errorf("vmName is invalid")
	}
	// Defaults are retained only by the one-release compatibility wrapper.
	// New application callers supply their own names and image.
	if args.Namespace == "" {
		args.Namespace = "opute-llm"
	}
	if args.SecretName == "" {
		args.SecretName = "opute-llm-proxy-credentials"
	}
	if args.ConfigMapName == "" {
		args.ConfigMapName = "opute-llm-proxy-config"
	}
	if args.DeploymentName == "" {
		args.DeploymentName = "opute-llm-proxy"
	}
	if args.ServiceName == "" {
		args.ServiceName = "opute-llm-proxy"
	}
	if args.ContainerImage == "" {
		args.ContainerImage = "nginx:1.27.0"
	}
	for name, value := range map[string]string{"namespace": args.Namespace, "secretName": args.SecretName, "configMapName": args.ConfigMapName, "deploymentName": args.DeploymentName, "serviceName": args.ServiceName} {
		if !safeGatewayIdentifier.MatchString(strings.ToLower(strings.TrimSpace(value))) {
			return "", fmt.Errorf("%s is invalid", name)
		}
	}
	if strings.ContainsAny(args.ContainerImage, "\r\n") || strings.TrimSpace(args.ContainerImage) == "" {
		return "", fmt.Errorf("containerImage is invalid")
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
  name: %s
---
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  relay-token: PLACEHOLDER_RELAY_TOKEN
  bearer-key: PLACEHOLDER_BEARER_KEY
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
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
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels: {app: %s}
  template:
    metadata: {labels: {app: %s}}
    spec:
      containers:
      - name: nginx
        image: %s
        ports: [{containerPort: 8080}]
        env:
        - name: RELAY_TOKEN
          valueFrom: {secretKeyRef: {name: %s, key: relay-token}}
        - name: BEARER_KEY
          valueFrom: {secretKeyRef: {name: %s, key: bearer-key}}
        - name: RELAY_HOST
          value: %q
        - name: RELAY_PORT
          value: %d
        command: ["/bin/sh", "-c"]
        args: ["envsubst '${RELAY_TOKEN} ${BEARER_KEY} ${RELAY_HOST} ${RELAY_PORT}' < /etc/nginx-template/nginx.conf.template > /etc/nginx/nginx.conf && exec nginx -g 'daemon off;'"]
        volumeMounts: [{name: nginx-template, mountPath: /etc/nginx-template, readOnly: true}]
        readinessProbe: {httpGet: {path: /healthz, port: 8080}}
      volumes: [{name: nginx-template, configMap: {name: %s}}]
---
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  type: NodePort
  selector: {app: %s}
  ports:
  - port: 8080
    targetPort: 8080
    nodePort: %d
`, args.Namespace, args.SecretName, args.Namespace, args.ConfigMapName, args.Namespace, args.DeploymentName, args.Namespace, args.DeploymentName, args.DeploymentName, args.ContainerImage, args.SecretName, args.SecretName, args.RelayHost, args.RelayPort, args.ConfigMapName, args.ServiceName, args.Namespace, args.ServiceName, args.NodePort), nil
}
