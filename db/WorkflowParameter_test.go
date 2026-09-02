package db

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workflowParameterRaw(value string) json.RawMessage { return json.RawMessage(value) }

func TestResolveWorkflowParametersAppliesTypedPrecedenceAndRedaction(t *testing.T) {
	minimum, maximum := int64(1), int64(10)
	declarations := []WorkflowParameterDeclaration{
		{Name: "region", Type: WorkflowParameterString, Required: true, Default: workflowParameterRaw(`"eu"`), MinLength: 2, MaxLength: 8},
		{Name: "replicas", Type: WorkflowParameterInteger, Default: workflowParameterRaw(`2`), Minimum: &minimum, Maximum: &maximum},
		{Name: "deploy", Type: WorkflowParameterBoolean, Default: workflowParameterRaw(`false`)},
		{Name: "tier", Type: WorkflowParameterEnumeration, Default: workflowParameterRaw(`"dev"`), Options: []string{"dev", "prod"}},
		{Name: "token", Type: WorkflowParameterSecretReference, Required: true, SecretOptions: []WorkflowSecretOption{{AccessKeyID: 41, Label: "deployment token"}}},
	}

	resolved, err := ResolveWorkflowParameters(
		declarations,
		map[string]json.RawMessage{"region": workflowParameterRaw(`"us"`), "replicas": workflowParameterRaw(`3`)},
		map[string]json.RawMessage{"replicas": workflowParameterRaw(`5`), "token": workflowParameterRaw(`{"access_key_id":41}`)},
	)
	require.NoError(t, err)
	assert.JSONEq(t, `"us"`, string(resolved["region"].Value))
	assert.Equal(t, WorkflowParameterSourceTrigger, resolved["region"].Source)
	assert.JSONEq(t, `5`, string(resolved["replicas"].Value))
	assert.Equal(t, WorkflowParameterSourceUser, resolved["replicas"].Source)
	require.NotNil(t, resolved["token"].SecretReference)
	assert.Equal(t, 41, resolved["token"].SecretReference.AccessKeyID)
	assert.NotEmpty(t, resolved["token"].ReferenceFingerprint)

	encoded, err := json.Marshal(resolved)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "deployment-secret")
}

func TestResolveWorkflowParametersRejectsUnknownInvalidAndPlaintextValues(t *testing.T) {
	minimum, maximum := int64(2), int64(4)
	tests := []struct {
		name  string
		decls []WorkflowParameterDeclaration
		input map[string]json.RawMessage
	}{
		{name: "unknown", decls: []WorkflowParameterDeclaration{{Name: "known", Type: WorkflowParameterString}}, input: map[string]json.RawMessage{"other": workflowParameterRaw(`"x"`)}},
		{name: "missing required", decls: []WorkflowParameterDeclaration{{Name: "required", Type: WorkflowParameterBoolean, Required: true}}},
		{name: "string below minimum", decls: []WorkflowParameterDeclaration{{Name: "name", Type: WorkflowParameterString, MinLength: 2}}, input: map[string]json.RawMessage{"name": workflowParameterRaw(`"x"`)}},
		{name: "string above maximum", decls: []WorkflowParameterDeclaration{{Name: "name", Type: WorkflowParameterString, MaxLength: 3}}, input: map[string]json.RawMessage{"name": workflowParameterRaw(`"long"`)}},
		{name: "integer below minimum", decls: []WorkflowParameterDeclaration{{Name: "count", Type: WorkflowParameterInteger, Minimum: &minimum}}, input: map[string]json.RawMessage{"count": workflowParameterRaw(`1`)}},
		{name: "integer above maximum", decls: []WorkflowParameterDeclaration{{Name: "count", Type: WorkflowParameterInteger, Maximum: &maximum}}, input: map[string]json.RawMessage{"count": workflowParameterRaw(`5`)}},
		{name: "invalid boolean", decls: []WorkflowParameterDeclaration{{Name: "enabled", Type: WorkflowParameterBoolean}}, input: map[string]json.RawMessage{"enabled": workflowParameterRaw(`"yes"`)}},
		{name: "invalid enumeration", decls: []WorkflowParameterDeclaration{{Name: "tier", Type: WorkflowParameterEnumeration, Options: []string{"dev", "prod"}}}, input: map[string]json.RawMessage{"tier": workflowParameterRaw(`"staging"`)}},
		{name: "plaintext secret", decls: []WorkflowParameterDeclaration{{Name: "secret", Type: WorkflowParameterSecretReference, SecretOptions: []WorkflowSecretOption{{AccessKeyID: 7}}}}, input: map[string]json.RawMessage{"secret": workflowParameterRaw(`"deployment-secret"`)}},
		{name: "unapproved secret", decls: []WorkflowParameterDeclaration{{Name: "secret", Type: WorkflowParameterSecretReference, SecretOptions: []WorkflowSecretOption{{AccessKeyID: 7}}}}, input: map[string]json.RawMessage{"secret": workflowParameterRaw(`{"access_key_id":8}`)}},
		{name: "invalid integer", decls: []WorkflowParameterDeclaration{{Name: "count", Type: WorkflowParameterInteger}}, input: map[string]json.RawMessage{"count": workflowParameterRaw(`1.5`)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveWorkflowParameters(test.decls, nil, test.input)
			require.Error(t, err)
		})
	}
}

