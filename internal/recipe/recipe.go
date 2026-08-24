package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/plan"
	"go.yaml.in/yaml/v3"
)

const ContractVersion = "runtime-recipe.v1"

const (
	maxRecipeBytes = 512 * 1024
	githubRawHost  = "raw.githubusercontent.com"
)

var commitRevisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// Document is the independently versioned recipe envelope. The embedded plan
// remains the only execution language; this package only resolves and
// validates its source and inputs.
type Document struct {
	ContractVersion string               `json:"contractVersion" yaml:"contractVersion"`
	RecipeID        string               `json:"recipeId" yaml:"recipeId"`
	RecipeVersion   string               `json:"recipeVersion" yaml:"recipeVersion"`
	Runtime         RuntimeSpec          `json:"runtime" yaml:"runtime"`
	Inputs          map[string]InputSpec `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Compatibility   CompatibilitySpec    `json:"compatibility,omitempty" yaml:"compatibility,omitempty"`
	Plan            plan.Document        `json:"plan" yaml:"plan"`
	OutputMapping   map[string]string    `json:"outputMapping,omitempty" yaml:"outputMapping,omitempty"`
	Activation      *ActivationSpec      `json:"activation,omitempty" yaml:"activation,omitempty"`
}

type RuntimeSpec struct {
	ID              string   `json:"id" yaml:"id"`
	ServingContract string   `json:"servingContract" yaml:"servingContract"`
	Capabilities    []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

type InputSpec struct {
	Schema      map[string]any `json:"schema,omitempty" yaml:"schema,omitempty"`
	Default     any            `json:"default,omitempty" yaml:"default,omitempty"`
	Required    bool           `json:"required,omitempty" yaml:"required,omitempty"`
	Secret      bool           `json:"secret,omitempty" yaml:"secret,omitempty"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
}

type CompatibilitySpec struct {
	MinHostAgentVersion string   `json:"minHostAgentVersion,omitempty" yaml:"minHostAgentVersion,omitempty"`
	RequiredTools       []string `json:"requiredTools,omitempty" yaml:"requiredTools,omitempty"`
}

// ActivationSpec describes the neutral serving contract that must be checked
// before a successful recipe run may replace the host's active runtime for a
// capability. It names recipe inputs, rather than an implementation-specific
// command or API, so the host agent owns one validation flow for every
// compatible runtime.
type ActivationSpec struct {
	Capability      string            `json:"capability" yaml:"capability"`
	ServingContract string            `json:"servingContract" yaml:"servingContract"`
	InputBindings   map[string]string `json:"inputBindings" yaml:"inputBindings"`
}

type SourceRequest struct {
	Source        string
	Revision      string
	SHA256        string
	RequireSHA256 bool
}

type SourceMetadata struct {
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	Revision   string `json:"revision,omitempty"`
	RawSHA256  string `json:"rawSha256"`
	RecipeHash string `json:"recipeHash"`
}

type Loaded struct {
	Document     Document
	ExpandedPlan plan.Document
	Inputs       map[string]any
	Source       SourceMetadata
	Raw          []byte
}

func (l Loaded) RedactedInputs() map[string]any {
	redacted := make(map[string]any, len(l.Inputs))
	for name, value := range l.Inputs {
		if l.Document.Inputs[name].Secret {
			redacted[name] = "[redacted]"
			continue
		}
		redacted[name] = value
	}
	return redacted
}

