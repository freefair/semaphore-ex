package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveProjectWorkflowRoleIdentitiesReturnsEveryCurrentRole(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "multiple")
	manager, err := store.GetGlobalRoleBySlug("manager")
	require.NoError(t, err)
	runner, err := store.CreateProjectRole(db.Role{
		ID: "role_workflow_multi_runner", Name: "Runner", ProjectID: &project.ID,
		Permissions: db.CanStartWorkflows, Revision: 3,
	})
	require.NoError(t, err)
	createMembershipWithRoles(t, store, project.ID, user.ID, []db.Role{manager, runner})

	identities, err := store.ResolveProjectWorkflowRoleIdentities(project.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, identities, 2)
	assert.ElementsMatch(t, []db.ProjectRoleReference{
		db.BuiltinProjectRoleReferenceManager,
		db.ProjectRoleReferenceForCustomRole(runner.ID),
	}, []db.ProjectRoleReference{identities[0].Reference, identities[1].Reference})
	for _, identity := range identities {
		require.NoError(t, identity.Validate())
	}
}

func TestResolveProjectWorkflowRoleIdentitiesSkipsGlobalRolesButKeepsLocalRoles(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "global-and-local")
	global, err := store.CreateGlobalRole(db.Role{
		ID: "role_workflow_global_only", Name: "Global only", Revision: 1,
		GlobalPermissions: db.CanReadGlobalAudit,
	})
	require.NoError(t, err)
	local, err := store.CreateProjectRole(db.Role{
		ID: "role_workflow_local_after_global", Name: "Local", ProjectID: &project.ID,
		Permissions: db.CanViewWorkflows, Revision: 1,
	})
	require.NoError(t, err)
	createMembershipWithRoles(t, store, project.ID, user.ID, []db.Role{global, local})

	identities, err := store.ResolveProjectWorkflowRoleIdentities(project.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, identities, 1)
	assert.Equal(t, db.ProjectRoleReferenceForCustomRole(local.ID), identities[0].Reference)

	membership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(project.ID, user.ID, []db.ProjectRoleID{global.ID}, membership.Revision))
	_, err = store.ResolveProjectWorkflowRoleIdentities(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrProjectWorkflowRoleIdentityUnavailable)
}

func TestWorkflowApprovalEligibilityUsesEveryCurrentRoleAndRevocation(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "approval-multiple")
	manager, err := store.GetGlobalRoleBySlug("manager")
	require.NoError(t, err)
	approver, err := store.CreateProjectRole(db.Role{
		ID: "role_workflow_approval_multi", Name: "Approver", ProjectID: &project.ID,
		Permissions: db.CanViewWorkflows, Revision: 2,
	})
	require.NoError(t, err)
	createMembershipWithRoles(t, store, project.ID, user.ID, []db.Role{manager, approver})

	state, err := pro_interfaces.ResolveWorkflowAuthorizationState(store, project.ID, user.ID)
	require.NoError(t, err)
	policy := db.WorkflowApprovalRolePolicy{
		Revision: 1, Mode: db.WorkflowApprovalRoleModeAllOf, MinimumDistinctApprovers: 2,
		RoleIDs: []db.ProjectRoleReference{
			db.BuiltinProjectRoleReferenceManager,
			db.ProjectRoleReferenceForCustomRole(approver.ID),
		},
	}
	eligibility := pro_interfaces.EvaluateWorkflowApprovalEligibility(pro_interfaces.WorkflowApprovalEligibilityRequest{
		Policy: policy, ActorUserID: user.ID, EffectiveRoleReferences: state.RoleReferences(), KnownRoleReferences: state.KnownRoles,
	})
	require.True(t, eligibility.Allowed)
	assert.ElementsMatch(t, policy.RoleIDs, eligibility.MatchingRoleIDs)

	membership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(project.ID, user.ID, []db.ProjectRoleID{approver.ID}, membership.Revision))
	state, err = pro_interfaces.ResolveWorkflowAuthorizationState(store, project.ID, user.ID)
	require.NoError(t, err)
	eligibility = pro_interfaces.EvaluateWorkflowApprovalEligibility(pro_interfaces.WorkflowApprovalEligibilityRequest{
		Policy: policy, ActorUserID: user.ID, EffectiveRoleReferences: state.RoleReferences(), KnownRoleReferences: state.KnownRoles,
	})
	require.True(t, eligibility.Allowed)
	assert.Equal(t, []db.ProjectRoleReference{db.ProjectRoleReferenceForCustomRole(approver.ID)}, eligibility.MatchingRoleIDs)
}

