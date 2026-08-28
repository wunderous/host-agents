package hostagent

import (
	"encoding/json"

	"github.com/wunderous/host-agents/internal/domain/serving"
)

// These aliases name the serving domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
type (
	ServingAssignmentArgs      = serving.ServingAssignmentArgs
	ServiceIngressEndpoint     = serving.ServiceIngressEndpoint
	DiscoverServiceIngressArgs = serving.DiscoverServiceIngressArgs
)

// serving builds the serving domain over this service's shared seam, binding
// the four cross-domain capabilities it declares. The bindings are stated in
// primitives on purpose: the incus inventory model and the Kubernetes Service
// schema stay on this side of the boundary.
func (s *Service) Serving() *serving.Service {
	return serving.New(&s.shared, serving.Deps{
		RunAgentShell: func(command string, onData func(string)) error {
			_, err := s.Host().RunAgentShell(command, onData)
			return err
		},
		SetHostServiceState: func(serviceName, state, scope string, onData func(string)) (map[string]any, error) {
			return s.Host().SetHostServiceState(SetHostServiceStateArgs{ServiceName: serviceName, State: state, Scope: scope}, onData)
		},
		BridgeIP: func(vmName string) (string, error) {
			info, err := s.Incus().GetVMInfo(vmName, true)
			if err != nil {
				return "", err
			}
			return firstBridgeIPv4(info.IPv4), nil
		},
		IngressLoadBalancer: func(vmName, namespace, service string) (ip, hostname string) {
			stdout, err := s.runKubernetesKubectl(vmName, []string{"get", "svc", service, "-n", namespace, "-o", "json"}, "discover ingress service")
			if err != nil || stdout == "" {
				return "", ""
			}
			var decoded map[string]any
			if json.Unmarshal([]byte(stdout), &decoded) != nil {
				return "", ""
			}
			return loadBalancerFromService(decoded)
		},
	})
}