func TestValidateWorkflowParameterDeclarationsRejectsInvalidShapes(t *testing.T) {
	minimum, maximum := int64(4), int64(2)
	tests := []struct {
		name string
		decl WorkflowParameterDeclaration
	}{
		{name: "invalid name", decl: WorkflowParameterDeclaration{Name: "not valid", Type: WorkflowParameterString}},
		{name: "string with numeric bound", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterString, Minimum: &minimum}},
		{name: "reversed string bounds", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterString, MinLength: 4, MaxLength: 2}},
		{name: "reversed integer bounds", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterInteger, Minimum: &minimum, Maximum: &maximum}},
		{name: "boolean with options", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterBoolean, Options: []string{"true"}}},
		{name: "enumeration without options", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterEnumeration}},
		{name: "enumeration duplicate option", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterEnumeration, Options: []string{"one", "one"}}},
		{name: "secret without approved credentials", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterSecretReference}},
		{name: "secret duplicate credential", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterSecretReference, SecretOptions: []WorkflowSecretOption{{AccessKeyID: 1}, {AccessKeyID: 1}}}},
		{name: "invalid type", decl: WorkflowParameterDeclaration{Name: "value", Type: WorkflowParameterType("unsupported")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, ValidateWorkflowParameterDeclarations([]WorkflowParameterDeclaration{test.decl}))
		})
	}
	require.Error(t, ValidateWorkflowParameterDeclarations([]WorkflowParameterDeclaration{
		{Name: "duplicate", Type: WorkflowParameterString},
		{Name: "duplicate", Type: WorkflowParameterString},
	}))
}

func TestWorkflowSecretReferenceFingerprintContainsNoValue(t *testing.T) {
	fingerprint := (WorkflowSecretReference{AccessKeyID: 41}).Fingerprint()
	assert.True(t, strings.HasPrefix(fingerprint, "sha256:"))
	assert.Len(t, fingerprint, len("sha256:")+64)
	assert.Equal(t, fingerprint, (WorkflowSecretReference{AccessKeyID: 41}).Fingerprint())
	assert.NotEqual(t, fingerprint, (WorkflowSecretReference{AccessKeyID: 42}).Fingerprint())
}

func TestWorkflowSecretReferenceSupportsApprovedGlobalCredentials(t *testing.T) {
	declaration := WorkflowParameterDeclaration{
		Name: "token", Type: WorkflowParameterSecretReference, Required: true,
		SecretOptions: []WorkflowSecretOption{{GlobalCredentialID: 51, Label: "Global deployment token"}},
	}

	resolved, err := ResolveWorkflowParameters(
		[]WorkflowParameterDeclaration{declaration}, nil,
		map[string]json.RawMessage{"token": workflowParameterRaw(`{"global_credential_id":51}`)},
	)

	require.NoError(t, err)
	require.NotNil(t, resolved["token"].SecretReference)
	assert.Equal(t, 51, resolved["token"].SecretReference.GlobalCredentialID)
	assert.Zero(t, resolved["token"].SecretReference.AccessKeyID)
	assert.NotEqual(t,
		(WorkflowSecretReference{AccessKeyID: 51}).Fingerprint(),
		resolved["token"].ReferenceFingerprint,
	)
}

func TestWorkflowSecretReferenceRejectsAmbiguousCredentialKinds(t *testing.T) {
	declaration := WorkflowParameterDeclaration{
		Name: "token", Type: WorkflowParameterSecretReference,
		SecretOptions: []WorkflowSecretOption{{AccessKeyID: 7, GlobalCredentialID: 8}},
	}
	require.Error(t, ValidateWorkflowParameterDeclarations([]WorkflowParameterDeclaration{declaration}))

	valid := WorkflowParameterDeclaration{
		Name: "token", Type: WorkflowParameterSecretReference,
		SecretOptions: []WorkflowSecretOption{{GlobalCredentialID: 8}},
	}
	_, err := ResolveWorkflowParameters([]WorkflowParameterDeclaration{valid}, nil,
		map[string]json.RawMessage{"token": workflowParameterRaw(`{"access_key_id":7,"global_credential_id":8}`)})
	require.Error(t, err)
}

func TestValidateWorkflowNodeOverrideEnforcesAllowList(t *testing.T) {
	policy := WorkflowNodeOverridePolicy{
		InventoryIDs: []int{11, 12}, EnvironmentIDs: []int{21, 22}, AllowArguments: true,
	}
	branch := "unapproved"
	require.Error(t, ValidateWorkflowNodeOverride(policy, WorkflowNodeOverride{GitBranch: &branch}))
	inventory := 13
	require.Error(t, ValidateWorkflowNodeOverride(policy, WorkflowNodeOverride{InventoryID: &inventory}))
	inventory = 12
	arguments := `["--check"]`
	require.NoError(t, ValidateWorkflowNodeOverride(policy, WorkflowNodeOverride{
		InventoryID: &inventory, Arguments: &arguments,
	}))
}

func TestValidateWorkflowNodeOverrideRejectsInvalidGitBranch(t *testing.T) {
	branch := "refs/heads/release..candidate"
	require.Error(t, ValidateWorkflowNodeOverride(
		WorkflowNodeOverridePolicy{AllowBranch: true},
		WorkflowNodeOverride{GitBranch: &branch},
	))
}

func TestValidateWorkflowNodeCredentialParameterAllowList(t *testing.T) {
	assert.NoError(t, ValidateWorkflowNodeOverridePolicy(WorkflowNodeOverridePolicy{
		CredentialParameters: []string{"deploy_token"},
	}))
	assert.Error(t, ValidateWorkflowNodeOverridePolicy(WorkflowNodeOverridePolicy{
		CredentialParameters: []string{"bad name"},
	}))
	assert.Error(t, ValidateWorkflowNodeOverridePolicy(WorkflowNodeOverridePolicy{
		CredentialParameters: []string{"token", "token"},
	}))
}
