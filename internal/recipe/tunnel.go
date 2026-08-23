package recipe

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/plan"
)

const TunnelContractVersion = "tunnel-recipe.v1"

type TunnelDocument struct {
	Envelope   `yaml:",inline"`
	Provider   TunnelProvider  `json:"provider" yaml:"provider"`
	Bindings   []TunnelBinding `json:"bindings" yaml:"bindings"`
	Activation *ActivationSpec `json:"activation,omitempty" yaml:"activation,omitempty"`
}

type TunnelProvider struct {
	ID           string   `json:"id" yaml:"id"`
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

type TunnelBinding struct {
	ID          string `json:"id" yaml:"id"`
	Hostname    string `json:"hostname" yaml:"hostname"`
	LocalTarget string `json:"localTarget" yaml:"localTarget"`
	Path        string `json:"path,omitempty" yaml:"path,omitempty"`
}

type LoadedTunnel struct {
	BaseLoaded
	Document TunnelDocument
}

func LoadTunnel(request SourceRequest) (LoadedTunnel, error) {
	raw, metadata, err := LoadRaw(request)
	if err != nil {
		return LoadedTunnel{}, err
	}
	var document TunnelDocument
	if err := Decode(raw, &document, "tunnel recipe"); err != nil {
		return LoadedTunnel{}, err
	}
	if err := validateTunnelEnvelope(document); err != nil {
		return LoadedTunnel{}, err
	}
	base := BaseLoaded{Document: document.Envelope, Source: metadata}
	base.Source.RecipeHash, err = CanonicalHash(document)
	if err != nil {
		return LoadedTunnel{}, fmt.Errorf("hash tunnel recipe: %w", err)
	}
	return LoadedTunnel{BaseLoaded: base, Document: document}, nil
}

func (loaded *LoadedTunnel) ResolveInputs(values map[string]any) error {
	base, err := ResolveBaseInputs(loaded.Document.Envelope, values)
	if err != nil {
		return err
	}
	rawBindings := make([]any, 0, len(loaded.Document.Bindings))
	for _, binding := range loaded.Document.Bindings {
		rawBindings = append(rawBindings, map[string]any{
			"id":          binding.ID,
			"hostname":    binding.Hostname,
			"localTarget": binding.LocalTarget,
			"path":        binding.Path,
		})
	}
	expanded, err := plan.InterpolateArgs(map[string]any{"bindings": rawBindings}, plan.EvalContext{Variables: base.ExpandedPlan.Variables})
	if err != nil {
		return fmt.Errorf("resolve tunnel bindings: %w", err)
	}
	encoded, err := json.Marshal(expanded["bindings"])
	if err != nil {
		return fmt.Errorf("encode resolved tunnel bindings: %w", err)
	}
	var bindings []TunnelBinding
	if err := json.Unmarshal(encoded, &bindings); err != nil {
		return fmt.Errorf("decode resolved tunnel bindings: %w", err)
	}
	loaded.Document.Bindings = bindings
	for _, binding := range bindings {
		if strings.Contains(binding.Hostname, "${") || strings.Contains(binding.LocalTarget, "${") {
			return fmt.Errorf("tunnel binding %q contains unresolved variables", binding.ID)
		}
	}
	base.Source = loaded.Source
	base.Document = loaded.Document.Envelope
	loaded.BaseLoaded = base
	return nil
}

func (loaded LoadedTunnel) Validate(capabilities map[string]plan.Capability, catalogRevision string) error {
	if err := validateTunnelEnvelope(loaded.Document); err != nil {
		return err
	}
	if loaded.ExpandedPlan.ContractVersion == "" {
		return fmt.Errorf("recipe plan is not resolved; resolve inputs first")
	}
	for _, name := range loaded.Document.Compatibility.RequiredTools {
		if _, ok := capabilities[name]; !ok {
			return fmt.Errorf("recipe requires unsupported host-agent capability %q", name)
		}
	}
	if err := rejectNestedPlanRuns(loaded.ExpandedPlan); err != nil {
		return err
	}
	if err := plan.Validate(loaded.ExpandedPlan, capabilities, catalogRevision); err != nil {
		return fmt.Errorf("validate recipe host plan: %w", err)
	}
	return nil
}

func validateTunnelEnvelope(document TunnelDocument) error {
	if err := ValidateBaseEnvelope(document.Envelope, TunnelContractVersion); err != nil {
		return err
	}
	if strings.TrimSpace(document.Provider.ID) == "" {
		return fmt.Errorf("provider.id is required")
	}
	if len(document.Bindings) == 0 {
		return fmt.Errorf("tunnel recipe must contain at least one binding")
	}
	seen := map[string]bool{}
	for _, binding := range document.Bindings {
		if strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.Hostname) == "" || strings.TrimSpace(binding.LocalTarget) == "" {
			return fmt.Errorf("tunnel bindings require id, hostname, and localTarget")
		}
		if seen[binding.ID] {
			return fmt.Errorf("duplicate tunnel binding %q", binding.ID)
		}
		seen[binding.ID] = true
	}
	if document.Activation != nil {
		if strings.TrimSpace(document.Activation.Capability) == "" || strings.TrimSpace(document.Activation.ServingContract) == "" {
			return fmt.Errorf("activation capability and servingContract are required")
		}
		if len(document.Activation.InputBindings) == 0 {
			return fmt.Errorf("activation.inputBindings is required")
		}
		for binding, inputName := range document.Activation.InputBindings {
			if strings.TrimSpace(binding) == "" || strings.TrimSpace(inputName) == "" {
				return fmt.Errorf("activation.inputBindings must contain non-empty names")
			}
			if _, ok := document.Inputs[inputName]; !ok {
				return fmt.Errorf("activation.inputBindings[%q] references unknown input %q", binding, inputName)
			}
		}
	}
	return nil
}
