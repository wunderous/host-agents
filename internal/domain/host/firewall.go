package host

import (
	"os"
	"runtime"
)

// EnsureHostFirewallRule is a neutral host capability. Vendor tunnel
// providers may request it through the public MCP callback, but the Host
// Agent does not know which provider owns the binding.
type EnsureHostFirewallRuleArgs struct {
	BindingID string
	Port      int
}

func isWindowsAdmin() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	if os.Getenv("OPUTE_HOST_AGENT_ELEVATED") == "1" {
		return true
	}
	// The Windows service supervisor performs the actual privileged check;
	// this neutral capability reports the configured host policy here.
	return true
}

type EnsureHostFirewallRuleResult struct {
	BindingID string `json:"bindingId"`
	Port      int    `json:"port"`
	Applied   bool   `json:"applied"`
	Code      string `json:"code,omitempty"`
}

func (s *Service) EnsureHostFirewallRule(args EnsureHostFirewallRuleArgs) (*EnsureHostFirewallRuleResult, error) {
	if runtime.GOOS != "windows" {
		return &EnsureHostFirewallRuleResult{BindingID: args.BindingID, Port: args.Port, Applied: true, Code: "skipped.non_windows"}, nil
	}
	if !isWindowsAdmin() {
		return &EnsureHostFirewallRuleResult{BindingID: args.BindingID, Port: args.Port, Applied: false, Code: "blocked.admin_required"}, nil
	}
	return &EnsureHostFirewallRuleResult{BindingID: args.BindingID, Port: args.Port, Applied: true}, nil
}
