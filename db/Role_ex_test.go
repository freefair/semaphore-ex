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
