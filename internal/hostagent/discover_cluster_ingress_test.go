package hostagent

import "testing"

func TestFirstBridgeIPv4PrefersIncusBridge(t *testing.T) {
	got := firstBridgeIPv4([]string{"10.42.0.1", "10.123.133.201", "10.42.0.0"})
	if got != "10.123.133.201" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadBalancerFromService(t *testing.T) {
	ip, host := loadBalancerFromService(map[string]any{
		"status": map[string]any{
			"loadBalancer": map[string]any{
				"ingress": []any{map[string]any{"ip": "10.123.133.201"}},
			},
		},
	})
	if ip != "10.123.133.201" || host != "" {
		t.Fatalf("got %q %q", ip, host)
	}
}
