package recipe

import (
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/plan"
)

// HostContractVersion is deliberately separate from runtime-recipe.v1. Host
// recipes coordinate target-bound infrastructure work; they do not describe a
// serving runtime or an active capability.
const HostContractVersion = "host-recipe.v1"

type HostDocument struct {
	ContractVersion string               `json:"contractVersion" yaml:"contractVersion"`
	RecipeID        string               `json:"recipeId" yaml:"recipeId"`
	RecipeVersion   string               `json:"recipeVersion" yaml:"recipeVersion"`
	Execution       HostExecution        `json:"execution" yaml:"execution"`
	Inputs          map[string]InputSpec `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Compatibility   CompatibilitySpec    `json:"compatibility,omitempty" yaml:"compatibility,omitempty"`
	Plan            plan.Document        `json:"plan" yaml:"plan"`
	OutputMapping   map[string]string    `json:"outputMapping,omitempty" yaml:"outputMapping,omitempty"`
}

type HostExecution struct {
	Coordinator string `json:"coordinator" yaml:"coordinator"`
	Mode        string `json:"mode" yaml:"mode"`
}

type HostLoaded struct {
	Document     HostDocument
	ExpandedPlan plan.Document
	Inputs       map[string]any
	Source       SourceMetadata
	Raw          []byte
}

func LoadHost(request SourceRequest) (HostLoaded, error) {
	raw, metadata, err := loadSource(request)
	if err != nil {
		return HostLoaded{}, err
	}
	var document HostDocument
	if err := decodeValue(raw, &document, "host recipe"); err != nil {
		return HostLoaded{}, err
	}
	if err := ValidateHostEnvelope(document); err != nil {
		return HostLoaded{}, err
	}
	hash, err := CanonicalHash(document)
	if err != nil {
		return HostLoaded{}, fmt.Errorf("hash host recipe: %w", err)
	}
	metadata.RecipeHash = hash
	return HostLoaded{Document: document, Source: metadata, Raw: append([]byte(nil), raw...)}, nil
}

func DecodeHost(raw []byte) (HostDocument, error) {
	var document HostDocument
	if err := decodeValue(raw, &document, "host recipe"); err != nil {
		return HostDocument{}, err
	}
	return document, nil
}

func ResolveHostInputs(document HostDocument, values map[string]any) (HostLoaded, error) {
	if err := ValidateHostEnvelope(document); err != nil {
		return HostLoaded{}, err
	}
	resolved, err := resolveInputs(document.Inputs, values)
	if err != nil {
		return HostLoaded{}, err
	}
	variables := reservedPlanVariables(document.Plan.Variables)
	variables["inputs"] = resolved
	expanded := document.Plan
	expanded.Variables = variables
	identity, err := plan.InterpolateArgs(map[string]any{"idempotencyKey": expanded.IdempotencyKey}, plan.EvalContext{Variables: variables})
	if err != nil {
		return HostLoaded{}, fmt.Errorf("resolve host recipe plan identity: %w", err)
	}
	if value, ok := identity["idempotencyKey"].(string); ok {
		expanded.IdempotencyKey = value
	}
	return HostLoaded{Document: document, ExpandedPlan: expanded, Inputs: resolved}, nil
}

func (loaded HostLoaded) RedactedInputs() map[string]any {
	redacted := make(map[string]any, len(loaded.Inputs))
	for name, value := range loaded.Inputs {
		if loaded.Document.Inputs[name].Secret {
			redacted[name] = "[redacted]"
		} else {
			redacted[name] = value
		}
	}
	return redacted
}

func (loaded HostLoaded) SecretInputNames() []string {
	names := make([]string, 0)
	for name, spec := range loaded.Document.Inputs {
		if spec.Secret {
			names = append(names, name)
		}
	}
	return sortedStrings(names)
}

func (loaded HostLoaded) Validate(capabilities map[string]plan.Capability, catalogRevision string) error {
	if err := ValidateHostEnvelope(loaded.Document); err != nil {
		return err
	}
	if loaded.ExpandedPlan.ContractVersion == "" {
		return fmt.Errorf("host recipe plan is not resolved; resolve inputs first")
	}
	for _, name := range loaded.Document.Compatibility.RequiredTools {
		if _, ok := capabilities[name]; !ok {
			return fmt.Errorf("host recipe requires unsupported host-agent capability %q", name)
		}
	}
	if err := rejectNestedPlanRuns(loaded.ExpandedPlan); err != nil {
		return err
	}
	if err := validateHostTargets(loaded.ExpandedPlan); err != nil {
		return err
	}
	if err := plan.Validate(loaded.ExpandedPlan, capabilities, catalogRevision); err != nil {
		return fmt.Errorf("validate host recipe plan: %w", err)
	}
	return nil
}

func ValidateHostEnvelope(document HostDocument) error {
	if document.ContractVersion != HostContractVersion {
		return fmt.Errorf("unsupported host recipe contractVersion %q", document.ContractVersion)
	}
	if strings.TrimSpace(document.RecipeID) == "" || strings.TrimSpace(document.RecipeVersion) == "" {
		return fmt.Errorf("recipeId and recipeVersion are required")
	}
	if document.Execution.Coordinator != "platform" || document.Execution.Mode != "distributed" {
		return fmt.Errorf("execution must be coordinator=platform and mode=distributed")
	}
	if document.Plan.ContractVersion != plan.ContractVersion {
		return fmt.Errorf("host recipe plan must use %s", plan.ContractVersion)
	}
	if len(document.Plan.Nodes) == 0 {
		return fmt.Errorf("host recipe plan must contain at least one node")
	}
	return nil
}

func validateHostTargets(document plan.Document) error {
	for _, node := range document.Nodes {
		if node.Action == nil {
			continue
		}
		if node.Target == nil {
			return fmt.Errorf("host recipe action node %q requires an exact target binding", node.ID)
		}
		ref := strings.TrimSpace(node.Target.HostRef)
		if !strings.HasPrefix(ref, "${vars.inputs.") || !strings.HasSuffix(ref, "}") || strings.Contains(ref, "/") {
			return fmt.Errorf("host recipe node %q target hostRef must be an exact vars.inputs reference", node.ID)
		}
	}
	return nil
}
