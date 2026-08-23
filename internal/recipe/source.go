package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/wunderous/host-agents/internal/plan"
)

// Envelope is the capability-neutral portion shared by every recipe family.
// A recipe family may add provider/capability metadata, but it never gets a
// second plan language or executor.
type Envelope struct {
	ContractVersion string               `json:"contractVersion" yaml:"contractVersion"`
	RecipeID        string               `json:"recipeId" yaml:"recipeId"`
	RecipeVersion   string               `json:"recipeVersion" yaml:"recipeVersion"`
	Inputs          map[string]InputSpec `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Compatibility   CompatibilitySpec    `json:"compatibility,omitempty" yaml:"compatibility,omitempty"`
	Plan            plan.Document        `json:"plan" yaml:"plan"`
	OutputMapping   map[string]string    `json:"outputMapping,omitempty" yaml:"outputMapping,omitempty"`
}

// BaseLoaded is the resolved, validated input to any recipe-family handler.
// It deliberately contains only source provenance, input values, and a
// host-plan document.
type BaseLoaded struct {
	Document     Envelope
	ExpandedPlan plan.Document
	Inputs       map[string]any
	Source       SourceMetadata
}

func LoadRaw(request SourceRequest) ([]byte, SourceMetadata, error) {
	return loadSource(request)
}

func CanonicalHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func Decode(raw []byte, target any, kind string) error {
	if target == nil {
		return fmt.Errorf("recipe %s decode target is required", kind)
	}
	return decodeValue(raw, target, kind)
}

func ResolveBaseInputs(document Envelope, values map[string]any) (BaseLoaded, error) {
	resolved, err := resolveInputs(document.Inputs, values)
	if err != nil {
		return BaseLoaded{}, err
	}
	variables := make(map[string]any, len(document.Plan.Variables)+1)
	for key, value := range document.Plan.Variables {
		variables[key] = value
	}
	variables["inputs"] = resolved
	expanded := document.Plan
	expanded.Variables = variables
	identity, err := plan.InterpolateArgs(map[string]any{"idempotencyKey": expanded.IdempotencyKey}, plan.EvalContext{Variables: variables})
	if err != nil {
		return BaseLoaded{}, fmt.Errorf("resolve recipe plan identity: %w", err)
	}
	if value, ok := identity["idempotencyKey"].(string); ok {
		expanded.IdempotencyKey = value
	}
	return BaseLoaded{Document: document, ExpandedPlan: expanded, Inputs: resolved}, nil
}

func ValidateBaseEnvelope(document Envelope, expectedContract string) error {
	if document.ContractVersion != expectedContract {
		return fmt.Errorf("unsupported recipe contractVersion %q", document.ContractVersion)
	}
	if document.RecipeID == "" || document.RecipeVersion == "" {
		return fmt.Errorf("recipeId and recipeVersion are required")
	}
	if document.Plan.ContractVersion != plan.ContractVersion {
		return fmt.Errorf("recipe plan must use %s", plan.ContractVersion)
	}
	if len(document.Plan.Nodes) == 0 {
		return fmt.Errorf("recipe plan must contain at least one node")
	}
	return nil
}

func (loaded BaseLoaded) RedactedInputs() map[string]any {
	redacted := make(map[string]any, len(loaded.Inputs))
	for name, value := range loaded.Inputs {
		if loaded.Document.Inputs[name].Secret {
			redacted[name] = "[redacted]"
			continue
		}
		redacted[name] = value
	}
	return redacted
}

func (loaded BaseLoaded) SecretInputNames() []string {
	result := make([]string, 0)
	for name, spec := range loaded.Document.Inputs {
		if spec.Secret {
			result = append(result, name)
		}
	}
	return sortedStrings(result)
}

func resolveInputs(specs map[string]InputSpec, values map[string]any) (map[string]any, error) {
	resolved := make(map[string]any, len(specs))
	for name, value := range values {
		if _, ok := specs[name]; !ok {
			return nil, fmt.Errorf("unknown recipe input %q", name)
		}
		resolved[name] = value
	}
	for name, spec := range specs {
		value, present := resolved[name]
		if !present && spec.Default != nil {
			value, present = spec.Default, true
		}
		if !present {
			if spec.Required {
				return nil, fmt.Errorf("required recipe input %q is missing", name)
			}
			continue
		}
		if len(spec.Schema) > 0 {
			if err := plan.ValidateJSON(spec.Schema, value); err != nil {
				return nil, fmt.Errorf("recipe input %q: %w", name, err)
			}
		}
		resolved[name] = value
	}
	return resolved, nil
}

func sortedStrings(values []string) []string {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values
}
