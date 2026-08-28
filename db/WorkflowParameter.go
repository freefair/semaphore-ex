package db

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	gitvalidation "github.com/semaphoreui/semaphore/pkg/git"
)

const (
	MaxWorkflowParameters            = 64
	MaxWorkflowParameterDescription  = 512
	MaxWorkflowParameterStringBytes  = 4096
	MaxWorkflowParameterOptions      = 64
	MaxWorkflowSecretOptions         = 64
	MaxWorkflowNodeOverrideResources = 64
	MaxWorkflowNodeOverrideArguments = 16 * 1024
	MaxWorkflowNodeOverrideBranch    = 255
)

type WorkflowParameterType string

const (
	WorkflowParameterString          WorkflowParameterType = "string"
	WorkflowParameterInteger         WorkflowParameterType = "integer"
	WorkflowParameterBoolean         WorkflowParameterType = "boolean"
	WorkflowParameterEnumeration     WorkflowParameterType = "enumeration"
	WorkflowParameterSecretReference WorkflowParameterType = "secret_reference"
)

type WorkflowParameterSource string

const (
	WorkflowParameterSourceDefault WorkflowParameterSource = "default"
	WorkflowParameterSourceTrigger WorkflowParameterSource = "trigger"
	WorkflowParameterSourceUser    WorkflowParameterSource = "user"
)

// WorkflowSecretReference identifies an approved project credential without
// copying its value into a workflow definition or run snapshot.
type WorkflowSecretReference struct {
	AccessKeyID int `json:"access_key_id"`
}

func (reference WorkflowSecretReference) Fingerprint() string {
	if reference.AccessKeyID <= 0 {
		return ""
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("semaphore-workflow-secret-reference:v1\x00%d", reference.AccessKeyID)))
	return "sha256:" + hex.EncodeToString(hash[:])
}

type WorkflowSecretOption struct {
	AccessKeyID int    `json:"access_key_id"`
	Label       string `json:"label,omitempty"`
}

// WorkflowParameterDeclaration is persisted with the workflow definition.
// Secret parameters carry only an allow-list of credential references.
type WorkflowParameterDeclaration struct {
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	Type          WorkflowParameterType  `json:"type"`
	Required      bool                   `json:"required,omitempty"`
	Default       json.RawMessage        `json:"default,omitempty"`
	MinLength     int                    `json:"min_length,omitempty"`
	MaxLength     int                    `json:"max_length,omitempty"`
	Minimum       *int64                 `json:"minimum,omitempty"`
	Maximum       *int64                 `json:"maximum,omitempty"`
	Options       []string               `json:"options,omitempty"`
	SecretOptions []WorkflowSecretOption `json:"secret_options,omitempty"`
}

// WorkflowParameterSnapshot is immutable run input. Value is present only for
// non-secret parameters; secret parameters retain value-free reference data.
type WorkflowParameterSnapshot struct {
	Name                 string                   `json:"name"`
	Type                 WorkflowParameterType    `json:"type"`
	Source               WorkflowParameterSource  `json:"source"`
	Value                json.RawMessage          `json:"value,omitempty"`
	SecretReference      *WorkflowSecretReference `json:"secret_reference,omitempty"`
	ReferenceFingerprint string                   `json:"reference_fingerprint,omitempty"`
}

// WorkflowNodeOverridePolicy is authored with a node and bounds which
// run-start overrides a task runner may supply.
type WorkflowNodeOverridePolicy struct {
	InventoryIDs         []int    `json:"inventory_ids,omitempty"`
	EnvironmentIDs       []int    `json:"environment_ids,omitempty"`
	CredentialParameters []string `json:"credential_parameters,omitempty"`
	AllowArguments       bool     `json:"allow_arguments,omitempty"`
	AllowBranch          bool     `json:"allow_branch,omitempty"`
}

// WorkflowNodeOverride deliberately exposes only the task fields supported by
// the workflow contract. Unknown JSON fields are rejected at the API boundary.
type WorkflowNodeOverride struct {
	InventoryID    *int    `json:"inventory_id,omitempty"`
	EnvironmentIDs *[]int  `json:"environment_ids,omitempty"`
	Arguments      *string `json:"arguments,omitempty"`
	GitBranch      *string `json:"git_branch,omitempty"`
}

