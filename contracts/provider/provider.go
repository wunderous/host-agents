// Package provider defines the neutral wire contracts shared by the Host
// Agent and independently built provider MCP servers.
package provider

import "time"

const (
	PluginDescriptorVersion = "opute-provider-plugin.v1"
	InstallManifestVersion  = "opute-provider-install-manifest.v1"
	CordisExtensionID       = "com.opute/cordis-provider"
	CordisExtensionVersion  = 1
)

type CapabilityRef struct {
	ID      string `json:"id" yaml:"id"`
	Version int    `json:"version" yaml:"version"`
}

type PluginDescriptor struct {
	Schema       string           `json:"schema" yaml:"schema"`
	PluginID     string           `json:"pluginId" yaml:"pluginId"`
	Version      string           `json:"version" yaml:"version"`
	Capabilities []CapabilityRef  `json:"capabilities" yaml:"capabilities"`
	Requires     []CapabilityRef  `json:"requires,omitempty" yaml:"requires,omitempty"`
	Server       ServerDescriptor `json:"server" yaml:"server"`
}

type ServerDescriptor struct {
	Transport  string   `json:"transport" yaml:"transport"`
	Endpoint   string   `json:"endpoint" yaml:"endpoint"`
	Executable string   `json:"executable,omitempty" yaml:"executable,omitempty"`
	Args       []string `json:"args,omitempty" yaml:"args,omitempty"`
	SHA256     string   `json:"sha256,omitempty" yaml:"sha256,omitempty"`
}

type ProviderRef struct {
	ID      string `json:"id" yaml:"id"`
	Version string `json:"version" yaml:"version"`
}

type HostAgentRequirements struct {
	MinimumVersion string `json:"minimumVersion,omitempty" yaml:"minimumVersion,omitempty"`
}

type RecipeSource struct {
	URI      string `json:"uri" yaml:"uri"`
	Revision string `json:"revision,omitempty" yaml:"revision,omitempty"`
	SHA256   string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
}

type RecipeRef struct {
	ID     string         `json:"id" yaml:"id"`
	Source RecipeSource   `json:"source" yaml:"source"`
	Mode   string         `json:"mode,omitempty" yaml:"mode,omitempty"`
	Inputs map[string]any `json:"inputs,omitempty" yaml:"inputs,omitempty"`
}

type ValidationRef struct {
	Capability string `json:"capability" yaml:"capability"`
	Operation  string `json:"operation" yaml:"operation"`
}

// InstallManifest is returned by the provider's read-only manifest
// operation. It contains requirements and recipe references only; it cannot
// directly mutate the host.
type InstallManifest struct {
	Schema    string                `json:"schema" yaml:"schema"`
	Provider  ProviderRef           `json:"provider" yaml:"provider"`
	Provides  []CapabilityRef       `json:"provides" yaml:"provides"`
	Requires  []CapabilityRef       `json:"requires,omitempty" yaml:"requires,omitempty"`
	HostAgent HostAgentRequirements `json:"hostAgent" yaml:"hostAgent"`
	Recipes   []RecipeRef           `json:"recipes" yaml:"recipes"`
	Services  []ServiceDefinition   `json:"services,omitempty" yaml:"services,omitempty"`
	// Teardown is a provider-declared lifecycle operation. The host agent
	// invokes it only after explicit confirmation and executes the returned
	// host-plan.v1 through its ordinary plan boundary.
	Teardown   *Operation    `json:"teardown,omitempty" yaml:"teardown,omitempty"`
	Validation ValidationRef `json:"validation" yaml:"validation"`
}

type Operation struct {
	ID                string            `json:"id" yaml:"id"`
	Version           int               `json:"version" yaml:"version"`
	Description       string            `json:"description,omitempty" yaml:"description,omitempty"`
	InputSchema       map[string]any    `json:"inputSchema" yaml:"inputSchema"`
	OutputSchema      map[string]any    `json:"outputSchema" yaml:"outputSchema"`
	ValidationSchema  string            `json:"validationSchema,omitempty" yaml:"validationSchema,omitempty"`
	ObservationSchema string            `json:"observationSchema,omitempty" yaml:"observationSchema,omitempty"`
	Effect            string            `json:"effect" yaml:"effect"`
	ResourceKinds     []string          `json:"resourceKinds,omitempty" yaml:"resourceKinds,omitempty"`
	Requires          []ResourceBinding `json:"requires,omitempty" yaml:"requires,omitempty"`
	Produces          []ResourceBinding `json:"produces,omitempty" yaml:"produces,omitempty"`
	Idempotent        bool              `json:"idempotent" yaml:"idempotent"`
	SupportsReadiness bool              `json:"supportsReadiness" yaml:"supportsReadiness"`
	SupportsStreaming bool              `json:"supportsStreaming,omitempty" yaml:"supportsStreaming,omitempty"`
}

// ResourceBinding is provider-declared metadata. The host validates its
// shape and resource kind, but never derives relationships from URI-shaped
// fields or provider-specific schemas.
type ResourceBinding struct {
	Argument     string `json:"argument,omitempty" yaml:"argument,omitempty"`
	ResourceType string `json:"resourceType" yaml:"resourceType"`
	SourcePath   string `json:"sourcePath,omitempty" yaml:"sourcePath,omitempty"`
	Required     bool   `json:"required,omitempty" yaml:"required,omitempty"`
}

type ServiceDefinition struct {
	ID           string          `json:"id" yaml:"id"`
	Version      int             `json:"version" yaml:"version"`
	Operations   []Operation     `json:"operations" yaml:"operations"`
	Dependencies []CapabilityRef `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
}

type ProviderEvent struct {
	Name            string    `json:"name"`
	ProviderID      string    `json:"providerId"`
	GenerationID    string    `json:"generationId,omitempty"`
	CatalogRevision string    `json:"catalogRevision,omitempty"`
	OperationID     string    `json:"operationId,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	At              time.Time `json:"at"`
}
