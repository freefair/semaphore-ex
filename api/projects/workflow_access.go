package projects

import (
	"errors"
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// WorkflowAccessMiddleware applies a current workflow policy before an API
// handler sees a definition or run. View denial deliberately returns 404 so a
// direct URL cannot reveal a hidden workflow or run.
func WorkflowAccessMiddleware(permission pro_interfaces.PermissionID) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			workflow, ok := workflowAccessWorkflow(r)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			viewAllowed, allowed, err := AuthorizeWorkflowRequest(r, workflow, permission)
			if err != nil || !viewAllowed {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if !allowed {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WorkflowProjectPermissionMiddleware authorizes collection/create routes that
// have no existing workflow policy to load. Item routes must additionally use
// WorkflowAccessMiddleware so view narrowing remains a prerequisite.
func WorkflowProjectPermissionMiddleware(permission pro_interfaces.PermissionID) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := helpers.UserFromContext(r)
			if user == nil {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if user.Admin {
				next.ServeHTTP(w, r)
				return
			}
			project := helpers.GetFromContext(r, "project").(db.Project)
			store, ok := any(helpers.Store(r)).(pro_interfaces.WorkflowAuthorizationIdentityStore)
			if !ok {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			state, err := pro_interfaces.ResolveWorkflowAuthorizationState(store, project.ID, user.ID)
			if err != nil || !pro_interfaces.EvaluateWorkflowAccess(pro_interfaces.WorkflowAccessRequest{
				Permission: permission, ProjectPermissions: state.Identity.Permissions,
				EffectiveRoleReferences: []db.ProjectRoleReference{state.Identity.Reference},
				KnownRoleReferences:     state.KnownRoles,
			}).Allowed {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AuthorizeWorkflowRequest evaluates current identity and the supplied
// workflow or immutable run snapshot. A false view result must be translated
// to 404 by callers; a false action after visible means 403.
func AuthorizeWorkflowRequest(
	r *http.Request,
	workflow db.WorkflowTemplate,
	permission pro_interfaces.PermissionID,
) (viewAllowed bool, allowed bool, err error) {
	user := helpers.UserFromContext(r)
	if user == nil {
		return false, false, errors.New("workflow actor is unavailable")
	}
	if user.Admin {
		return true, true, nil
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	if workflow.ProjectID != project.ID {
		return false, false, errors.New("workflow project mismatch")
	}
	store, ok := any(helpers.Store(r)).(pro_interfaces.WorkflowAuthorizationIdentityStore)
	if !ok {
		return false, false, errors.New("workflow identity resolver is unavailable")
	}
	state, err := pro_interfaces.ResolveWorkflowAuthorizationState(store, project.ID, user.ID)
	if err != nil {
		return false, false, err
	}
	view := pro_interfaces.AuthorizeWorkflowRead(workflow, state.Identity, state.KnownRoles)
	if !view.Allowed {
		return false, false, nil
	}
	decision := pro_interfaces.EvaluateWorkflowAccess(pro_interfaces.WorkflowAccessRequest{
		Permission: permission, ProjectPermissions: state.Identity.Permissions,
		EffectiveRoleReferences: []db.ProjectRoleReference{state.Identity.Reference},
		KnownRoleReferences:     state.KnownRoles, Policy: workflow.AccessPolicy,
	})
	return true, decision.Allowed, nil
}

func workflowAccessWorkflow(r *http.Request) (db.WorkflowTemplate, bool) {
	if value, ok := helpers.GetOkFromContext(r, "workflow_run"); ok {
		if run, valid := value.(db.WorkflowRun); valid {
			return run.DefinitionSnapshot, true
		}
	}
	value, ok := helpers.GetOkFromContext(r, "workflow")
	workflow, valid := value.(db.WorkflowTemplate)
	return workflow, ok && valid
}
