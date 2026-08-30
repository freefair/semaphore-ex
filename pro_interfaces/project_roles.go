package pro_interfaces

import "github.com/semaphoreui/semaphore/db"

// PermissionID is a stable API identifier for one assignable permission.
type PermissionID string

const (
	PermissionRunProjectTasks        PermissionID = "project.tasks.run"
	PermissionUpdateProject          PermissionID = "project.settings.update"
	PermissionViewProjectResources   PermissionID = "project.resources.view"
	PermissionManageProjectResources PermissionID = "project.resources.manage"
	PermissionManageProjectUsers     PermissionID = "project.members.manage"
	PermissionManageGlobalUsers      PermissionID = "global.users.manage"
	PermissionManageGlobalRoles      PermissionID = "global.roles.manage"
	PermissionManageGlobalSystem     PermissionID = "global.system.manage"
	PermissionReadGlobalAudit        PermissionID = "global.audit.read"
	PermissionReadTemplate           PermissionID = "template.read"
	PermissionRunTemplate            PermissionID = "template.run"
	PermissionEditTemplate           PermissionID = "template.edit"
	PermissionDeleteTemplate         PermissionID = "template.delete"
)

type PermissionScope string

const (
	PermissionScopeGlobal     PermissionScope = "global"
	PermissionScopeProject    PermissionScope = "project"
	PermissionScopeTemplate   PermissionScope = "template"
	PermissionScopeOwnership  PermissionScope = "ownership"
	PermissionScopeCapability PermissionScope = "capability"
)

// PermissionDefinition is the backend-authoritative catalog entry presented
// to API and UI clients. Capability prerequisites are typed even when a core
// permission has none, so later Enhanced permissions cannot rely on UI-only
// availability checks.
type PermissionDefinition struct {
	ID                      PermissionID             `json:"id"`
	Description             string                   `json:"description"`
	Scope                   PermissionScope          `json:"scope"`
	Permission              db.ProjectUserPermission `json:"permission"`
	CapabilityPrerequisites []CapabilityID           `json:"capability_prerequisites"`
}

// ProjectPermissionCatalog returns an isolated, stable-order catalog.
func ProjectPermissionCatalog() []PermissionDefinition {
	return permissionDefinitions(PermissionScopeProject)
}

// GlobalPermissionCatalog returns global permissions in stable API order.
func GlobalPermissionCatalog() []PermissionDefinition {
	return permissionDefinitions(PermissionScopeGlobal)
}

// TemplatePermissionCatalog returns template permissions in stable API order.
func TemplatePermissionCatalog() []PermissionDefinition {
	return permissionDefinitions(PermissionScopeTemplate)
}

// PermissionCatalog returns the complete backend-authoritative catalog.
func PermissionCatalog() []PermissionDefinition {
	definitions := permissionCatalog()
	return clonePermissionDefinitions(definitions)
}

// PermissionDefinitionByID resolves an exact typed identifier. Permission IDs
// are never interpreted by string prefix.
func PermissionDefinitionByID(id PermissionID) (PermissionDefinition, bool) {
	for _, definition := range permissionCatalog() {
		if definition.ID == id {
			definition.CapabilityPrerequisites = cloneCapabilityIDs(
				definition.CapabilityPrerequisites,
			)
			return definition, true
		}
	}
	return PermissionDefinition{}, false
}

func permissionDefinitions(scope PermissionScope) []PermissionDefinition {
	definitions := make([]PermissionDefinition, 0)
	for _, definition := range permissionCatalog() {
		if definition.Scope == scope {
			definitions = append(definitions, definition)
		}
	}
	return clonePermissionDefinitions(definitions)
}

func clonePermissionDefinitions(definitions []PermissionDefinition) []PermissionDefinition {
	result := make([]PermissionDefinition, len(definitions))
	for i, definition := range definitions {
		result[i] = definition
		result[i].CapabilityPrerequisites = cloneCapabilityIDs(
			definition.CapabilityPrerequisites,
		)
	}
	return result
}

func cloneCapabilityIDs(ids []CapabilityID) []CapabilityID {
	result := make([]CapabilityID, len(ids))
	copy(result, ids)
	return result
}

