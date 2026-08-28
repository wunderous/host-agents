package serving

import (
	"errors"
	"fmt"
	"strings"
)

// ServiceIngressEndpoint is an opaque caller-declared endpoint binding.
type ServiceIngressEndpoint struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
}

type DiscoverServiceIngressArgs struct {
	VMName           string                   `json:"vmName"`
	Endpoints        []ServiceIngressEndpoint `json:"endpoints"`
	IngressNamespace string                   `json:"ingressNamespace,omitempty"`
	IngressService   string                   `json:"ingressService,omitempty"`
}

// DiscoverServiceIngress is the generic form of ingress discovery. Product
// hostnames are never inferred; the caller supplies named endpoint bindings.
func (s *Service) DiscoverServiceIngress(args DiscoverServiceIngressArgs) (map[string]any, error) {
	if strings.TrimSpace(args.VMName) == "" || len(args.Endpoints) == 0 {
		return nil, errors.New("vmName and endpoints are required")
	}
	for _, endpoint := range args.Endpoints {
		if strings.TrimSpace(endpoint.Name) == "" || strings.TrimSpace(endpoint.Hostname) == "" {
			return nil, errors.New("each ingress endpoint requires name and hostname")
		}
	}
	vmName := strings.TrimSpace(args.VMName)
	ns := orDefault(args.IngressNamespace, "kube-system")
	svcName := orDefault(args.IngressService, "traefik")
	bridgeIP, err := s.deps.BridgeIP(vmName)
	if err != nil {
		return nil, err
	}
	if bridgeIP == "" {
		return nil, fmt.Errorf("no bridge IPv4 found for target %s", vmName)
	}
	lbIP, lbHostname := s.deps.IngressLoadBalancer(vmName, ns, svcName)
	ingressIP := bridgeIP
	if lbIP != "" {
		ingressIP = lbIP
	}
	return map[string]any{
		"vmName":               vmName,
		"bridgeIp":             bridgeIP,
		"ingressNamespace":     ns,
		"ingressService":       svcName,
		"loadBalancerIp":       lbIP,
		"loadBalancerHostname": lbHostname,
		"ingressIp":            ingressIP,
		"endpoints":            args.Endpoints,
		"note":                 "Use the caller-declared endpoint bindings with the observed ingress address; no product hostname was inferred.",
	}, nil
}

// orDefault substitutes a fallback for a blank value.
func orDefault(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
