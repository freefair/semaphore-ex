package db

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestBuiltInProjectRolePermissionsRemainDeterministic(t *testing.T) {
	expected := map[ProjectUserRole]ProjectUserPermission{
		ProjectOwner: CanRunProjectTasks | CanUpdateProject | CanManageProjectResources |
			CanManageProjectUsers | CanViewProjectResources | CanViewWorkflows |
			CanEditWorkflows | CanStartWorkflows | CanStopWorkflows | CanAdministerWorkflows,
		ProjectManager: CanRunProjectTasks | CanManageProjectResources | CanViewProjectResources |
			CanViewWorkflows | CanEditWorkflows | CanStartWorkflows | CanStopWorkflows |
			CanAdministerWorkflows,
		ProjectTaskRunner: CanRunProjectTasks | CanViewProjectResources |
			CanViewWorkflows | CanStartWorkflows | CanStopWorkflows,
		ProjectGuest: CanViewProjectResources | CanViewWorkflows,
	}

	permissions := BuiltInProjectRolePermissions()
	assert.Equal(t, expected, permissions)
	for role, permission := range expected {
		assert.Equal(t, permission, role.GetPermissions())
		assert.True(t, role.Can(CanViewProjectResources))
	}

	permissions[ProjectGuest] = 0
	assert.Equal(t, CanViewProjectResources|CanViewWorkflows, ProjectGuest.GetPermissions())
}

func TestProjectRoleReferencesAreStableAndNeverUseLegacySlugs(t *testing.T) {
	owner, ok := ProjectRoleReferenceForBuiltInRole(ProjectOwner)
	assert.True(t, ok)
	assert.Equal(t, BuiltinProjectRoleReferenceOwner, owner)
	assert.True(t, IsValidProjectRoleReferenceSyntax(owner))

	custom := ProjectRoleReferenceForCustomRole("role_0123456789abcdef")
	assert.Equal(t, ProjectRoleReference("role:role_0123456789abcdef"), custom)
	assert.True(t, IsValidProjectRoleReferenceSyntax(custom))
	assert.False(t, IsValidProjectRoleReferenceSyntax(ProjectRoleReference("manager")))
	assert.False(t, IsValidProjectRoleReferenceSyntax(ProjectRoleReference("role:missing space")))

	known := KnownProjectRoleReferences([]ProjectRoleID{"role_0123456789abcdef"})
	assert.True(t, known[owner])
	assert.True(t, known[custom])
	assert.False(t, known[ProjectRoleReferenceForCustomRole("role_deleted")])
}

func TestProjectWorkflowRoleIdentityRejectsNonCanonicalDirectoryFingerprint(t *testing.T) {
	identity := ProjectWorkflowRoleIdentity{
		Reference:                    ProjectRoleReferenceForCustomRole("role_directory"),
		Permissions:                  CanViewWorkflows,
		Revision:                     1,
		Origin:                       ProjectWorkflowRoleOriginLDAP,
		DirectoryProviderID:          "corp",
		DirectoryMappingID:           "approvers",
		DirectoryMappingRevision:     1,
		DirectoryRevisionFingerprint: "F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3F0E1D2C3",
	}
	assert.ErrorIs(t, identity.Validate(), ErrProjectWorkflowRoleIdentityUnavailable)
}
