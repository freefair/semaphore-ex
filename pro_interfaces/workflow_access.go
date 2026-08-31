package pro_interfaces

import "github.com/semaphoreui/semaphore/db"

// WorkflowAuthorizationIdentityStore supplies the current, fail-closed project
// role identity and the live custom-role catalog for a workflow decision.
// Directory claims remain inside the repository; callers receive only bounded
// role provenance.
type WorkflowAuthorizationIdentityStore interface {
	db.ProjectWorkflowRoleIdentityResolver
	GetProjectRoles(projectID int) ([]db.Role, error)
}

// WorkflowAuthorizationState is the live role state required by the pure
// workflow access evaluators. It is intentionally scoped to one project and
// one actor so it cannot be reused as a cross-project authorization grant.
type WorkflowAuthorizationState struct {
	Identity   db.ProjectWorkflowRoleIdentity
	KnownRoles map[db.ProjectRoleReference]bool
}

// ResolveWorkflowAuthorizationState resolves the actor's single current
// project role plus every currently known project role. Missing or malformed
// membership fails closed.
func ResolveWorkflowAuthorizationState(
	store WorkflowAuthorizationIdentityStore,
	projectID int,
	actorID int,
) (WorkflowAuthorizationState, error) {
	identity, err := store.ResolveProjectWorkflowRoleIdentity(projectID, actorID)
	if err != nil {
		return WorkflowAuthorizationState{}, err
	}
	if err = identity.Validate(); err != nil {
		return WorkflowAuthorizationState{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	roles, err := store.GetProjectRoles(projectID)
	if err != nil {
		return WorkflowAuthorizationState{}, err
	}
	roleIDs := make([]db.ProjectRoleID, 0, len(roles))
	for _, role := range roles {
		if role.ProjectID == nil || *role.ProjectID != projectID || role.Revision < 1 {
			return WorkflowAuthorizationState{}, db.ErrProjectWorkflowRoleIdentityUnavailable
		}
		roleIDs = append(roleIDs, role.ID)
	}
	return WorkflowAuthorizationState{
		Identity: identity, KnownRoles: db.KnownProjectRoleReferences(roleIDs),
	}, nil
}

// AuthorizeWorkflowRead decides whether a workflow definition, run, artifact,
// or workflow-owned task may be read. The caller chooses the live workflow or
// immutable run snapshot deliberately.
func AuthorizeWorkflowRead(
	workflow db.WorkflowTemplate,
	identity db.ProjectWorkflowRoleIdentity,
	knownRoles map[db.ProjectRoleReference]bool,
) WorkflowAccessDecision {
	return authorizeWorkflowAccess(workflow, identity, knownRoles, PermissionViewWorkflow)
}

// AuthorizeWorkflowTaskRead is kept separate so every direct task/log route
// can use the same fail-closed rule without treating workflow tasks as normal
// project tasks.
func AuthorizeWorkflowTaskRead(
	workflow db.WorkflowTemplate,
	identity db.ProjectWorkflowRoleIdentity,
	knownRoles map[db.ProjectRoleReference]bool,
) WorkflowAccessDecision {
	return AuthorizeWorkflowRead(workflow, identity, knownRoles)
}

// AuthorizeWorkflowTriggerRead keeps trigger discovery bound to workflow view
// access. Trigger credentials remain absent from this read decision.
func AuthorizeWorkflowTriggerRead(
	workflow db.WorkflowTemplate,
	identity db.ProjectWorkflowRoleIdentity,
	knownRoles map[db.ProjectRoleReference]bool,
) WorkflowAccessDecision {
	return AuthorizeWorkflowRead(workflow, identity, knownRoles)
}

// AuthorizeWorkflowTriggerAdmin gates all trigger mutation and history
// administration through workflow administer, with view as a prerequisite.
func AuthorizeWorkflowTriggerAdmin(
	workflow db.WorkflowTemplate,
	identity db.ProjectWorkflowRoleIdentity,
	knownRoles map[db.ProjectRoleReference]bool,
) WorkflowAccessDecision {
	if !AuthorizeWorkflowRead(workflow, identity, knownRoles).Allowed {
		return WorkflowAccessDecision{}
	}
	return authorizeWorkflowAccess(workflow, identity, knownRoles, PermissionAdministerWorkflow)
}

// FilterWorkflowTemplatesByAccess removes workflows a caller may not view.
// Collection callers must use it rather than returning a list and relying on
// clients to hide rows.
func FilterWorkflowTemplatesByAccess(
	workflows []db.WorkflowTemplate,
	identity db.ProjectWorkflowRoleIdentity,
	knownRoles map[db.ProjectRoleReference]bool,
) []db.WorkflowTemplate {
	visible := make([]db.WorkflowTemplate, 0, len(workflows))
	for _, workflow := range workflows {
		if AuthorizeWorkflowRead(workflow, identity, knownRoles).Allowed {
			visible = append(visible, workflow)
		}
	}
	return visible
}

// AuthorizeWorkflowApprovalQuorum evaluates persisted contributions only. SQL
// code must still enforce the one-row-per-actor invariant atomically.
func AuthorizeWorkflowApprovalQuorum(
	policy db.WorkflowApprovalRolePolicy,
	initiatorUserID int,
	knownRoles map[db.ProjectRoleReference]bool,
	contributions []db.WorkflowApprovalContribution,
) WorkflowApprovalProgress {
	return EvaluateWorkflowApprovalProgress(WorkflowApprovalProgressRequest{
		Policy: policy, InitiatorUserID: initiatorUserID,
		KnownRoleReferences: knownRoles, Contributions: contributions,
	})
}

func authorizeWorkflowAccess(
	workflow db.WorkflowTemplate,
	identity db.ProjectWorkflowRoleIdentity,
	knownRoles map[db.ProjectRoleReference]bool,
	permission PermissionID,
) WorkflowAccessDecision {
	if identity.Validate() != nil {
		return WorkflowAccessDecision{}
	}
	return EvaluateWorkflowAccess(WorkflowAccessRequest{
		Permission: permission, ProjectPermissions: identity.Permissions,
		EffectiveRoleReferences: []db.ProjectRoleReference{identity.Reference},
		KnownRoleReferences:     knownRoles, Policy: workflow.AccessPolicy,
	})
}