func TestResolveProjectWorkflowRoleIdentitiesRejectsStaleAssignmentProvenance(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "stale-assignment")
	runner, err := store.CreateProjectRole(db.Role{
		ID: "role_workflow_stale_runner", Name: "Runner", ProjectID: &project.ID,
		Permissions: db.CanStartWorkflows, Revision: 1,
	})
	require.NoError(t, err)
	createMembershipWithRoles(t, store, project.ID, user.ID, []db.Role{runner})
	_, err = store.Sql().Exec(
		"update project__user_role set ldap_group_managed_assignment_id=? where project_id=? and user_id=? and role_id=?",
		999, project.ID, user.ID, runner.ID,
	)
	require.NoError(t, err)

	_, err = store.ResolveProjectWorkflowRoleIdentities(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrProjectWorkflowRoleIdentityUnavailable)
}

func TestResolveProjectWorkflowRoleIdentitiesKeepsManualAndLDAPAssignmentsSeparate(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_900_300_000, 0).UTC()
	project, user := workflowRoleIdentityProjectUser(t, store, "manual-and-ldap")
	role, err := store.CreateProjectRole(db.Role{
		ID: "role_workflow_ldap_multi", Name: "Directory approver", ProjectID: &project.ID,
		Permissions: db.CanStartWorkflows, Revision: 1,
	})
	require.NoError(t, err)
	setupLDAPWorkflowRoleIdentity(t, store, project, user, role, now)
	_, err = store.Sql().Exec(
		"insert into project__user_role (project_id, user_id, role_id) values (?, ?, ?)",
		project.ID, user.ID, db.BuiltinProjectRoleReferenceManager,
	)
	require.NoError(t, err)

	identities, err := store.ResolveProjectWorkflowRoleIdentities(project.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, identities, 2)
	assert.ElementsMatch(t, []db.ProjectRoleReference{
		db.BuiltinProjectRoleReferenceManager,
		db.ProjectRoleReferenceForCustomRole(role.ID),
	}, []db.ProjectRoleReference{identities[0].Reference, identities[1].Reference})
	for _, identity := range identities {
		if identity.Reference == db.ProjectRoleReferenceForCustomRole(role.ID) {
			assert.Equal(t, db.ProjectWorkflowRoleOriginLDAP, identity.Origin)
		}
	}

	mappingRevision, err := store.GetLDAPGroupMappingRevision("corp")
	require.NoError(t, err)
	removal := db.LDAPGroupReconciliation{
		ProviderID: "corp", Source: "manual", Status: "preview", Token: "manual-and-ldap-removal",
		MappingRevision: mappingRevision, DirectoryRevision: "directory-revision", PreviewJSON: "{}", Created: now.Add(time.Minute),
	}
	_, err = store.SaveLDAPGroupReconciliation(removal)
	require.NoError(t, err)
	removal.Status = "applied"
	appliedAt := now.Add(2 * time.Minute)
	removal.AppliedAt = &appliedAt
	_, err = store.ApplyLDAPGroupPreview(removal, nil, []db.LDAPGroupAssignmentChange{{
		MappingID: "workflow-approvers", UserID: user.ID, TargetScope: "project", ProjectID: &project.ID, RoleID: string(role.ID),
	}})
	require.NoError(t, err)
	identities, err = store.ResolveProjectWorkflowRoleIdentities(project.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, identities, 1)
	assert.Equal(t, db.BuiltinProjectRoleReferenceManager, identities[0].Reference)
	projection, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectManager, projection.Role)
	assert.Nil(t, projection.RoleID)
	assert.Nil(t, projection.LDAPGroupManagedAssignmentID)
}
