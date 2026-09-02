package pro_interfaces

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectPermissionCatalogIsStableTypedAndIsolated(t *testing.T) {
	catalog := ProjectPermissionCatalog()
	require.Len(t, catalog, 12)
	assert.Equal(t, []PermissionID{
		PermissionRunProjectTasks,
		PermissionUpdateProject,
		PermissionViewProjectResources,
		PermissionManageProjectResources,
		PermissionManageProjectUsers,
		PermissionViewWorkflow,
		PermissionEditWorkflow,
		PermissionStartWorkflow,
		PermissionStopWorkflow,
		PermissionAdministerWorkflow,
		PermissionListGrantedCredentials,
		PermissionConsumeGrantedCredentials,
	}, []PermissionID{
		catalog[0].ID, catalog[1].ID, catalog[2].ID, catalog[3].ID, catalog[4].ID,
		catalog[5].ID, catalog[6].ID, catalog[7].ID, catalog[8].ID, catalog[9].ID,
		catalog[10].ID, catalog[11].ID,
	})

	seenPermissions := make(map[db.ProjectUserPermission]struct{}, len(catalog))
	for _, definition := range catalog {
		assert.NotEmpty(t, definition.Description)
		assert.Equal(t, PermissionScopeProject, definition.Scope)
		assert.NotNil(t, definition.CapabilityPrerequisites)
		_, duplicate := seenPermissions[definition.Permission]
		assert.False(t, duplicate)
		seenPermissions[definition.Permission] = struct{}{}
	}

	catalog[0].CapabilityPrerequisites = append(
		catalog[0].CapabilityPrerequisites,
		CapabilityProjectRoles,
	)
	assert.Empty(t, ProjectPermissionCatalog()[0].CapabilityPrerequisites)
}

func TestPermissionCatalogUsesExplicitGlobalAndTemplateScopes(t *testing.T) {
	assert.Equal(t, []PermissionID{
		PermissionManageGlobalUsers,
		PermissionManageGlobalRoles,
		PermissionManageGlobalSystem,
		PermissionReadGlobalAudit,
		PermissionManageGlobalCredentialsMetadata,
		PermissionManageGlobalCredentialsRotate,
		PermissionManageGlobalCredentialsGrant,
	}, permissionIDs(GlobalPermissionCatalog()))
	assert.Equal(t, []PermissionID{
		PermissionReadTemplate,
		PermissionRunTemplate,
		PermissionEditTemplate,
		PermissionDeleteTemplate,
	}, permissionIDs(TemplatePermissionCatalog()))

	definition, ok := PermissionDefinitionByID(PermissionDeleteTemplate)
	require.True(t, ok)
	assert.Equal(t, PermissionScopeTemplate, definition.Scope)
	_, ok = PermissionDefinitionByID("template.unknown")
	assert.False(t, ok)
}

func TestEvaluatePermissionTemplateInheritanceOverrideAndConstraints(t *testing.T) {
	project := &PermissionGrant{
		Scope: PermissionScopeProject, RoleID: "runner", RoleName: "Runner",
		Permissions: []PermissionID{PermissionRunProjectTasks},
	}

	inherited := EvaluatePermission(PermissionEvaluationRequest{
		Permission: PermissionRunTemplate,
		Project:    project,
	})
	assert.True(t, inherited.Allowed)
	assert.Equal(t, PermissionScopeProject, inherited.Provenance.Scope)
	assert.Equal(t, PermissionEffectInherited, inherited.Provenance.Effect)
	assert.Equal(t, "runner", inherited.Provenance.RoleID)

	denied := EvaluatePermission(PermissionEvaluationRequest{
		Permission: PermissionRunTemplate,
		Project:    project,
		Template: &PermissionOverride{
			RoleID: "runner", RoleName: "Runner",
			Allow: []PermissionID{PermissionRunTemplate},
			Deny:  []PermissionID{PermissionRunTemplate},
		},
	})
	assert.False(t, denied.Allowed, "explicit deny wins over explicit allow and inheritance")
	assert.Equal(t, PermissionScopeTemplate, denied.Provenance.Scope)
	assert.Equal(t, PermissionEffectDeny, denied.Provenance.Effect)

	capabilityDenied := EvaluatePermission(PermissionEvaluationRequest{
		Permission: PermissionRunTemplate,
		Project:    project,
		Capability: &PermissionConstraint{ID: string(CapabilityProjectRoles), Allowed: false},
	})
	assert.False(t, capabilityDenied.Allowed)
	assert.Equal(t, PermissionScopeCapability, capabilityDenied.Provenance.Scope)
	assert.Equal(t, string(CapabilityProjectRoles), capabilityDenied.Provenance.ConstraintID)
}

