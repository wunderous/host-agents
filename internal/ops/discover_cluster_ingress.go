package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// DiscoverClusterIngressArgs resolves how to reach Traefik-published hostnames
// on a VM-backed cluster without operator port-forwards.
type DiscoverClusterIngressArgs struct {
	VMName           string `json:"vmName"`
	WebHostname      string `json:"webHostname,omitempty"`
	McpHostname      string `json:"mcpHostname,omitempty"`
	TraefikNamespace string `json:"traefikNamespace,omitempty"`
	TraefikService   string `json:"traefikService,omitempty"`
}

// DiscoverClusterIngress returns the VM bridge IP and Traefik LoadBalancer
// address (when present) plus client URL hints for CPC web/MCP.
func (s *HostOperationsService) DiscoverClusterIngress(args DiscoverClusterIngressArgs) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	webHost := defaultString(strings.TrimSpace(args.WebHostname), "platform.opute.io")
	mcpHost := defaultString(strings.TrimSpace(args.McpHostname), "mcp.opute.io")
	ns := defaultString(strings.TrimSpace(args.TraefikNamespace), "kube-system")
	svcName := defaultString(strings.TrimSpace(args.TraefikService), "traefik")

	info, err := s.GetVMInfo(vmName, true)
	if err != nil {
		return nil, err
	}
	bridgeIP := firstBridgeIPv4(info.IPv4)
	if bridgeIP == "" {
		return nil, fmt.Errorf("no bridge IPv4 found for VM %s", vmName)
	}

	lbIP := ""
	lbHostname := ""
	stdout, err := s.runKubernetesKubectl(vmName, []string{
		"get", "svc", svcName, "-n", ns, "-o", "json",
	}, "discover traefik service")
	if err == nil && stdout != "" {
		var svc map[string]any
		if json.Unmarshal([]byte(stdout), &svc) == nil {
			lbIP, lbHostname = loadBalancerFromService(svc)
		}
	}

	ingressIP := bridgeIP
	if lbIP != "" {
		ingressIP = lbIP
	}
	base := "http://" + ingressIP
	return map[string]any{
		"vmName":               vmName,
		"bridgeIp":             bridgeIP,
		"traefikNamespace":     ns,
		"traefikService":       svcName,
		"loadBalancerIp":       lbIP,
		"loadBalancerHostname": lbHostname,
		"ingressIp":            ingressIP,
		"webHostname":          webHost,
		"mcpHostname":          mcpHost,
		"webUrl":               base + "/",
		"mcpUrl":               base + "/mcp",
		"curlHints": map[string]string{
			"web": fmt.Sprintf("curl -sS -H 'Host: %s' %s/", webHost, base),
			"mcp": fmt.Sprintf("curl -sS -H 'Host: %s' %s/mcp", mcpHost, base),
		},
		"note": "Use Host headers (or /etc/hosts) until public DNS/tunnel is configured; no kubectl port-forward required.",
	}, nil
}

func firstBridgeIPv4(ips []string) string {
	for _, ip := range ips {
		if strings.HasPrefix(ip, "10.123.") {
			return ip
		}
	}
	for _, ip := range ips {
		if ip != "" && !strings.HasPrefix(ip, "10.42.") && !strings.HasPrefix(ip, "127.") {
			return ip
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func loadBalancerFromService(svc map[string]any) (ip, hostname string) {
	status, _ := svc["status"].(map[string]any)
	lb, _ := status["loadBalancer"].(map[string]any)
	ingress, _ := lb["ingress"].([]any)
	if len(ingress) == 0 {
		return "", ""
	}
	first, _ := ingress[0].(map[string]any)
	if value, ok := first["ip"].(string); ok {
		ip = value
	}
	if value, ok := first["hostname"].(string); ok {
		hostname = value
	}
	return ip, hostname
}