func ValidateWorkflowParameterDeclarations(declarations []WorkflowParameterDeclaration) error {
	if len(declarations) > MaxWorkflowParameters {
		return fmt.Errorf("workflow parameters exceed maximum count %d", MaxWorkflowParameters)
	}
	seen := make(map[string]struct{}, len(declarations))
	for index := range declarations {
		declaration := declarations[index]
		if !workflowArtifactNamePattern.MatchString(declaration.Name) {
			return fmt.Errorf("workflow parameter %d name is invalid", index)
		}
		if _, duplicate := seen[declaration.Name]; duplicate {
			return fmt.Errorf("workflow parameter %q is duplicated", declaration.Name)
		}
		seen[declaration.Name] = struct{}{}
		if len(declaration.Description) > MaxWorkflowParameterDescription {
			return fmt.Errorf("workflow parameter %q description is too long", declaration.Name)
		}
		if err := declaration.validateShape(); err != nil {
			return fmt.Errorf("workflow parameter %q: %w", declaration.Name, err)
		}
		if len(declaration.Default) != 0 {
			if _, err := resolveWorkflowParameterValue(declaration, declaration.Default, WorkflowParameterSourceDefault); err != nil {
				return fmt.Errorf("workflow parameter %q default: %w", declaration.Name, err)
			}
		}
	}
	return nil
}

func (declaration WorkflowParameterDeclaration) validateShape() error {
	switch declaration.Type {
	case WorkflowParameterString:
		if len(declaration.Options) != 0 || len(declaration.SecretOptions) != 0 || declaration.Minimum != nil || declaration.Maximum != nil {
			return errors.New("string parameter has incompatible bounds or options")
		}
		if declaration.MinLength < 0 || declaration.MaxLength < 0 || declaration.MaxLength > MaxWorkflowParameterStringBytes ||
			(declaration.MaxLength > 0 && declaration.MinLength > declaration.MaxLength) {
			return errors.New("string parameter length bounds are invalid")
		}
	case WorkflowParameterInteger:
		if declaration.MinLength != 0 || declaration.MaxLength != 0 || len(declaration.Options) != 0 || len(declaration.SecretOptions) != 0 {
			return errors.New("integer parameter has incompatible bounds or options")
		}
		if declaration.Minimum != nil && declaration.Maximum != nil && *declaration.Minimum > *declaration.Maximum {
			return errors.New("integer parameter bounds are invalid")
		}
	case WorkflowParameterBoolean:
		if declaration.MinLength != 0 || declaration.MaxLength != 0 || declaration.Minimum != nil || declaration.Maximum != nil ||
			len(declaration.Options) != 0 || len(declaration.SecretOptions) != 0 {
			return errors.New("boolean parameter cannot declare bounds or options")
		}
	case WorkflowParameterEnumeration:
		if declaration.MinLength != 0 || declaration.MaxLength != 0 || declaration.Minimum != nil || declaration.Maximum != nil || len(declaration.SecretOptions) != 0 {
			return errors.New("enumeration parameter has incompatible bounds or options")
		}
		if len(declaration.Options) == 0 || len(declaration.Options) > MaxWorkflowParameterOptions {
			return fmt.Errorf("enumeration parameter must declare between 1 and %d options", MaxWorkflowParameterOptions)
		}
		seen := make(map[string]struct{}, len(declaration.Options))
		for _, option := range declaration.Options {
			if option == "" || len(option) > MaxWorkflowParameterStringBytes {
				return errors.New("enumeration option is invalid")
			}
			if _, duplicate := seen[option]; duplicate {
				return fmt.Errorf("enumeration option %q is duplicated", option)
			}
			seen[option] = struct{}{}
		}
	case WorkflowParameterSecretReference:
		if declaration.MinLength != 0 || declaration.MaxLength != 0 || declaration.Minimum != nil || declaration.Maximum != nil || len(declaration.Options) != 0 {
			return errors.New("secret-reference parameter cannot declare scalar bounds or options")
		}
		if len(declaration.SecretOptions) == 0 || len(declaration.SecretOptions) > MaxWorkflowSecretOptions {
			return fmt.Errorf("secret-reference parameter must declare between 1 and %d approved credentials", MaxWorkflowSecretOptions)
		}
		seen := make(map[int]struct{}, len(declaration.SecretOptions))
		for _, option := range declaration.SecretOptions {
			if option.AccessKeyID <= 0 || len(option.Label) > 128 {
				return errors.New("secret-reference option is invalid")
			}
			if _, duplicate := seen[option.AccessKeyID]; duplicate {
				return fmt.Errorf("secret-reference option %d is duplicated", option.AccessKeyID)
			}
			seen[option.AccessKeyID] = struct{}{}
		}
	default:
		return errors.New("parameter type is invalid")
	}
	return nil
}

