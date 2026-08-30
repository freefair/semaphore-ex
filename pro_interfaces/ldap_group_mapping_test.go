package pro_interfaces

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeLDAPGroupPreviewPreservesManualAssignmentsAndProtectsAdmin(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	target := LDAPRoleTarget{Scope: LDAPRoleScopeGlobal, RoleID: "administrators"}
	mapping := LDAPGroupMapping{
		ID: "admins", ProviderID: "corp",
		GroupExternalID: "entryUUID:40F1C82A-B773-4D41-A587-7C4CF7F3CD67",
		Target:          target, Enabled: true, Revision: 1,
	}
	require.NoError(t, NormalizeLDAPGroupMapping(&mapping))
	assert.Equal(t, "entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67", mapping.GroupExternalID)

	preview, err := ComputeLDAPGroupPreview(LDAPGroupPreviewInput{
		ProviderID: "corp", MappingRevision: 1, DirectoryRevision: "one", CapturedAt: now,
		Mappings:          []LDAPGroupMapping{mapping},
		DirectoryGroupIDs: []string{mapping.GroupExternalID},
		ExistingAssignments: []LDAPRoleAssignment{
			{UserID: 1, Target: target, ManagedByMappingID: mapping.ID, ProtectedAdministrator: true},
			{UserID: 2, Target: target},
		},
		DirectoryUsers: []LDAPDirectoryUser{{ExternalID: "entryuuid:user-2", GroupExternalIDs: []string{mapping.GroupExternalID}}},
		LinkedUsers:    []LDAPLinkedUser{{ExternalID: "entryuuid:user-2", UserID: 2}},
	})
	require.NoError(t, err)
	assert.Empty(t, preview.Additions)
	assert.Empty(t, preview.Removals)
	require.Len(t, preview.Collisions, 1)
	assert.Equal(t, "manual_assignment_preserved", preview.Collisions[0].Reason)
	require.Len(t, preview.ProtectedAdminViolations, 1)
	assert.True(t, preview.IsFresh(1, "one"))
	assert.False(t, preview.IsFresh(2, "one"))
}

func TestComputeLDAPGroupPreviewRejectsMultipleProjectRoles(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	groupID := "entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67"
	preview, err := ComputeLDAPGroupPreview(LDAPGroupPreviewInput{
		ProviderID: "corp", MappingRevision: 2, DirectoryRevision: "two", CapturedAt: now,
		Mappings: []LDAPGroupMapping{
			{ID: "runner", ProviderID: "corp", GroupExternalID: groupID, Enabled: true, Revision: 1,
				Target: LDAPRoleTarget{Scope: LDAPRoleScopeProject, ProjectID: 7, RoleID: "runner"}},
			{ID: "manager", ProviderID: "corp", GroupExternalID: groupID, Enabled: true, Revision: 1,
				Target: LDAPRoleTarget{Scope: LDAPRoleScopeProject, ProjectID: 7, RoleID: "manager"}},
		},
		DirectoryGroupIDs: []string{groupID},
		DirectoryUsers:    []LDAPDirectoryUser{{ExternalID: "entryuuid:user", GroupExternalIDs: []string{groupID}}},
		LinkedUsers:       []LDAPLinkedUser{{ExternalID: "entryuuid:user", UserID: 9}},
	})
	require.NoError(t, err)
	assert.Empty(t, preview.Additions)
	assert.Len(t, preview.Collisions, 2)
}

func TestComputeLDAPGroupPreviewPreservesExistingProjectMembership(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	groupID := "entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67"
	preview, err := ComputeLDAPGroupPreview(LDAPGroupPreviewInput{
		ProviderID: "corp", MappingRevision: 3, DirectoryRevision: "three", CapturedAt: now,
		Mappings: []LDAPGroupMapping{{
			ID: "runner", ProviderID: "corp", GroupExternalID: groupID, Enabled: true, Revision: 1,
			Target: LDAPRoleTarget{Scope: LDAPRoleScopeProject, ProjectID: 7, RoleID: "runner"},
		}},
		DirectoryUsers: []LDAPDirectoryUser{{ExternalID: "entryuuid:user", GroupExternalIDs: []string{groupID}}},
		LinkedUsers: []LDAPLinkedUser{{ExternalID: "entryuuid:user", UserID: 9}},
		ExistingAssignments: []LDAPRoleAssignment{{
			UserID: 9, Target: LDAPRoleTarget{Scope: LDAPRoleScopeProject, ProjectID: 7, RoleID: "owner"},
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, preview.Additions)
	require.Len(t, preview.Collisions, 1)
	assert.Equal(t, "existing_project_membership_preserved", preview.Collisions[0].Reason)
}
