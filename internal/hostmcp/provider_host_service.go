package hostmcp

import (
	"strings"

	"github.com/wunderous/host-agents/internal/hostagent"
)

// resolveProviderHostService finds the host service a provider generation runs
// as, using only what the host can observe.
//
// Teardown's host-owned half -- disable, remove the unit, stop the process --
// needs the unit name, and until now the only source of it was `inputs` on the
// teardown call. That made a correct teardown depend on the caller still
// holding what it learned at install time. A caller that does not have it gets
// the generation retired and the provider process left running, which is not a
// teardown; on the bootstrap path the caller is an arbitrary host agent user
// with no install record at all.
//
// The rule is the provider's own installed location: a unit whose executable or
// unit file sits under a path segment equal to the provider id belongs to that
// provider. That is evidence rather than a naming convention -- the id is in
// the path because the provider was installed there -- so a unit that merely
// looks related is not claimed. On this host that distinguishes
// opute-provider-k3s-p15-... (ExecStart under providers/com.opute.k3s/) from a
// stale opute-provider-k3s left by an older instance layout, and only the
// former is reclaimed.
func (s *Server) resolveProviderHostService(providerID string) map[string]any {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || s == nil || s.agent == nil {
		return nil
	}
	hostService := s.agent.Host()
	for _, scope := range []string{"user", "system"} {
		listed, err := hostService.ListHostServices(scope)
		if err != nil {
			continue
		}
		services, _ := listed["services"].([]map[string]any)
		for _, service := range services {
			name, _ := service["serviceName"].(string)
			if !providerServiceNameIsPlausible(providerID, name) {
				continue
			}
			observed, err := hostService.InspectHostService(hostagent.InspectHostServiceArgs{ServiceName: name, Scope: scope}, nil)
			if err != nil {
				continue
			}
			execStart, _ := observed["execStart"].(string)
			fragmentPath, _ := observed["fragmentPath"].(string)
			if !pathHasSegment(execStart, providerID) && !pathHasSegment(fragmentPath, providerID) {
				continue
			}
			resolved := map[string]any{"serviceName": name, "scope": scope}
			if fragmentPath != "" {
				resolved["serviceFile"] = fragmentPath
			}
			return resolved
		}
	}
	return nil
}

// providerServiceNameIsPlausible narrows which units are worth inspecting. It
// is an optimisation, not the decision: a host carries well over a hundred
// units and inspecting each costs three systemctl calls. Being generous is
// therefore the safe direction -- a name this rejects can only cost a
// resolution that reports "unknown", never a unit wrongly claimed.
func providerServiceNameIsPlausible(providerID, serviceName string) bool {
	if serviceName == "" {
		return false
	}
	for _, segment := range strings.Split(providerID, ".") {
		if len(segment) > 2 && strings.Contains(serviceName, segment) {
			return true
		}
	}
	return false
}

// pathHasSegment reports whether a filesystem path contains `segment` as a
// whole path element. Substring matching would let com.opute.k3s claim a unit
// living under com.opute.k3s-experimental.
func pathHasSegment(path, segment string) bool {
	if path == "" || segment == "" {
		return false
	}
	for _, element := range strings.Split(path, "/") {
		if element == segment {
			return true
		}
	}
	return false
}
