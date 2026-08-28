package ops

import (
	"encoding/json"

	"github.com/wunderous/host-agents/internal/domain/serving"
)

// The serving domain now owns these types and operations. HostOperationsService
// keeps delegating methods so the dispatch registry and every existing caller
// are unaffected while the remaining domains move; this file disappears with
// internal/ops itself.
type (
	ServingAssignmentArgs      = serving.ServingAssignmentArgs
	ServiceIngressEndpoint     = serving.ServiceIngressEndpoint
	DiscoverServiceIngressArgs = serving.DiscoverServiceIngressArgs
)

// serving builds the serving domain over this service's shared seam, binding
// the four cross-domain capabilities it declares. The bindings are stated in
// primitives on purpose: the incus inventory model and the Kubernetes Service
// schema stay on this side of the boundary.
func (s *HostOperationsService) serving() *serving.Service {
	return serving.New(&s.shared, serving.Deps{
		RunAgentShell: func(command string, onData func(string)) error {
			_, err := s.RunAgentShell(command, onData)
			return err
		},
		SetHostServiceState: func(serviceName, state, scope string, onData func(string)) (map[string]any, error) {
			return s.SetHostServiceState(SetHostServiceStateArgs{ServiceName: serviceName, State: state, Scope: scope}, onData)
		},
		BridgeIP: func(vmName string) (string, error) {
			info, err := s.GetVMInfo(vmName, true)
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

func (s *HostOperationsService) ReconcileServingAssignment(args ServingAssignmentArgs, onData func(string)) (map[string]any, error) {
	return s.serving().ReconcileServingAssignment(args, onData)
}

func (s *HostOperationsService) DiscoverServiceIngress(args DiscoverServiceIngressArgs) (map[string]any, error) {
	return s.serving().DiscoverServiceIngress(args)
}