func (l Loaded) SecretInputNames() []string {
	names := make([]string, 0)
	for name, spec := range l.Document.Inputs {
		if spec.Secret {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func Load(request SourceRequest) (Loaded, error) {
	raw, metadata, err := loadSource(request)
	if err != nil {
		return Loaded{}, err
	}
	doc, err := decode(raw)
	if err != nil {
		return Loaded{}, err
	}
	if err := validateEnvelope(doc); err != nil {
		return Loaded{}, err
	}
	recipeHash, err := hashDocument(doc)
	if err != nil {
		return Loaded{}, fmt.Errorf("hash runtime recipe: %w", err)
	}
	metadata.RecipeHash = recipeHash
	return Loaded{Document: doc, Source: metadata, Raw: append([]byte(nil), raw...)}, nil
}

func (l *Loaded) ResolveInputs(values map[string]any) error {
	resolved := make(map[string]any, len(l.Document.Inputs))
	for name, value := range values {
		if _, ok := l.Document.Inputs[name]; !ok {
			return fmt.Errorf("unknown recipe input %q", name)
		}
		resolved[name] = value
	}
	for name, spec := range l.Document.Inputs {
		value, present := resolved[name]
		if !present && spec.Default != nil {
			value = spec.Default
			present = true
		}
		if !present {
			if spec.Required {
				return fmt.Errorf("required recipe input %q is missing", name)
			}
			continue
		}
		if len(spec.Schema) > 0 {
			if err := plan.ValidateJSON(spec.Schema, value); err != nil {
				return fmt.Errorf("recipe input %q: %w", name, err)
			}
		}
		resolved[name] = value
	}
	variables := reservedPlanVariables(l.Document.Plan.Variables)
	variables["inputs"] = resolved
	l.ExpandedPlan = l.Document.Plan
	l.ExpandedPlan.Variables = variables
	identity, err := plan.InterpolateArgs(map[string]any{"idempotencyKey": l.ExpandedPlan.IdempotencyKey}, plan.EvalContext{Variables: variables})
	if err != nil {
		return fmt.Errorf("resolve recipe plan identity: %w", err)
	}
	if value, ok := identity["idempotencyKey"].(string); ok {
		l.ExpandedPlan.IdempotencyKey = value
	}
	l.Inputs = resolved
	return nil
}

func (l Loaded) Validate(capabilities map[string]plan.Capability, catalogRevision string) error {
	if err := validateEnvelope(l.Document); err != nil {
		return err
	}
	if l.ExpandedPlan.ContractVersion == "" {
		return fmt.Errorf("recipe plan is not resolved; resolve inputs first")
	}
	for _, name := range l.Document.Compatibility.RequiredTools {
		if _, ok := capabilities[name]; !ok {
			return fmt.Errorf("recipe requires unsupported host-agent capability %q", name)
		}
	}
	if err := rejectNestedPlanRuns(l.ExpandedPlan); err != nil {
		return err
	}
	if err := plan.Validate(l.ExpandedPlan, capabilities, catalogRevision); err != nil {
		return fmt.Errorf("validate recipe host plan: %w", err)
	}
	return nil
}

// ValidateHostAgentVersion compares the numeric major/minor/patch portion of
// a recipe requirement with the running agent. Development builds cannot
// honestly satisfy a release requirement, so they fail closed.
func ValidateHostAgentVersion(minimum, current string) error {
	minimum = strings.TrimSpace(strings.TrimPrefix(minimum, "v"))
	if minimum == "" {
		return nil
	}
	required, ok := parseVersion(minimum)
	if !ok {
		return fmt.Errorf("invalid minimum host-agent version %q", minimum)
	}
	actual, ok := parseVersion(strings.TrimSpace(strings.TrimPrefix(current, "v")))
	if !ok {
		return fmt.Errorf("cannot verify minimum host-agent version %q against %q", minimum, current)
	}
	for i := range required {
		if actual[i] > required[i] {
			return nil
		}
		if actual[i] < required[i] {
			return fmt.Errorf("recipe requires host-agent version >= %s, running %s", minimum, current)
		}
	}
	return nil
}

func parseVersion(value string) ([3]int, bool) {
	var result [3]int
	parts := strings.SplitN(value, ".", 4)
	if len(parts) < 3 {
		return result, false
	}
	for index := 0; index < 3; index++ {
		if parts[index] == "" {
			return result, false
		}
		for _, character := range parts[index] {
			if character < '0' || character > '9' {
				return result, false
			}
		}
		var parsed int
		if _, err := fmt.Sscanf(parts[index], "%d", &parsed); err != nil {
			return result, false
		}
		result[index] = parsed
	}
	return result, true
}

func rejectNestedPlanRuns(doc plan.Document) error {
	for _, node := range doc.Nodes {
		actions := []*plan.Action{node.Action, node.Compensate}
		if node.Recover != nil {
			actions = append(actions, &node.Recover.Action)
		}
		for _, action := range actions {
			if action == nil {
				continue
			}
			if action.Tool == "run_host_plan" || action.Tool == "run_runtime_recipe" {
				return fmt.Errorf("recipe node %q cannot recursively run %s", node.ID, action.Tool)
			}
		}
		if node.Validate != nil && (node.Validate.Tool == "run_host_plan" || node.Validate.Tool == "run_runtime_recipe") {
			return fmt.Errorf("recipe node %q cannot use %s as validation", node.ID, node.Validate.Tool)
		}
	}
	return nil
}

func decode(raw []byte) (Document, error) {
	var doc Document
	if err := decodeValue(raw, &doc, "runtime recipe"); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func decodeValue(raw []byte, target any, kind string) error {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(raw, target); err != nil {
			return fmt.Errorf("decode %s JSON: %w", kind, err)
		}
	} else if err := yaml.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode %s YAML: %w", kind, err)
	}
	return nil
}

func validateEnvelope(doc Document) error {
	if doc.ContractVersion != ContractVersion {
		return fmt.Errorf("unsupported runtime recipe contractVersion %q", doc.ContractVersion)
	}
	if strings.TrimSpace(doc.RecipeID) == "" || strings.TrimSpace(doc.RecipeVersion) == "" {
		return fmt.Errorf("recipeId and recipeVersion are required")
	}
	if strings.TrimSpace(doc.Runtime.ID) == "" {
		return fmt.Errorf("runtime.id is required")
	}
	if strings.TrimSpace(doc.Runtime.ServingContract) == "" {
		return fmt.Errorf("runtime.servingContract is required")
	}
	supportedServingContracts := map[string]struct{}{
		"openai-chat.v1":   {},
		"http-exposure.v1": {},
		"kubernetes.v1":    {},
	}
	if _, ok := supportedServingContracts[doc.Runtime.ServingContract]; !ok {
		return fmt.Errorf("unsupported runtime serving contract %q", doc.Runtime.ServingContract)
	}
	if doc.Activation != nil {
		if strings.TrimSpace(doc.Activation.Capability) == "" {
			return fmt.Errorf("activation.capability is required")
		}
		if doc.Activation.ServingContract != doc.Runtime.ServingContract {
			return fmt.Errorf("activation.servingContract must match runtime.servingContract")
		}
		if len(doc.Activation.InputBindings) == 0 {
			return fmt.Errorf("activation.inputBindings is required")
		}
		for binding, inputName := range doc.Activation.InputBindings {
			if strings.TrimSpace(binding) == "" || strings.TrimSpace(inputName) == "" {
				return fmt.Errorf("activation.inputBindings must contain non-empty names")
			}
			if _, ok := doc.Inputs[inputName]; !ok {
				return fmt.Errorf("activation.inputBindings[%q] references unknown input %q", binding, inputName)
			}
		}
	}
	if doc.Plan.ContractVersion != plan.ContractVersion {
		return fmt.Errorf("recipe plan must use %s", plan.ContractVersion)
	}
	if len(doc.Plan.Nodes) == 0 {
		return fmt.Errorf("recipe plan must contain at least one node")
	}
	return nil
}

func hashDocument(doc Document) (string, error) {
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func loadSource(request SourceRequest) ([]byte, SourceMetadata, error) {
	source := strings.TrimSpace(request.Source)
	if source == "" {
		return nil, SourceMetadata{}, fmt.Errorf("recipe source is required")
	}
	if strings.HasPrefix(source, "github:") {
		parsed, err := parseGitHubReference(strings.TrimPrefix(source, "github:"), request.Revision)
		if err != nil {
			return nil, SourceMetadata{}, err
		}
		return fetchRemote(parsed.URL, parsed.Revision, request.SHA256, request.RequireSHA256)
	}
	if strings.HasPrefix(source, "https://") {
		parsed, err := parseRawGitHubURL(source, request.Revision)
		if err != nil {
			return nil, SourceMetadata{}, err
		}
		return fetchRemote(parsed.URL, parsed.Revision, request.SHA256, request.RequireSHA256)
	}
	return readLocal(source, request.SHA256)
}

type githubReference struct {
	URL      string
	Revision string
}

func parseGitHubReference(value, revision string) (githubReference, error) {
	parts := strings.SplitN(strings.TrimSpace(value), "@", 2)
	if len(parts) != 2 || !commitRevisionPattern.MatchString(parts[1]) {
		return githubReference{}, fmt.Errorf("GitHub recipe source requires @<40-character-commit-sha>")
	}
	pathParts := strings.Split(strings.Trim(parts[0], "/"), "/")
	if len(pathParts) < 3 || pathParts[0] == "" || pathParts[1] == "" {
		return githubReference{}, fmt.Errorf("GitHub recipe source must be github:<owner>/<repo>/<path>@<commit>")
	}
	if err := validateGitHubPath(pathParts); err != nil {
		return githubReference{}, err
	}
	if strings.TrimSpace(revision) != "" && revision != parts[1] {
		return githubReference{}, fmt.Errorf("GitHub source revision disagrees with source reference")
	}
	return githubReference{
		URL:      "https://" + githubRawHost + "/" + strings.Join(pathParts[:2], "/") + "/" + parts[1] + "/" + strings.Join(pathParts[2:], "/"),
		Revision: strings.ToLower(parts[1]),
	}, nil
}

func parseRawGitHubURL(raw, revision string) (githubReference, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != githubRawHost {
		return githubReference{}, fmt.Errorf("recipe URL must be an HTTPS raw.githubusercontent.com URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] == "" || !commitRevisionPattern.MatchString(parts[2]) {
		return githubReference{}, fmt.Errorf("raw GitHub recipe URL must contain an immutable commit revision")
	}
	if err := validateGitHubPath(parts[:2]); err != nil {
		return githubReference{}, err
	}
	if err := validateGitHubPath(parts[3:]); err != nil {
		return githubReference{}, err
	}
	if strings.TrimSpace(revision) != "" && revision != parts[2] {
		return githubReference{}, fmt.Errorf("GitHub source revision disagrees with URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return githubReference{URL: parsed.String(), Revision: strings.ToLower(parts[2])}, nil
}

func validateGitHubPath(parts []string) error {
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `\\`) {
			return fmt.Errorf("recipe source path contains an invalid traversal segment")
		}
	}
	return nil
}

func readLocal(path, expected string) ([]byte, SourceMetadata, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, SourceMetadata{}, fmt.Errorf("stat recipe source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, SourceMetadata{}, fmt.Errorf("recipe source must be a regular, non-symlink file")
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, SourceMetadata{}, fmt.Errorf("open recipe source: %w", err)
	}
	defer file.Close()
	raw, err := readBounded(file)
	if err != nil {
		return nil, SourceMetadata{}, fmt.Errorf("read recipe source: %w", err)
	}
	rawHash := hashBytes(raw)
	if err := verifyExpectedHash(expected, rawHash); err != nil {
		return nil, SourceMetadata{}, err
	}
	return raw, SourceMetadata{Kind: "path", Source: filepath.Clean(path), RawSHA256: rawHash}, nil
}

func fetchRemote(rawURL, revision, expected string, requireHash bool) ([]byte, SourceMetadata, error) {
	if requireHash && strings.TrimSpace(expected) == "" {
		return nil, SourceMetadata{}, fmt.Errorf("remote recipe mutation requires an expected sha256")
	}
	if !commitRevisionPattern.MatchString(revision) {
		return nil, SourceMetadata{}, fmt.Errorf("remote recipe source requires an immutable 40-character commit revision")
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.Host != githubRawHost {
				return fmt.Errorf("recipe redirect leaves raw.githubusercontent.com")
			}
			return nil
		},
	}
	response, err := client.Get(rawURL)
	if err != nil {
		return nil, SourceMetadata{}, fmt.Errorf("fetch recipe source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, SourceMetadata{}, fmt.Errorf("fetch recipe source: HTTP %d", response.StatusCode)
	}
	raw, err := readBounded(response.Body)
	if err != nil {
		return nil, SourceMetadata{}, fmt.Errorf("read recipe source: %w", err)
	}
	rawHash := hashBytes(raw)
	if err := verifyExpectedHash(expected, rawHash); err != nil {
		return nil, SourceMetadata{}, err
	}
	return raw, SourceMetadata{Kind: "github", Source: rawURL, Revision: strings.ToLower(revision), RawSHA256: rawHash}, nil
}

func readBounded(reader io.Reader) ([]byte, error) {
	truncated, err := io.ReadAll(io.LimitReader(reader, maxRecipeBytes+1))
	if err != nil {
		return nil, err
	}
	if len(truncated) > maxRecipeBytes {
		return nil, fmt.Errorf("recipe source exceeds %d bytes", maxRecipeBytes)
	}
	if len(strings.TrimSpace(string(truncated))) == 0 {
		return nil, fmt.Errorf("recipe source is empty")
	}
	return truncated, nil
}

func hashBytes(value []byte) string {
	hash := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func verifyExpectedHash(expected, actual string) error {
	expected = strings.TrimSpace(strings.TrimPrefix(expected, "sha256:"))
	if expected == "" {
		return nil
	}
	if !regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(expected) {
		return fmt.Errorf("recipe sha256 must be a 64-character hexadecimal digest")
	}
	if !strings.EqualFold("sha256:"+expected, actual) {
		return fmt.Errorf("recipe sha256 mismatch: expected sha256:%s, got %s", strings.ToLower(expected), actual)
	}
	return nil
}
