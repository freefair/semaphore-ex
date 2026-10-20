package projects

import (
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
)

// IntegrationController serves template and workflow integration targets in
// the single EX product. Authorization remains at the project route boundary.
type IntegrationController struct {
	store              db.Store
	workflowStore      db.WorkflowManager
	capabilityProvider pro_interfaces.CapabilityProvider
}

func NewIntegrationController(dependencies ...any) *IntegrationController {
	controller := &IntegrationController{}
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case db.Store:
			controller.store = value
		case db.WorkflowManager:
			controller.workflowStore = value
		case pro_interfaces.CapabilityProvider:
			controller.capabilityProvider = value
		}
	}
	return controller
}

// workflowTargetAllowed binds a workflow integration to the authenticated
// editor only after proving that the editor can currently view and start that
// workflow. The webhook later repeats the same checks with the stored actor.
func (c *IntegrationController) workflowTargetAllowed(w http.ResponseWriter, r *http.Request, integration db.Integration) bool {
	if !integration.RunsWorkflow() {
		return true
	}
	user, ok := helpers.GetOkFromContext(r, "user")
	actor, validUser := user.(*db.User)
	if !ok || !validUser || actor == nil {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return c.workflowIntegrationActorAllowed(w, r, integration, actor)
}

// workflowIntegrationActorAllowed validates the current workflow start
// boundary for both a requesting editor and the principal already bound to an
// integration. The latter remains server-owned and is never replaced by a
// child-resource mutation.
func (c *IntegrationController) workflowIntegrationActorAllowed(w http.ResponseWriter, r *http.Request, integration db.Integration, actor *db.User) bool {
	if actor == nil || c.store == nil || c.workflowStore == nil || c.capabilityProvider == nil {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	workflow, err := c.workflowStore.GetWorkflowTemplate(integration.ProjectID, *integration.WorkflowID)
	if err != nil || workflow.ID == 0 {
		w.WriteHeader(http.StatusNotFound)
		return false
	}
	if !actor.Admin {
		identityStore, available := any(c.store).(pro_interfaces.WorkflowAuthorizationIdentityStore)
		if !available {
			w.WriteHeader(http.StatusForbidden)
			return false
		}
		state, stateErr := pro_interfaces.ResolveWorkflowAuthorizationState(identityStore, workflow.ProjectID, actor.ID)
		if stateErr != nil || !pro_interfaces.AuthorizeWorkflowReadForState(workflow, state).Allowed || !pro_interfaces.EvaluateWorkflowAccess(pro_interfaces.WorkflowAccessRequest{
			Permission: pro_interfaces.PermissionStartWorkflow, ProjectPermissions: state.ProjectPermissions,
			EffectiveRoleReferences: state.RoleReferences(), KnownRoleReferences: state.KnownRoles, Policy: workflow.AccessPolicy,
		}).Allowed {
			w.WriteHeader(http.StatusForbidden)
			return false
		}
	}
	snapshot, err := c.capabilityProvider.Resolve(r.Context(), pro_interfaces.CapabilityRequest{UserID: actor.ID, IsAdmin: actor.Admin, At: tz.Now()})
	if err == nil {
		err = snapshot.Require(pro_interfaces.CapabilityWorkflowTriggers, pro_interfaces.CapabilityAccessExecute)
	}
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return true
}

// WorkflowIntegrationInputMutationMiddleware prevents a resource manager from
// changing the externally supplied inputs of a workflow integration unless
// both the editor and its persisted run principal can currently start the
// workflow. Template integrations retain their existing resource-management
// boundary.
func (c *IntegrationController) WorkflowIntegrationInputMutationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		integration, ok := helpers.GetFromContext(r, "integration").(db.Integration)
		if !ok || !integration.RunsWorkflow() {
			next.ServeHTTP(w, r)
			return
		}
		if !c.workflowIntegrationAuthorityAllowed(w, r, integration) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WorkflowIntegrationAliasMiddleware protects the bearer URL of a
// workflow-target integration. Reading or rotating that URL is equivalent to
// controlling an unauthenticated workflow entry point.
func (c *IntegrationController) WorkflowIntegrationAliasMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		integration, ok := helpers.GetFromContext(r, "integration").(db.Integration)
		if !ok || !integration.RunsWorkflow() || c.workflowIntegrationAuthorityAllowed(w, r, integration) {
			next.ServeHTTP(w, r)
		}
	})
}

