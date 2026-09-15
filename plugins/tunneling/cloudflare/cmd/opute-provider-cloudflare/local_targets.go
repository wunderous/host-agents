package main

import (
	"fmt"
	"net"
	"strings"
)

// defaultForwarderImage backs the per-mapping forwarder sidecar. It is pinned
// for the same reason the cloudflared image is: a connector that silently
// changes what it runs between two installs of the same recipe is not a
// reproducible deployment.
const defaultForwarderImage = "alpine/socat:1.8.0.0"

// localTarget is one `localhost:<LocalPort>` -> `<Target>` mapping inside the
// connector pod.
//
// A token-backed tunnel takes its ingress from Cloudflare's remote
// configuration, and that configuration names loopback addresses --
// `http://localhost:9190` -- because the connector is the thing that is
// supposed to be next to the service. Inside a Kubernetes pod it is not: the
// service is a ClusterIP reachable by DNS, and nothing was listening on those
// loopback ports at all. That is what these mappings close.
type localTarget struct {
	LocalPort int
	Target    string
}

// parseLocalTargets validates the caller's mappings.
//
// It refuses rather than drops. The old code passed this argument to
// fmt.Sprint and rendered the result as a YAML comment, so a caller who asked
// for routing that never happened got a healthy connector and a 502 from
// Cloudflare with nothing in the install to explain it. A malformed mapping is
// now an error at install time, where the caller can still act on it.
func parseLocalTargets(raw any) ([]localTarget, error) {
	if raw == nil {
		return nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("localTargets must be an array of {localPort, target}")
	}
	targets := make([]localTarget, 0, len(entries))
	bound := make(map[int]string, len(entries))
	for index, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("localTargets[%d] must be an object with localPort and target", index)
		}
		port := intInput(fields, "localPort", 0)
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("localTargets[%d].localPort must be a TCP port between 1 and 65535, got %v", index, fields["localPort"])
		}
		target := strings.TrimSpace(stringInput(fields, "target", ""))
		if target == "" {
			return nil, fmt.Errorf("localTargets[%d].target is required", index)
		}
		// host:port, because the forwarder opens a TCP connection and has
		// nowhere to guess a port from. A bare hostname here is the mistake
		// worth catching: it renders a socat address that fails at runtime,
		// inside a pod the caller is not watching.
		host, servicePort, err := net.SplitHostPort(target)
		if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(servicePort) == "" {
			return nil, fmt.Errorf("localTargets[%d].target must be host:port, got %q", index, target)
		}
		if previous, clash := bound[port]; clash {
			return nil, fmt.Errorf("localTargets binds localhost:%d to both %q and %q", port, previous, target)
		}
		bound[port] = target
		targets = append(targets, localTarget{LocalPort: port, Target: target})
	}
	return targets, nil
}

// forwarderContainers renders one sidecar per mapping.
//
// Containers in a pod share a network namespace, so a listener here is
// `localhost` to cloudflared in the same pod -- which is exactly the address
// its remote ingress was configured with. Binding to 127.0.0.1 rather than
// every interface keeps the mapping private to the pod: it exists to serve the
// connector beside it, not to publish a second way into the target from
// anywhere in the cluster.
func forwarderContainers(targets []localTarget, image string) string {
	var out strings.Builder
	for _, target := range targets {
		listen := fmt.Sprintf("TCP-LISTEN:%d,fork,reuseaddr,bind=127.0.0.1", target.LocalPort)
		connect := "TCP:" + target.Target
		fmt.Fprintf(&out, "      - name: forward-%d\n", target.LocalPort)
		fmt.Fprintf(&out, "        image: %s\n", image)
		fmt.Fprintf(&out, "        args: [%s, %s]\n", yamlQuote(listen), yamlQuote(connect))
		// Without a limit one mapping under load can evict the connector it
		// exists to serve, and the tunnel goes down for every other hostname
		// on it. A forwarder moves bytes and holds no state; this is enough.
		out.WriteString("        resources:\n")
		out.WriteString("          requests:\n")
		out.WriteString("            cpu: 10m\n")
		out.WriteString("            memory: 16Mi\n")
		out.WriteString("          limits:\n")
		out.WriteString("            memory: 64Mi\n")
	}
	return out.String()
}

// localTargetsSchema is the declared shape of the mappings. It was `array` with
// no item schema, which accepted anything and described nothing.
func localTargetsSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":     "object",
			"required": []string{"localPort", "target"},
			"properties": map[string]any{
				"localPort": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
				"target":    map[string]any{"type": "string", "minLength": 3, "description": "host:port the connector pod should reach on this loopback port."},
			},
		},
	}
}