// ResolveWorkflowParameters applies default < trigger < user precedence and
// returns a value-safe immutable snapshot. Unknown fields are rejected.
func ResolveWorkflowParameters(
	declarations []WorkflowParameterDeclaration,
	triggerValues map[string]json.RawMessage,
	userValues map[string]json.RawMessage,
) (map[string]WorkflowParameterSnapshot, error) {
	if err := ValidateWorkflowParameterDeclarations(declarations); err != nil {
		return nil, err
	}
	byName := make(map[string]WorkflowParameterDeclaration, len(declarations))
	for _, declaration := range declarations {
		byName[declaration.Name] = declaration
	}
	for _, values := range []map[string]json.RawMessage{triggerValues, userValues} {
		for _, name := range sortedRawMessageKeys(values) {
			if _, ok := byName[name]; !ok {
				return nil, fmt.Errorf("unknown workflow parameter %q", name)
			}
		}
	}
	resolved := make(map[string]WorkflowParameterSnapshot, len(declarations))
	for _, declaration := range declarations {
		var value json.RawMessage
		var source WorkflowParameterSource
		if len(declaration.Default) != 0 {
			value, source = declaration.Default, WorkflowParameterSourceDefault
		}
		if trigger, ok := triggerValues[declaration.Name]; ok {
			value, source = trigger, WorkflowParameterSourceTrigger
		}
		if user, ok := userValues[declaration.Name]; ok {
			value, source = user, WorkflowParameterSourceUser
		}
		if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			if declaration.Required {
				return nil, fmt.Errorf("workflow parameter %q is required", declaration.Name)
			}
			continue
		}
		snapshot, err := resolveWorkflowParameterValue(declaration, value, source)
		if err != nil {
			return nil, fmt.Errorf("workflow parameter %q: %w", declaration.Name, err)
		}
		resolved[declaration.Name] = snapshot
	}
	return resolved, nil
}

func resolveWorkflowParameterValue(
	declaration WorkflowParameterDeclaration,
	raw json.RawMessage,
	source WorkflowParameterSource,
) (WorkflowParameterSnapshot, error) {
	if len(raw) > MaxWorkflowParameterStringBytes+1024 {
		return WorkflowParameterSnapshot{}, errors.New("value is too large")
	}
	snapshot := WorkflowParameterSnapshot{Name: declaration.Name, Type: declaration.Type, Source: source}
	switch declaration.Type {
	case WorkflowParameterString, WorkflowParameterEnumeration:
		var value string
		if err := decodeSingleJSON(raw, &value); err != nil {
			return WorkflowParameterSnapshot{}, errors.New("value must be a string")
		}
		if len(value) > MaxWorkflowParameterStringBytes || len(value) < declaration.MinLength ||
			(declaration.MaxLength > 0 && len(value) > declaration.MaxLength) {
			return WorkflowParameterSnapshot{}, errors.New("value violates length bounds")
		}
		if declaration.Type == WorkflowParameterEnumeration && !containsString(declaration.Options, value) {
			return WorkflowParameterSnapshot{}, errors.New("value is not an allowed option")
		}
		encoded, _ := json.Marshal(value)
		snapshot.Value = encoded
	case WorkflowParameterInteger:
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var number json.Number
		if err := decoder.Decode(&number); err != nil {
			return WorkflowParameterSnapshot{}, errors.New("value must be an integer")
		}
		if err := requireJSONEOF(decoder); err != nil {
			return WorkflowParameterSnapshot{}, errors.New("value must be an integer")
		}
		value, err := number.Int64()
		if err != nil {
			return WorkflowParameterSnapshot{}, errors.New("value must be an integer")
		}
		if declaration.Minimum != nil && value < *declaration.Minimum || declaration.Maximum != nil && value > *declaration.Maximum {
			return WorkflowParameterSnapshot{}, errors.New("value violates integer bounds")
		}
		snapshot.Value = json.RawMessage(number.String())
	case WorkflowParameterBoolean:
		var value bool
		if err := decodeSingleJSON(raw, &value); err != nil {
			return WorkflowParameterSnapshot{}, errors.New("value must be a boolean")
		}
		encoded, _ := json.Marshal(value)
		snapshot.Value = encoded
	case WorkflowParameterSecretReference:
		var reference WorkflowSecretReference
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&reference); err != nil || requireJSONEOF(decoder) != nil || reference.AccessKeyID <= 0 {
			return WorkflowParameterSnapshot{}, errors.New("value must be an approved secret reference")
		}
		approved := false
		for _, option := range declaration.SecretOptions {
			if option.AccessKeyID == reference.AccessKeyID {
				approved = true
				break
			}
		}
		if !approved {
			return WorkflowParameterSnapshot{}, errors.New("secret reference is not approved")
		}
		snapshot.SecretReference = &reference
		snapshot.ReferenceFingerprint = reference.Fingerprint()
	default:
		return WorkflowParameterSnapshot{}, errors.New("parameter type is invalid")
	}
	return snapshot, nil
}