func permissionCatalog() []PermissionDefinition {
	return []PermissionDefinition{
		{
			ID: PermissionRunProjectTasks, Description: "Run project tasks",
			Scope: PermissionScopeProject, Permission: db.CanRunProjectTasks,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID: PermissionUpdateProject, Description: "Update project settings",
			Scope: PermissionScopeProject, Permission: db.CanUpdateProject,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID: PermissionViewProjectResources, Description: "View project resources",
			Scope: PermissionScopeProject, Permission: db.CanViewProjectResources,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID:          PermissionManageProjectResources,
			Description: "Create, update, and delete project resources",
			Scope:       PermissionScopeProject, Permission: db.CanManageProjectResources,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID: PermissionManageProjectUsers, Description: "Manage project members and roles",
			Scope: PermissionScopeProject, Permission: db.CanManageProjectUsers,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID: PermissionManageGlobalUsers, Description: "Manage global users",
			Scope: PermissionScopeGlobal, Permission: 1,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
		{
			ID: PermissionManageGlobalRoles, Description: "Manage global roles",
			Scope: PermissionScopeGlobal, Permission: 2,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
		{
			ID: PermissionManageGlobalSystem, Description: "Manage global system settings",
			Scope: PermissionScopeGlobal, Permission: 4,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
		{
			ID: PermissionReadGlobalAudit, Description: "Read the global audit log",
			Scope: PermissionScopeGlobal, Permission: 8,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
		{
			ID: PermissionReadTemplate, Description: "Read this template",
			Scope: PermissionScopeTemplate, Permission: 1,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
		{
			ID: PermissionRunTemplate, Description: "Run this template",
			Scope: PermissionScopeTemplate, Permission: 2,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
		{
			ID: PermissionEditTemplate, Description: "Edit this template",
			Scope: PermissionScopeTemplate, Permission: 4,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
		{
			ID: PermissionDeleteTemplate, Description: "Delete this template",
			Scope: PermissionScopeTemplate, Permission: 8,
			CapabilityPrerequisites: []CapabilityID{CapabilityProjectRoles},
		},
	}
}

// PermissionEffect describes why one layer decided an action.
type PermissionEffect string

const (
	PermissionEffectAllow     PermissionEffect = "allow"
	PermissionEffectDeny      PermissionEffect = "deny"
	PermissionEffectInherited PermissionEffect = "inherited"
)

// PermissionGrant is one role's grant at a concrete scope.
type PermissionGrant struct {
	Scope       PermissionScope `json:"scope"`
	RoleID      string          `json:"role_id"`
	RoleName    string          `json:"role_name"`
	Permissions []PermissionID  `json:"permissions"`
}

// PermissionOverride is an explicit template-scoped allow/deny for one role.
type PermissionOverride struct {
	RoleID   string         `json:"role_id"`
	RoleName string         `json:"role_name"`
	Allow    []PermissionID `json:"allow"`
	Deny     []PermissionID `json:"deny"`
}

// PermissionConstraint represents a later ownership or capability constraint.
type PermissionConstraint struct {
	ID      string `json:"id"`
	Allowed bool   `json:"allowed"`
}

// PermissionEvaluationRequest contains already-resolved role inputs. Keeping
// persistence out of the evaluator makes its order deterministic and testable.
type PermissionEvaluationRequest struct {
	Permission PermissionID          `json:"permission"`
	Global     *PermissionGrant      `json:"global,omitempty"`
	Project    *PermissionGrant      `json:"project,omitempty"`
	Template   *PermissionOverride   `json:"template,omitempty"`
	Ownership  *PermissionConstraint `json:"ownership,omitempty"`
	Capability *PermissionConstraint `json:"capability,omitempty"`
}

// PermissionProvenance exposes only the layer and role that decided the action.
type PermissionProvenance struct {
	Scope        PermissionScope  `json:"scope"`
	RoleID       string           `json:"role_id,omitempty"`
	RoleName     string           `json:"role_name,omitempty"`
	Effect       PermissionEffect `json:"effect"`
	ConstraintID string           `json:"constraint_id,omitempty"`
}

// EffectivePermissionDecision is the bounded result returned to API/UI callers.
type EffectivePermissionDecision struct {
	Permission PermissionID         `json:"permission"`
	Allowed    bool                 `json:"allowed"`
	Provenance PermissionProvenance `json:"provenance"`
}

// EffectiveGlobalPermissions is the bounded global authorization view exposed
// to the administration UI. Each decision contains at most the role that made
// that permission effective; unrelated assignments are not included.
type EffectiveGlobalPermissions struct {
	Permissions db.GlobalPermission           `json:"permissions"`
	Decisions   []EffectivePermissionDecision `json:"decisions"`
}

// EffectiveTemplatePermissions is the current user's bounded authorization
// view for one template.
type EffectiveTemplatePermissions struct {
	Permissions db.TemplatePermission         `json:"permissions"`
	Decisions   []EffectivePermissionDecision `json:"decisions"`
}

// ExplainEffectiveGlobalPermissions produces the effective mask and bounded
// provenance for one user. Assignment ordering is preserved so callers can
// choose a stable repository order without exposing every contributing role.
func ExplainEffectiveGlobalPermissions(
	isBuiltInAdministrator bool,
	assignments []db.GlobalRoleAssignment,
) EffectiveGlobalPermissions {
	result := EffectiveGlobalPermissions{
		Decisions: make([]EffectivePermissionDecision, 0, len(GlobalPermissionCatalog())),
	}
	if isBuiltInAdministrator {
		result.Permissions = db.AllGlobalPermissions
	} else {
		for _, assignment := range assignments {
			result.Permissions |= assignment.GlobalPermissions
		}
	}

	for _, definition := range GlobalPermissionCatalog() {
		var grant *PermissionGrant
		if isBuiltInAdministrator {
			grant = &PermissionGrant{
				Scope: PermissionScopeGlobal, RoleID: "admin", RoleName: "Administrator",
				Permissions: []PermissionID{definition.ID},
			}
		} else {
			permission := db.GlobalPermission(definition.Permission)
			for _, assignment := range assignments {
				if assignment.GlobalPermissions.Can(permission) {
					grant = &PermissionGrant{
						Scope: PermissionScopeGlobal, RoleID: string(assignment.RoleID),
						RoleName: assignment.RoleName, Permissions: []PermissionID{definition.ID},
					}
					break
				}
			}
		}
		result.Decisions = append(result.Decisions, EvaluatePermission(
			PermissionEvaluationRequest{Permission: definition.ID, Global: grant},
		))
	}
	return result
}

// EvaluatePermission applies global/project/template/ownership/capability in a
// fixed order. Scope mappings are explicit switches rather than prefix rules.
func EvaluatePermission(request PermissionEvaluationRequest) EffectivePermissionDecision {
	definition, known := PermissionDefinitionByID(request.Permission)
	if !known {
		return deniedPermission(request.Permission, "", PermissionScopeCapability)
	}

	var decision EffectivePermissionDecision
	switch definition.Scope {
	case PermissionScopeGlobal:
		decision = evaluateGrant(request.Permission, request.Global, PermissionScopeGlobal)
	case PermissionScopeProject:
		decision = evaluateGrant(request.Permission, request.Project, PermissionScopeProject)
	case PermissionScopeTemplate:
		decision = evaluateTemplatePermission(request)
	default:
		decision = deniedPermission(request.Permission, "", definition.Scope)
	}

	if decision.Allowed && request.Ownership != nil && !request.Ownership.Allowed {
		decision = deniedPermission(request.Permission, request.Ownership.ID, PermissionScopeOwnership)
	}
	if decision.Allowed && request.Capability != nil && !request.Capability.Allowed {
		decision = deniedPermission(request.Permission, request.Capability.ID, PermissionScopeCapability)
	}
	return decision
}

func evaluateGrant(
	permission PermissionID,
	grant *PermissionGrant,
	scope PermissionScope,
) EffectivePermissionDecision {
	if grant != nil && grant.Scope == scope && containsPermission(grant.Permissions, permission) {
		return EffectivePermissionDecision{
			Permission: permission,
			Allowed:    true,
			Provenance: PermissionProvenance{
				Scope: scope, RoleID: grant.RoleID, RoleName: grant.RoleName,
				Effect: PermissionEffectAllow,
			},
		}
	}
	return deniedPermission(permission, "", scope)
}

func evaluateTemplatePermission(request PermissionEvaluationRequest) EffectivePermissionDecision {
	if request.Template != nil {
		if containsPermission(request.Template.Deny, request.Permission) {
			return EffectivePermissionDecision{
				Permission: request.Permission,
				Provenance: PermissionProvenance{
					Scope: PermissionScopeTemplate, RoleID: request.Template.RoleID,
					RoleName: request.Template.RoleName, Effect: PermissionEffectDeny,
				},
			}
		}
		if containsPermission(request.Template.Allow, request.Permission) {
			return EffectivePermissionDecision{
				Permission: request.Permission,
				Allowed:    true,
				Provenance: PermissionProvenance{
					Scope: PermissionScopeTemplate, RoleID: request.Template.RoleID,
					RoleName: request.Template.RoleName, Effect: PermissionEffectAllow,
				},
			}
		}
	}

	inherited, ok := inheritedProjectPermission(request.Permission)
	if ok && request.Project != nil && request.Project.Scope == PermissionScopeProject &&
		containsPermission(request.Project.Permissions, inherited) {
		return EffectivePermissionDecision{
			Permission: request.Permission,
			Allowed:    true,
			Provenance: PermissionProvenance{
				Scope: PermissionScopeProject, RoleID: request.Project.RoleID,
				RoleName: request.Project.RoleName, Effect: PermissionEffectInherited,
			},
		}
	}
	return deniedPermission(request.Permission, "", PermissionScopeTemplate)
}

func inheritedProjectPermission(permission PermissionID) (PermissionID, bool) {
	switch permission {
	case PermissionReadTemplate:
		return PermissionViewProjectResources, true
	case PermissionRunTemplate:
		return PermissionRunProjectTasks, true
	case PermissionEditTemplate, PermissionDeleteTemplate:
		return PermissionManageProjectResources, true
	default:
		return "", false
	}
}

func containsPermission(permissions []PermissionID, permission PermissionID) bool {
	for _, candidate := range permissions {
		if candidate == permission {
			return true
		}
	}
	return false
}

func deniedPermission(
	permission PermissionID,
	constraintID string,
	scope PermissionScope,
) EffectivePermissionDecision {
	return EffectivePermissionDecision{
		Permission: permission,
		Provenance: PermissionProvenance{
			Scope: scope, Effect: PermissionEffectDeny, ConstraintID: constraintID,
		},
	}
}
