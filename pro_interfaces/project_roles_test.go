package pro_interfaces

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectPermissionCatalogIsStableTypedAndIsolated(t *testing.T) {
	catalog := ProjectPermissionCatalog()
	require.Len(t, catalog, 5)
	assert.Equal(t, []PermissionID{
		PermissionRunProjectTasks,
		PermissionUpdateProject,
		PermissionViewProjectResources,
		PermissionManageProjectResources,
		PermissionManageProjectUsers,
	}, []PermissionID{
		catalog[0].ID, catalog[1].ID, catalog[2].ID, catalog[3].ID, catalog[4].ID,
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
	assert.Len(t, result.Decisions, 4)
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

func permissionIDs(definitions []PermissionDefinition) []PermissionID {
	result := make([]PermissionID, len(definitions))
	for i, definition := range definitions {
		result[i] = definition.ID
	}
	return result
}
