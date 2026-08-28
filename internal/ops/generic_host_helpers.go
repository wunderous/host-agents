package ops

import (
	"strings"
)

// upsertEnvFile persists caller-owned environment values without exposing
// their contents in the operation result. It is shared by generic connection
// configuration and keeps host-agent policy independent of any consumer.
// Removals are applied atomically with assignments so a connection migration
// cannot leave retired credentials or transport settings behind.
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

func loadBalancerFromService(service map[string]any) (ip, hostname string) {
	status, _ := service["status"].(map[string]any)
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
