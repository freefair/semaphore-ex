package pro_interfaces

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

func TestWorkflowStateHelpersUseRoleUnionAndRejectRevokedOrGlobalRoles(t *testing.T) {
	local := db.ProjectRoleReferenceForCustomRole("role_workflow_state_local")
	known := db.KnownProjectRoleReferences([]db.ProjectRoleID{"role_workflow_state_local"})
	state := WorkflowAuthorizationState{
		Identities: []db.ProjectWorkflowRoleIdentity{
			{Reference: db.BuiltinProjectRoleReferenceManager, Permissions: db.CanViewWorkflows, Revision: 1, Origin: db.ProjectWorkflowRoleOriginBuiltIn},
			{Reference: local, Permissions: db.CanViewWorkflows, Revision: 2, Origin: db.ProjectWorkflowRoleOriginManual},
		},
		ProjectPermissions: db.CanViewWorkflows,
		KnownRoles:         known,
	}
	localWorkflow := db.WorkflowTemplate{AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{local}}}
	managerWorkflow := db.WorkflowTemplate{AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager}}}

	assert.True(t, AuthorizeWorkflowReadForState(localWorkflow, state).Allowed)
	visible := FilterWorkflowTemplatesByAuthorizationState([]db.WorkflowTemplate{localWorkflow, managerWorkflow}, state)
	assert.Len(t, visible, 2)

	// A revoked local role and a globally-defined role are absent from current
	// workflow identities, so neither can satisfy a project workflow policy.
	revoked := state
	revoked.Identities = []db.ProjectWorkflowRoleIdentity{state.Identities[0]}
	assert.False(t, AuthorizeWorkflowReadForState(localWorkflow, revoked).Allowed)
	assert.Len(t, FilterWorkflowTemplatesByAuthorizationState([]db.WorkflowTemplate{localWorkflow, managerWorkflow}, revoked), 1)

	artifactPolicy := db.WorkflowFileArtifactAccessPolicy{Revision: 1, RoleIDs: []db.ProjectRoleReference{local}}
	assert.True(t, AuthorizeWorkflowFileArtifactAccessForState(artifactPolicy, state))
	assert.False(t, AuthorizeWorkflowFileArtifactAccessForState(artifactPolicy, revoked))
	invalidCurrent := state
	invalidCurrent.Identities = append([]db.ProjectWorkflowRoleIdentity{
		{Reference: db.ProjectRoleReferenceForCustomRole("role_removed"), Permissions: db.CanViewWorkflows, Revision: 1, Origin: db.ProjectWorkflowRoleOriginManual},
	}, state.Identities...)
	assert.False(t, AuthorizeWorkflowFileArtifactAccessForState(artifactPolicy, invalidCurrent), "an unknown current ref cannot be bypassed by a later matching role")
	assert.False(t, AuthorizeWorkflowFileArtifactAccessForState(db.WorkflowFileArtifactAccessPolicy{
		Revision: 1, RoleIDs: []db.ProjectRoleReference{db.ProjectRoleReferenceForCustomRole("role_global_only")},
	}, state))
}