// ProjectWorkflowIntegrationAliasMiddleware protects project-wide aliases
// only when their delivery fan-out includes a searchable workflow
// integration. Template-only project aliases retain the historical resource
// management boundary.
func (c *IntegrationController) ProjectWorkflowIntegrationAliasMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project, ok := helpers.GetFromContext(r, "project").(db.Project)
		if !ok || c.store == nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		integrations, err := c.store.GetIntegrations(project.ID, db.RetrieveQueryParams{}, false)
		if err != nil {
			helpers.WriteError(w, err)
			return
		}
		for _, integration := range integrations {
			if integration.Searchable && integration.RunsWorkflow() && !c.workflowIntegrationAuthorityAllowed(w, r, integration) {
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (c *IntegrationController) workflowIntegrationAuthorityAllowed(w http.ResponseWriter, r *http.Request, integration db.Integration) bool {
	if !c.workflowTargetAllowed(w, r, integration) {
		return false
	}
	if integration.WorkflowUserID == nil || *integration.WorkflowUserID <= 0 {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	principal, err := c.store.GetUser(*integration.WorkflowUserID)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return c.workflowIntegrationActorAllowed(w, r, integration, &principal)
}

// integrationMetadata names what the integration starts. An integration has
// exactly one target (Integration.Validate and the store enforce it), so one
// of the two ids is recorded, never both.
func integrationMetadata(integration db.Integration) audit.IntegrationMetadata {
	authMethod := string(integration.AuthMethod)
	if authMethod == "" {
		authMethod = "none"
	}
	metadata := audit.IntegrationMetadata{AuthMethod: authMethod}
	if integration.RunsWorkflow() {
		metadata.WorkflowID = *integration.WorkflowID
	} else if integration.TemplateID != nil {
		metadata.TemplateID = *integration.TemplateID
	}
	return metadata
}

func IntegrationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		integrationId, ok := helpers.GetIntParamOrAbort("integration_id", w, r)
		if !ok {
			return
		}

		projectId, ok := helpers.GetIntParamOrAbort("project_id", w, r)
		if !ok {
			return
		}

		integration, err := helpers.Store(r).GetIntegration(projectId, integrationId)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		r = helpers.SetContextValue(r, "integration", integration)
		next.ServeHTTP(w, r)
	})
}

func GetIntegration(w http.ResponseWriter, r *http.Request) {
	integration := helpers.GetFromContext(r, "integration").(db.Integration)
	helpers.WriteJSON(w, http.StatusOK, integration)
}

func GetIntegrations(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	integrations, err := helpers.Store(r).GetIntegrations(project.ID, helpers.QueryParams(r.URL), false)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, integrations)
}

func GetIntegrationRefs(w http.ResponseWriter, r *http.Request) {
	integration_id, ok := helpers.GetIntParamOrAbort("integration_id", w, r)

	if !ok {
		return
	}

	project := helpers.GetFromContext(r, "project").(db.Project)

	refs, err := helpers.Store(r).GetIntegrationRefs(project.ID, integration_id)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, refs)
}

func (c *IntegrationController) AddIntegration(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var integration db.Integration
	log.Info(fmt.Sprintf("Found Project: %v", project.ID))

	if !helpers.Bind(w, r, &integration) {
		log.Info("Failed to bind for integration uploads")

		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Project ID in body and URL must be the same",
		})
		return
	}

	if integration.ProjectID != project.ID {
		log.Error(fmt.Sprintf("Project ID in body and URL must be the same: %v vs. %v", integration.ProjectID, project.ID))

		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Project ID in body and URL must be the same",
		})
		return
	}
	err := integration.Validate()
	if err != nil {
		log.Error(err)
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	if !c.workflowTargetAllowed(w, r, integration) {
		return
	}
	if integration.RunsWorkflow() {
		actor := helpers.UserFromContext(r)
		integration.WorkflowUserID = &actor.ID
	} else {
		integration.WorkflowUserID = nil
	}

	newIntegration, errIntegration := helpers.Store(r).CreateIntegration(integration)

	if errIntegration != nil {
		log.Error(errIntegration)
		helpers.WriteError(w, errIntegration)
		return
	}

	// auth_method=none lets anyone start the template, which is what SIEM rules look for.
	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.ResourceIntegrationCreate,
		Target:    audit.ResourceTarget(audit.TargetIntegration, newIntegration.ID, newIntegration.Name),
		ProjectID: project.ID,
		Metadata:  integrationMetadata(newIntegration),
	})

	helpers.WriteJSON(w, http.StatusCreated, newIntegration)
}

func (c *IntegrationController) UpdateIntegration(w http.ResponseWriter, r *http.Request) {
	oldIntegration := helpers.GetFromContext(r, "integration").(db.Integration)
	var integration db.Integration

	if !helpers.Bind(w, r, &integration) {
		return
	}

	if integration.ID != oldIntegration.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Integration ID in body and URL must be the same",
		})
		return
	}

	if integration.ProjectID != oldIntegration.ProjectID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Project ID in body and URL must be the same",
		})
		return
	}

	if !c.workflowTargetAllowed(w, r, integration) {
		return
	}
	if integration.RunsWorkflow() {
		actor := helpers.UserFromContext(r)
		integration.WorkflowUserID = &actor.ID
	} else {
		integration.WorkflowUserID = nil
	}

	err := helpers.Store(r).UpdateIntegration(integration)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.ResourceIntegrationUpdate,
		Target:    audit.ResourceTarget(audit.TargetIntegration, oldIntegration.ID, integration.Name),
		ProjectID: oldIntegration.ProjectID,
		Metadata:  integrationMetadata(integration),
	})

	w.WriteHeader(http.StatusNoContent)
}

func DeleteIntegration(w http.ResponseWriter, r *http.Request) {
	integration := helpers.GetFromContext(r, "integration").(db.Integration)
	project := helpers.GetFromContext(r, "project").(db.Project)

	err := helpers.Store(r).DeleteIntegration(project.ID, integration.ID)
	if err == db.ErrInvalidOperation {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": "Integration failed to be deleted",
		})
		return
	}

	// The handler answers 204 on other store errors too, so only a real delete is recorded.
	if err == nil {
		helpers.Audit(r).Record(r.Context(), audit.Event{
			Kind:      audit.ResourceIntegrationDelete,
			Target:    audit.ResourceTarget(audit.TargetIntegration, integration.ID, integration.Name),
			ProjectID: project.ID,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}
