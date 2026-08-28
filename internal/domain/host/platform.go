package host

import (
	"github.com/wunderous/host-agents/pkg/hostplatform"
)

// DetectHostPlatform returns the read-only operating-system and CPU identity
// of the host this agent process runs on. Detection is local and
// provider-neutral: it never consults the control plane and never infers the
// host kind from an assignment or a product name.
func (s *Service) DetectHostPlatform() (*hostplatform.Platform, error) {
	platform := hostplatform.Detect()
	return &platform, nil
}
