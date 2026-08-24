package provider

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{2,127}$`)
var operationIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,127}$`)

func ValidateDescriptor(descriptor PluginDescriptor) error {
	if descriptor.Schema != PluginDescriptorVersion {
		return fmt.Errorf("unsupported provider descriptor schema %q", descriptor.Schema)
	}
	if !identifierPattern.MatchString(strings.TrimSpace(descriptor.PluginID)) {
		return fmt.Errorf("invalid provider pluginId")
	}
	if strings.TrimSpace(descriptor.Version) == "" {
		return fmt.Errorf("provider version is required")
	}
	if descriptor.Server.Transport != "streamable_http" {
		return fmt.Errorf("provider transport must be streamable_http")
	}
	if err := validateEndpoint(descriptor.Server.Endpoint); err != nil {
		return err
	}
	if len(descriptor.Capabilities) == 0 {
		return fmt.Errorf("provider must declare at least one capability")
	}
	return validateCapabilityRefs(descriptor.Capabilities)
}

func ValidateInstallManifest(manifest InstallManifest, expected ProviderRef) error {
	if manifest.Schema != InstallManifestVersion {
		return fmt.Errorf("unsupported provider install manifest schema %q", manifest.Schema)
	}
	if manifest.Provider != expected {
		return fmt.Errorf("install manifest provider identity does not match trusted descriptor")
	}
	if len(manifest.Provides) == 0 {
		return fmt.Errorf("install manifest must provide at least one capability")
	}
	if err := validateCapabilityRefs(manifest.Provides); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Validation.Capability) == "" || strings.TrimSpace(manifest.Validation.Operation) == "" {
		return fmt.Errorf("install manifest validation capability and operation are required")
	}
	for _, recipe := range manifest.Recipes {
		if strings.TrimSpace(recipe.ID) == "" || strings.TrimSpace(recipe.Source.URI) == "" {
			return fmt.Errorf("install manifest recipes require id and source.uri")
		}
		if strings.TrimSpace(recipe.Source.Revision) == "" || strings.TrimSpace(recipe.Source.SHA256) == "" {
			return fmt.Errorf("install manifest recipe %q must pin revision and sha256", recipe.ID)
		}
	}
	if err := validateServices(manifest.Services); err != nil {
		return err
	}
	if manifest.Teardown != nil {
		if manifest.Teardown.ID != "opute.provider.teardown" {
			return fmt.Errorf("provider teardown operation must be opute.provider.teardown")
		}
		if err := validateOperation(*manifest.Teardown, "provider teardown"); err != nil {
			return err
		}
		if manifest.Teardown.Effect != "destructive" {
			return fmt.Errorf("provider teardown operation must be destructive")
		}
	}
	return nil
}

func validateServices(services []ServiceDefinition) error {
	seenServices := make(map[string]bool, len(services))
	seenOperations := make(map[string]bool)
	for _, service := range services {
		if !identifierPattern.MatchString(strings.TrimSpace(service.ID)) || service.Version < 1 {
			return fmt.Errorf("invalid provider service definition")
		}
		if seenServices[service.ID] {
			return fmt.Errorf("duplicate provider service %q", service.ID)
		}
		seenServices[service.ID] = true
		for _, operation := range service.Operations {
			if err := validateOperation(operation, "provider service "+service.ID); err != nil {
				return err
			}
			if seenOperations[operation.ID] {
				return fmt.Errorf("duplicate provider operation %q", operation.ID)
			}
			seenOperations[operation.ID] = true
			switch operation.Effect {
			case "read", "mutation", "destructive", "credential_bearing":
			default:
				return fmt.Errorf("provider operation %q has unsupported effect %q", operation.ID, operation.Effect)
			}
		}
	}
	return nil
}

func validateOperation(operation Operation, owner string) error {
	if !operationIdentifierPattern.MatchString(strings.TrimSpace(operation.ID)) || operation.Version < 1 || operation.InputSchema == nil || operation.OutputSchema == nil {
		return fmt.Errorf("%s contains an invalid operation", owner)
	}
	switch operation.Effect {
	case "read", "mutation", "destructive", "credential_bearing":
	default:
		return fmt.Errorf("provider operation %q has unsupported effect %q", operation.ID, operation.Effect)
	}
	for _, binding := range append(append([]ResourceBinding(nil), operation.Requires...), operation.Produces...) {
		if strings.TrimSpace(binding.ResourceType) == "" {
			return fmt.Errorf("provider operation %q has a resource binding without resourceType", operation.ID)
		}
		if strings.TrimSpace(binding.Argument) == "" && strings.TrimSpace(binding.SourcePath) == "" {
			return fmt.Errorf("provider operation %q has a resource binding without argument or sourcePath", operation.ID)
		}
	}
	return nil
}

func validateCapabilityRefs(refs []CapabilityRef) error {
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if !identifierPattern.MatchString(strings.TrimSpace(ref.ID)) || ref.Version < 1 {
			return fmt.Errorf("invalid capability reference")
		}
		if seen[ref.ID] {
			return fmt.Errorf("duplicate capability reference %q", ref.ID)
		}
		seen[ref.ID] = true
	}
	return nil
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("provider endpoint must be an absolute http(s) URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("provider endpoint must not embed credentials")
	}
	return nil
}
