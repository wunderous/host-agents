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
	targetURI, err := s.kubernetesTargetURI(args.VMName)
	if err != nil {
		return nil, err
	}
	if _, err := s.ApplyManifest(ApplyManifestArgs{URI: targetURI, Manifest: manifest}, onData); err != nil {
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
	targetURI, err := s.kubernetesTargetURI(args.VMName)
	if err != nil {
		return nil, err
	}
	if _, err := s.DeleteK8sResource(K8sResourceArgs{URI: targetURI, Kind: "ingress", ResourceName: args.IngressName, Namespace: args.Namespace}, onData); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": args.Namespace, "ingressName": args.IngressName, "deleted": true}, nil
}
