package pro_interfaces

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOIDCGroupClaimParsingSupportsScalarArrayAndExplicitCaseFolding(t *testing.T) {
	tests := []struct {
		name     string
		claim    any
		caseFold bool
		expected []string
	}{
		{name: "scalar preserves case", claim: " Engineering ", expected: []string{"Engineering"}},
		{name: "array deduplicates", claim: []any{"Ops", "Engineering", "Ops"}, expected: []string{"Engineering", "Ops"}},
		{name: "case insensitive", claim: []any{"Ops", "ops"}, caseFold: true, expected: []string{"ops"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration := OIDCGroupClaimConfiguration{Path: "realm.groups", CaseInsensitive: tt.caseFold}
			claim, err := ParseOIDCGroupClaim(map[string]any{
				"realm": map[string]any{"groups": tt.claim}, "email": "not-selected@example.test",
			}, configuration)
			require.NoError(t, err)
			assert.True(t, claim.Trusted)
			assert.True(t, claim.Present)
			assert.Equal(t, tt.expected, claim.Values)
			assert.NotContains(t, claim.Revision, "not-selected")
		})
	}
}

func TestOIDCGroupClaimParsingRejectsInvalidPathsShapesAndBounds(t *testing.T) {
	tooMany := make([]any, OIDCGroupClaimMaxValues+1)
	for index := range tooMany {
		tooMany[index] = "group"
	}
	tests := []struct {
		name          string
		configuration OIDCGroupClaimConfiguration
		claims        map[string]any
	}{
		{name: "empty path", claims: map[string]any{"groups": "x"}},
		{name: "path traversal", configuration: OIDCGroupClaimConfiguration{Path: "realm..groups"}, claims: map[string]any{}},
		{name: "too deep", configuration: OIDCGroupClaimConfiguration{Path: "a.b.c.d.e.f.g.h.i"}, claims: map[string]any{}},
		{name: "object shape", configuration: OIDCGroupClaimConfiguration{Path: "groups"}, claims: map[string]any{"groups": map[string]any{"name": "x"}}},
		{name: "mixed array", configuration: OIDCGroupClaimConfiguration{Path: "groups"}, claims: map[string]any{"groups": []any{"x", 1}}},
		{name: "too many", configuration: OIDCGroupClaimConfiguration{Path: "groups"}, claims: map[string]any{"groups": tooMany}},
		{name: "too long", configuration: OIDCGroupClaimConfiguration{Path: "groups"}, claims: map[string]any{"groups": strings.Repeat("x", OIDCGroupClaimMaxValueLength+1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseOIDCGroupClaim(tt.claims, tt.configuration)
			assert.Error(t, err)
		})
	}
}

func TestOIDCGroupMissingClaimPolicyPreservesOrClears(t *testing.T) {
	preserve, err := ParseOIDCGroupClaim(map[string]any{}, OIDCGroupClaimConfiguration{
		Path: "groups", MissingClaimPolicy: OIDCMissingClaimPreserve,
	})
	require.NoError(t, err)
	assert.False(t, preserve.Trusted)
	assert.False(t, preserve.Present)

	clear, err := ParseOIDCGroupClaim(map[string]any{}, OIDCGroupClaimConfiguration{
		Path: "groups", MissingClaimPolicy: OIDCMissingClaimClear,
	})
	require.NoError(t, err)
	assert.True(t, clear.Trusted)
	assert.False(t, clear.Present)
	assert.Empty(t, clear.Values)
	assert.NotEqual(t, preserve.Revision, clear.Revision)
}

func TestOIDCGroupPreviewDiffIsDeterministicAndPreservesForeignOwnership(t *testing.T) {
	configuration := OIDCGroupClaimConfiguration{Path: "groups"}
	claim, err := ParseOIDCGroupClaim(map[string]any{"groups": []any{"unknown", "engineering"}}, configuration)
	require.NoError(t, err)
	input := OIDCGroupPreviewInput{
		ProviderID: "corp", MappingRevision: 4, CapturedAt: time.Unix(1_800_000_100, 0).UTC(),
		Configuration: configuration, Claim: claim, UserID: 7,
		Mappings: []OIDCGroupMapping{
			{ID: "project", ProviderID: "corp", ClaimValue: "engineering", Enabled: true,
				Target: OIDCRoleTarget{Scope: OIDCRoleScopeProject, ProjectID: 3, RoleID: "runner"}},
			{ID: "global", ProviderID: "corp", ClaimValue: "auditors", Enabled: true,
				Target: OIDCRoleTarget{Scope: OIDCRoleScopeGlobal, RoleID: "auditor"}},
		},
		ExistingAssignments: []OIDCRoleAssignment{
			{UserID: 7, Target: OIDCRoleTarget{Scope: OIDCRoleScopeGlobal, RoleID: "auditor"},
				OwnerKind: "oidc", ManagedByProviderID: "corp", ManagedByMappingID: "global"},
			{UserID: 7, Target: OIDCRoleTarget{Scope: OIDCRoleScopeProject, ProjectID: 9, RoleID: "manager"},
				OwnerKind: "manual"},
			{UserID: 7, Target: OIDCRoleTarget{Scope: OIDCRoleScopeProject, ProjectID: 10, RoleID: "guest"},
				OwnerKind: "ldap", ManagedByProviderID: "ldap-corp", ManagedByMappingID: "ldap-guest"},
		},
	}
	first, err := ComputeOIDCGroupPreview(input)
	require.NoError(t, err)
	second, err := ComputeOIDCGroupPreview(input)
	require.NoError(t, err)
	assert.Equal(t, first.Token, second.Token)
	require.Len(t, first.Additions, 1)
	require.Len(t, first.Removals, 1)
	assert.Equal(t, "global", first.Removals[0].MappingID)
	assert.Equal(t, []string{"unknown"}, first.UnknownValues)
	for _, removal := range first.Removals {
		assert.NotEqual(t, 9, removal.Target.ProjectID)
		assert.NotEqual(t, 10, removal.Target.ProjectID)
	}
}

func TestOIDCGroupPreviewRejectsProjectCollisionsAndProtectsLastAdministrator(t *testing.T) {
	configuration := OIDCGroupClaimConfiguration{Path: "groups"}
	claim, err := ParseOIDCGroupClaim(map[string]any{"groups": []any{"one", "two"}}, configuration)
	require.NoError(t, err)
	preview, err := ComputeOIDCGroupPreview(OIDCGroupPreviewInput{
		ProviderID: "corp", MappingRevision: 2, CapturedAt: time.Unix(1_800_000_200, 0).UTC(),
		Configuration: configuration, Claim: claim, UserID: 7,
		Mappings: []OIDCGroupMapping{
			{ID: "one", ProviderID: "corp", ClaimValue: "one", Enabled: true,
				Target: OIDCRoleTarget{Scope: OIDCRoleScopeProject, ProjectID: 3, RoleID: "runner"}},
			{ID: "two", ProviderID: "corp", ClaimValue: "two", Enabled: true,
				Target: OIDCRoleTarget{Scope: OIDCRoleScopeProject, ProjectID: 3, RoleID: "manager"}},
		},
		ExistingAssignments: []OIDCRoleAssignment{{
			UserID: 7, Target: OIDCRoleTarget{Scope: OIDCRoleScopeGlobal, RoleID: "administrator"},
			OwnerKind: "oidc", ManagedByProviderID: "corp", ManagedByMappingID: "old-admin",
			ProtectedAdministrator: true,
		}},
	})
	require.NoError(t, err)
	assert.Len(t, preview.Collisions, 2)
	assert.Empty(t, preview.Additions)
	assert.Empty(t, preview.Removals)
	require.Len(t, preview.ProtectedAdminViolations, 1)
	assert.Equal(t, "last_administrator", preview.ProtectedAdminViolations[0].Reason)
}

func TestOIDCGroupPreviewPreservesOtherProviderManagedAssignments(t *testing.T) {
	configuration := OIDCGroupClaimConfiguration{Path: "groups"}
	claim, err := ParseOIDCGroupClaim(map[string]any{"groups": []any{}}, configuration)
	require.NoError(t, err)

	preview, err := ComputeOIDCGroupPreview(OIDCGroupPreviewInput{
		ProviderID: "provider-a", MappingRevision: 1, CapturedAt: time.Unix(1_800_000_000, 0).UTC(),
		Configuration: configuration, Claim: claim, UserID: 7,
		Mappings: []OIDCGroupMapping{{
			ID: "shared-mapping", ProviderID: "provider-a", ClaimValue: "engineering",
			Target: OIDCRoleTarget{Scope: OIDCRoleScopeGlobal, RoleID: "auditor"}, Enabled: true,
		}},
		ExistingAssignments: []OIDCRoleAssignment{{
			UserID: 7, Target: OIDCRoleTarget{Scope: OIDCRoleScopeGlobal, RoleID: "auditor"},
			OwnerKind: "oidc", ManagedByProviderID: "provider-b", ManagedByMappingID: "shared-mapping",
		}},
	})

	require.NoError(t, err)
	assert.Empty(t, preview.Removals)
	assert.Empty(t, preview.Additions)
}
