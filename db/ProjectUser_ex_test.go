package db

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestBuiltInProjectRolePermissionsRemainDeterministic(t *testing.T) {
	expected := map[ProjectUserRole]ProjectUserPermission{
		ProjectOwner: CanRunProjectTasks | CanUpdateProject | AllProjectResourcePermissions |
			CanManageProjectUsers | CanViewWorkflows |
			CanEditWorkflows | CanStartWorkflows | CanStopWorkflows | CanAdministerWorkflows |
			CanListGrantedCredentials | CanConsumeGrantedCredentials | CanOverrideDeploymentWindow | CanManagePolicyGuardrails | CanRollbackPolicyGuardrails |
			CanReadTaskGroups | CanCreateTaskGroups | CanUpdateTaskGroups | CanDeleteTaskGroups | CanShareTaskGroups | CanReadProject | CanApproveWorkflows,
		ProjectManager: CanRunProjectTasks | AllProjectResourcePermissions |
			CanViewWorkflows | CanEditWorkflows | CanStartWorkflows | CanStopWorkflows |
			CanAdministerWorkflows | CanListGrantedCredentials | CanConsumeGrantedCredentials |
			CanOverrideDeploymentWindow | CanManagePolicyGuardrails | CanRollbackPolicyGuardrails |
			CanReadTaskGroups | CanCreateTaskGroups | CanUpdateTaskGroups | CanDeleteTaskGroups | CanShareTaskGroups | CanReadProject | CanApproveWorkflows,
		ProjectTaskRunner: CanRunProjectTasks | AllProjectResourceViewPermissions |
			CanViewWorkflows | CanStartWorkflows | CanStopWorkflows |
			CanListGrantedCredentials | CanConsumeGrantedCredentials | CanReadTaskGroups | CanReadProject | CanApproveWorkflows,
		ProjectGuest: AllProjectResourceViewPermissions | CanViewWorkflows | CanReadTaskGroups | CanReadProject,
	}

	permissions := BuiltInProjectRolePermissions()
	assert.Equal(t, expected, permissions)
	for role, permission := range expected {
		assert.Equal(t, permission, role.GetPermissions())
		assert.True(t, role.Can(CanViewTemplates))
	}

	permissions[ProjectGuest] = 0
	assert.Equal(t, AllProjectResourceViewPermissions|CanViewWorkflows|CanReadTaskGroups|CanReadProject, ProjectGuest.GetPermissions())
	assert.False(t, ProjectGuest.Can(CanApproveWorkflows))
	assert.True(t, ProjectOwner.Can(CanManagePolicyGuardrails))
	assert.True(t, ProjectOwner.Can(CanRollbackPolicyGuardrails))
	assert.True(t, ProjectManager.Can(CanManagePolicyGuardrails))
	assert.True(t, ProjectManager.Can(CanRollbackPolicyGuardrails))
	assert.False(t, ProjectTaskRunner.Can(CanManagePolicyGuardrails))
	assert.False(t, ProjectTaskRunner.Can(CanRollbackPolicyGuardrails))
}

func TestNormalizeProjectResourcePermissionsKeepsLegacySemanticsFailClosed(t *testing.T) {
	tests := []struct {
		name       string
		legacy     ProjectUserPermission
		expected   ProjectUserPermission
		coarseWant ProjectUserPermission
	}{
		{"view", CanViewProjectResources, AllProjectResourceViewPermissions, CanViewProjectResources},
		{"manage", CanManageProjectResources, AllProjectResourceMutationPermissions, CanManageProjectResources},
		{"both", CanViewProjectResources | CanManageProjectResources, AllProjectResourcePermissions, CanViewProjectResources | CanManageProjectResources},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := NormalizeProjectResourcePermissions(tt.legacy)
			assert.Equal(t, tt.expected, normalized)
			assert.Zero(t, normalized&(CanViewProjectResources|CanManageProjectResources))
			assert.True(t, normalized.Can(tt.coarseWant))
		})
	}

	assert.False(t, (AllProjectResourceViewPermissions &^ CanViewTemplates).Can(CanViewProjectResources))
	assert.False(t, (AllProjectResourceMutationPermissions &^ CanDeleteViews).Can(CanManageProjectResources))
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
