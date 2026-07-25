package ops

import (
	"errors"
	"fmt"
	"strings"
)

type ConfigureServiceDomainArgs struct {
	VMName       string `json:"vmName"`
	Namespace    string `json:"namespace"`
	IngressName  string `json:"ingressName"`
	Hostname     string `json:"hostname"`
	ServiceName  string `json:"serviceName"`
	ServicePort  int    `json:"servicePort"`
	IngressClass string `json:"ingressClass,omitempty"`
}

type InstallCloudflaredConnectorArgs struct {
	VMName       string                   `json:"vmName"`
	Namespace    string                   `json:"namespace,omitempty"`
	Name         string                   `json:"name,omitempty"`
	Token        string                   `json:"token"`
	Image        string                   `json:"image,omitempty"`
	Replicas     int                      `json:"replicas,omitempty"`
	LocalTargets []CloudflaredLocalTarget `json:"localTargets,omitempty"`
}

type CloudflaredLocalTarget struct {
	LocalPort int    `json:"localPort"`
	Target    string `json:"target"`
}

func (s *HostOperationsService) ConfigureServiceDomain(args ConfigureServiceDomainArgs, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(args.VMName) == "" || strings.TrimSpace(args.Namespace) == "" || strings.TrimSpace(args.IngressName) == "" || strings.TrimSpace(args.Hostname) == "" || strings.TrimSpace(args.ServiceName) == "" || args.ServicePort <= 0 {
		return nil, errors.New("vmName, namespace, ingressName, hostname, serviceName, and positive servicePort are required")
	}
	for value, field := range map[string]string{args.Namespace: "namespace", args.IngressName: "ingressName", args.ServiceName: "serviceName"} {
		if err := validateK8sIdentifier(value, field); err != nil {
			return nil, err
		}
	}
	hostname := strings.TrimSpace(args.Hostname)
	if strings.ContainsAny(hostname, "\r\n' \t/") || !strings.Contains(hostname, ".") {
		return nil, errors.New("hostname must be a DNS name")
	}
	class := strings.TrimSpace(args.IngressClass)
	if class == "" {
		class = "traefik"
	}
	manifest := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
spec:
  ingressClassName: %s
  rules:
    - host: %s
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: %s
                port:
                  number: %d
`, args.IngressName, args.Namespace, class, hostname, args.ServiceName, args.ServicePort)
	if _, err := s.ApplyManifest(ApplyManifestArgs{VMName: args.VMName, Manifest: manifest}, onData); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": args.Namespace, "ingressName": args.IngressName, "hostname": hostname, "serviceName": args.ServiceName, "servicePort": args.ServicePort, "ingressClass": class, "configured": true}, nil
}

func (s *HostOperationsService) RemoveServiceDomain(args ConfigureServiceDomainArgs, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(args.VMName) == "" || strings.TrimSpace(args.Namespace) == "" || strings.TrimSpace(args.IngressName) == "" {
		return nil, errors.New("vmName, namespace, and ingressName are required")
	}
	if err := validateK8sIdentifier(args.Namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := validateK8sIdentifier(args.IngressName, "ingressName"); err != nil {
		return nil, err
	}
	res, err := s.runKubernetesKubectl(args.VMName, []string{"delete", "ingress", args.IngressName, "-n", args.Namespace}, "delete domain ingress")
	if err != nil && !strings.Contains(strings.ToLower(res), "not found") {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": args.Namespace, "ingressName": args.IngressName, "deleted": true}, nil
}

func (s *HostOperationsService) InstallCloudflaredConnector(args InstallCloudflaredConnectorArgs, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(args.VMName) == "" || strings.TrimSpace(args.Token) == "" {
		return nil, errors.New("vmName and token are required")
	}
	namespace := defaultString(args.Namespace, "edge-system")
	name := defaultString(args.Name, "cloudflared")
	image := defaultString(args.Image, "cloudflare/cloudflared:2025.7.0")
	if err := validateK8sIdentifier(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := validateK8sIdentifier(name, "name"); err != nil {
		return nil, err
	}
	if strings.ContainsAny(image, "\r\n'") {
		return nil, errors.New("image is invalid")
	}
	replicas := args.Replicas
	if replicas <= 0 {
		replicas = 1
	}
	containers := fmt.Sprintf(`        - name: cloudflared
          image: %s
          args: ["tunnel", "--no-autoupdate", "run", "--token", "$(TUNNEL_TOKEN)"]
          env:
            - name: TUNNEL_TOKEN
              valueFrom:
                secretKeyRef:
                  name: %s-token
                  key: token`, image, name)
	for i, target := range args.LocalTargets {
		if target.LocalPort <= 0 || target.LocalPort > 65535 || strings.TrimSpace(target.Target) == "" {
			return nil, fmt.Errorf("localTargets[%d] must contain a valid localPort and target", i)
		}
		if strings.ContainsAny(target.Target, "\r\n'\"") {
			return nil, fmt.Errorf("localTargets[%d].target is invalid", i)
		}
		containers += fmt.Sprintf(`
        - name: proxy-%d
          image: alpine/socat:1.8.0.0
          args: ["TCP-LISTEN:%d,fork,reuseaddr", "TCP4:%s"]`, target.LocalPort, target.LocalPort, target.Target)
	}
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: Secret
metadata:
  name: %s-token
  namespace: %s
type: Opaque
stringData:
  token: %s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
    spec:
      containers:
%s
`, namespace, name, namespace, yamlSingleQuote(args.Token), name, namespace, replicas, name, name, containers)
	if _, err := s.ApplyManifest(ApplyManifestArgs{VMName: args.VMName, Manifest: manifest}, onData); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": namespace, "name": name, "replicas": replicas, "localTargets": args.LocalTargets, "installed": true}, nil
}

func yamlSingleQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func (s *HostOperationsService) DeleteCloudflaredConnector(args InstallCloudflaredConnectorArgs, onData func(string)) (map[string]any, error) {
	namespace := defaultString(args.Namespace, "edge-system")
	if strings.TrimSpace(args.VMName) == "" {
		return nil, errors.New("vmName is required")
	}
	if err := validateK8sIdentifier(namespace, "namespace"); err != nil {
		return nil, err
	}
	if _, err := s.runKubernetesKubectl(args.VMName, []string{"delete", "namespace", namespace, "--ignore-not-found=true"}, "delete Cloudflare connector namespace"); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": namespace, "deleted": true}, nil
}
