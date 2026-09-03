package db

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestValidateProjectRoleReferenceRejectsLegacyAndMalformedValues(t *testing.T) {
	assert.NoError(t, ValidateProjectRoleReference(BuiltinProjectRoleReferenceManager))
	assert.NoError(t, ValidateProjectRoleReference(ProjectRoleReferenceForCustomRole("role_1234")))
	assert.Error(t, ValidateProjectRoleReference("manager"))
	assert.Error(t, ValidateProjectRoleReference("role:"))
	assert.Error(t, ValidateProjectRoleReference("role:Uppercase"))
}

func TestValidateProjectRoleAcceptsWorkflowPermissions(t *testing.T) {
	projectID := 1
	assert.NoError(t, ValidateProjectRole(Role{
		ID: "role_workflow", Name: "Workflow operator", ProjectID: &projectID, Revision: 1,
		Permissions: CanViewWorkflows | CanEditWorkflows | CanStartWorkflows |
			CanStopWorkflows | CanAdministerWorkflows,
	}))
}

func TestPolicyGuardrailRoleBitsAreIndependentAndBounded(t *testing.T) {
	projectID := 1
	for _, permission := range []ProjectUserPermission{CanManagePolicyGuardrails, CanRollbackPolicyGuardrails} {
		assert.NoError(t, ValidateProjectRole(Role{ID: "role_policy", Name: "Policy", ProjectID: &projectID, Revision: 1, Permissions: permission}))
	}
	for _, permission := range []GlobalPermission{CanManageGlobalPolicyGuardrails, CanRollbackGlobalPolicyGuardrails} {
		assert.NoError(t, ValidateGlobalRole(Role{ID: "global_policy", Name: "Policy", Revision: 1, GlobalPermissions: permission}))
	}
	assert.Error(t, ValidateProjectRole(Role{ID: "role_unknown", Name: "Unknown", ProjectID: &projectID, Revision: 1, Permissions: 1 << 30}))
	assert.Error(t, ValidateGlobalRole(Role{ID: "global_unknown", Name: "Unknown", Revision: 1, GlobalPermissions: 1 << 30}))
}
