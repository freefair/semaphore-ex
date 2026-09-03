package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateRole(t *testing.T) {
	projectID := 1

	tests := []struct {
		name    string
		role    Role
		wantErr bool
	}{
		{
			name:    "valid custom role",
			role:    Role{Slug: "deployer", Name: "Deployer", ProjectID: &projectID},
			wantErr: false,
		},
		{
			name:    "empty name",
			role:    Role{Slug: "deployer", Name: "", ProjectID: &projectID},
			wantErr: true,
		},
		{
			name:    "empty slug",
			role:    Role{Slug: "", Name: "Deployer", ProjectID: &projectID},
			wantErr: true,
		},
		{
			name:    "reserved slug owner",
			role:    Role{Slug: string(ProjectOwner), Name: "pwn", ProjectID: &projectID},
			wantErr: true,
		},
		{
			name:    "reserved slug manager",
			role:    Role{Slug: string(ProjectManager), Name: "pwn", ProjectID: &projectID},
			wantErr: true,
		},
		{
			name:    "reserved slug task_runner",
			role:    Role{Slug: string(ProjectTaskRunner), Name: "pwn", ProjectID: &projectID},
			wantErr: true,
		},
		{
			name:    "reserved slug guest",
			role:    Role{Slug: string(ProjectGuest), Name: "pwn", ProjectID: &projectID},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRole(tt.role)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateRole_ReservedSlugsMatchBuiltins guards against a built-in role
// being added later without also reserving its slug in ValidateRole.
func TestValidateRole_ReservedSlugsMatchBuiltins(t *testing.T) {
	for role := range rolePermissions {
		err := ValidateRole(Role{Slug: string(role), Name: "custom"})
		assert.Error(t, err, "built-in slug %q must be rejected by ValidateRole", role)
	}
}

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