func TestEvaluatePermissionRejectsCrossScopeGrant(t *testing.T) {
	decision := EvaluatePermission(PermissionEvaluationRequest{
		Permission: PermissionManageGlobalUsers,
		Project: &PermissionGrant{
			Scope: PermissionScopeProject, RoleID: "owner", RoleName: "Owner",
			Permissions: []PermissionID{PermissionManageProjectUsers},
		},
	})
	assert.False(t, decision.Allowed)
	assert.Equal(t, PermissionScopeGlobal, decision.Provenance.Scope)

	wrongScope := EvaluatePermission(PermissionEvaluationRequest{
		Permission: PermissionManageGlobalUsers,
		Global: &PermissionGrant{
			Scope: PermissionScopeProject, RoleID: "global", RoleName: "Global",
			Permissions: []PermissionID{PermissionManageGlobalUsers},
		},
	})
	assert.False(t, wrongScope.Allowed)
}

func TestExplainEffectiveGlobalPermissionsReturnsOnlyDecidingRoles(t *testing.T) {
	result := ExplainEffectiveGlobalPermissions(false, []db.GlobalRoleAssignment{
		{
			RoleID: "user_manager", RoleName: "User manager",
			GlobalPermissions: db.CanManageGlobalUsers,
		},
		{
			RoleID: "auditor", RoleName: "Auditor",
			GlobalPermissions: db.CanReadGlobalAudit,
		},
	})

	assert.Equal(t, db.CanManageGlobalUsers|db.CanReadGlobalAudit, result.Permissions)
	assert.Len(t, result.Decisions, 7)
	assert.Equal(t, "user_manager", result.Decisions[0].Provenance.RoleID)
	assert.Empty(t, result.Decisions[1].Provenance.RoleID)
	assert.Empty(t, result.Decisions[2].Provenance.RoleID)
	assert.Equal(t, "auditor", result.Decisions[3].Provenance.RoleID)

	breakGlass := ExplainEffectiveGlobalPermissions(true, nil)
	assert.Equal(t, db.AllGlobalPermissions, breakGlass.Permissions)
	for _, decision := range breakGlass.Decisions {
		assert.True(t, decision.Allowed)
		assert.Equal(t, "admin", decision.Provenance.RoleID)
	}
}

func TestEvaluateWorkflowAccessNarrowsViewAndStartFailClosed(t *testing.T) {
	known := db.KnownProjectRoleReferences(nil)
	policy := db.WorkflowAccessPolicy{
		ViewRoleIDs:  []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
		StartRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager},
	}

	viewer := EvaluateWorkflowAccess(WorkflowAccessRequest{
		Permission: PermissionViewWorkflow, ProjectPermissions: db.CanViewWorkflows,
		EffectiveRoleReferences: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceGuest},
		KnownRoleReferences:     known, Policy: policy,
	})
	assert.False(t, viewer.Allowed)
	assert.True(t, viewer.PolicyApplied)

	manager := EvaluateWorkflowAccess(WorkflowAccessRequest{
		Permission: PermissionStartWorkflow, ProjectPermissions: db.CanStartWorkflows,
		EffectiveRoleReferences: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager},
		KnownRoleReferences:     known, Policy: policy,
	})
	assert.True(t, manager.Allowed)

	deletedRolePolicy := db.WorkflowAccessPolicy{
		ViewRoleIDs: []db.ProjectRoleReference{db.ProjectRoleReferenceForCustomRole("role_deleted")},
	}
	assert.False(t, EvaluateWorkflowAccess(WorkflowAccessRequest{
		Permission: PermissionViewWorkflow, ProjectPermissions: db.CanViewWorkflows,
		EffectiveRoleReferences: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
		KnownRoleReferences:     known, Policy: deletedRolePolicy,
	}).Allowed)
}