func ValidateWorkflowNodeOverridePolicy(policy WorkflowNodeOverridePolicy) error {
	if err := validateWorkflowResourceIDs("inventory", policy.InventoryIDs); err != nil {
		return err
	}
	if err := validateWorkflowResourceIDs("environment", policy.EnvironmentIDs); err != nil {
		return err
	}
	if len(policy.CredentialParameters) > MaxWorkflowSecretOptions {
		return fmt.Errorf("workflow node credential allow-list exceeds maximum count %d", MaxWorkflowSecretOptions)
	}
	seen := make(map[string]struct{}, len(policy.CredentialParameters))
	for _, name := range policy.CredentialParameters {
		if !workflowArtifactNamePattern.MatchString(name) {
			return errors.New("workflow node credential allow-list contains an invalid parameter name")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("workflow node credential allow-list contains duplicates")
		}
		seen[name] = struct{}{}
	}
	return nil
}

func ValidateWorkflowNodeOverride(policy WorkflowNodeOverridePolicy, override WorkflowNodeOverride) error {
	if err := ValidateWorkflowNodeOverridePolicy(policy); err != nil {
		return err
	}
	if override.InventoryID != nil && !containsInt(policy.InventoryIDs, *override.InventoryID) {
		return errors.New("workflow node inventory override is not approved")
	}
	if override.EnvironmentIDs != nil {
		if len(*override.EnvironmentIDs) > MaxWorkflowNodeOverrideResources {
			return errors.New("workflow node environment override contains too many resources")
		}
		seen := make(map[int]struct{}, len(*override.EnvironmentIDs))
		for _, id := range *override.EnvironmentIDs {
			if !containsInt(policy.EnvironmentIDs, id) {
				return errors.New("workflow node environment override is not approved")
			}
			if _, duplicate := seen[id]; duplicate {
				return errors.New("workflow node environment override contains duplicates")
			}
			seen[id] = struct{}{}
		}
	}
	if override.Arguments != nil {
		if !policy.AllowArguments {
			return errors.New("workflow node arguments override is not allowed")
		}
		if len(*override.Arguments) > MaxWorkflowNodeOverrideArguments || !json.Valid([]byte(*override.Arguments)) {
			return errors.New("workflow node arguments override is invalid")
		}
	}
	if override.GitBranch != nil {
		if !policy.AllowBranch {
			return errors.New("workflow node branch override is not allowed")
		}
		if strings.TrimSpace(*override.GitBranch) == "" || len(*override.GitBranch) > MaxWorkflowNodeOverrideBranch {
			return errors.New("workflow node branch override is invalid")
		}
		if err := gitvalidation.ValidateGitBranch(*override.GitBranch, "workflow node"); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowResourceIDs(name string, ids []int) error {
	if len(ids) > MaxWorkflowNodeOverrideResources {
		return fmt.Errorf("workflow node %s allow-list exceeds maximum count %d", name, MaxWorkflowNodeOverrideResources)
	}
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("workflow node %s allow-list contains an invalid id", name)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("workflow node %s allow-list contains duplicates", name)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func decodeSingleJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON value contains trailing data")
	}
	return nil
}

func sortedRawMessageKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