func TestWorkflowApprovalEligibilityAndProgressEnforceSeparationAndDistinctRoles(t *testing.T) {
	known := db.KnownProjectRoleReferences(nil)
	policy := db.WorkflowApprovalRolePolicy{
		Mode: db.WorkflowApprovalRoleModeAllOf,
		RoleIDs: []db.ProjectRoleReference{
			db.BuiltinProjectRoleReferenceOwner,
			db.BuiltinProjectRoleReferenceManager,
		},
		MinimumDistinctApprovers: 2, InitiatorSeparation: true,
	}
	self := EvaluateWorkflowApprovalEligibility(WorkflowApprovalEligibilityRequest{
		Policy: policy, ActorUserID: 7, InitiatorUserID: 7,
		EffectiveRoleReferences: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
		KnownRoleReferences:     known,
	})
	assert.False(t, self.Allowed)

	other := EvaluateWorkflowApprovalEligibility(WorkflowApprovalEligibilityRequest{
		Policy: policy, ActorUserID: 8, InitiatorUserID: 7,
		EffectiveRoleReferences: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
		KnownRoleReferences:     known,
	})
	assert.True(t, other.Allowed)

	partial := EvaluateWorkflowApprovalProgress(WorkflowApprovalProgressRequest{
		Policy: policy, InitiatorUserID: 7, KnownRoleReferences: known,
		Contributions: []db.WorkflowApprovalContribution{{ActorUserID: 8, RoleID: db.BuiltinProjectRoleReferenceOwner}},
	})
	assert.False(t, partial.Satisfied)

	complete := EvaluateWorkflowApprovalProgress(WorkflowApprovalProgressRequest{
		Policy: policy, InitiatorUserID: 7, KnownRoleReferences: known,
		Contributions: []db.WorkflowApprovalContribution{
			{ActorUserID: 8, RoleID: db.BuiltinProjectRoleReferenceOwner},
			{ActorUserID: 9, RoleID: db.BuiltinProjectRoleReferenceManager},
		},
	})
	assert.True(t, complete.Satisfied)

	duplicateActor := EvaluateWorkflowApprovalProgress(WorkflowApprovalProgressRequest{
		Policy: policy, InitiatorUserID: 7, KnownRoleReferences: known,
		Contributions: []db.WorkflowApprovalContribution{
			{ActorUserID: 8, RoleID: db.BuiltinProjectRoleReferenceOwner},
			{ActorUserID: 8, RoleID: db.BuiltinProjectRoleReferenceManager},
		},
	})
	assert.False(t, duplicateActor.Satisfied)
}

func TestWorkflowApprovalAllOfAllowsAdditionalDistinctContributorsForOneRole(t *testing.T) {
	known := db.KnownProjectRoleReferences(nil)
	policy := db.WorkflowApprovalRolePolicy{
		Mode: db.WorkflowApprovalRoleModeAllOf,
		RoleIDs: []db.ProjectRoleReference{
			db.BuiltinProjectRoleReferenceOwner,
			db.BuiltinProjectRoleReferenceManager,
		},
		MinimumDistinctApprovers: 3,
	}

	progress := EvaluateWorkflowApprovalProgress(WorkflowApprovalProgressRequest{
		Policy: policy, KnownRoleReferences: known,
		Contributions: []db.WorkflowApprovalContribution{
			{ActorUserID: 1, RoleID: db.BuiltinProjectRoleReferenceOwner},
			{ActorUserID: 2, RoleID: db.BuiltinProjectRoleReferenceManager},
			{ActorUserID: 3, RoleID: db.BuiltinProjectRoleReferenceManager},
		},
	})
	assert.True(t, progress.Satisfied)
}

func permissionIDs(definitions []PermissionDefinition) []PermissionID {
	result := make([]PermissionID, len(definitions))
	for i, definition := range definitions {
		result[i] = definition.ID
	}
	return result
}
