package projects

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/audit"
)

// WorkflowPermController manages role grants on a workflow.
type WorkflowPermController struct{ workflowRoles db.WorkflowRoleManager }

// The optional subscription argument is retained for upstream callers; EX
// availability is governed by authorization, not edition entitlements.
func NewWorkflowPermController(workflowRoles db.WorkflowRoleManager) *WorkflowPermController {
	return &WorkflowPermController{workflowRoles: workflowRoles}
}

func (c *WorkflowPermController) GetWorkflowPerms(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)

	perms, err := c.workflowRoles.GetWorkflowRoles(workflow.ProjectID, workflow.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, perms)
}

func (c *WorkflowPermController) AddWorkflowPerm(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)

	var perm db.WorkflowRolePerm
	if !helpers.Bind(w, r, &perm) {
		return
	}
	if perm.Permissions&^db.WorkflowGrantPermissions != 0 {
		helpers.WriteErrorStatus(w, "Permissions are not workflow permissions", http.StatusBadRequest)
		return
	}
	// A verb on a workflow includes seeing it.
	if perm.Permissions != 0 {
		perm.Permissions |= db.CanReadProject
	}

	// Role identity is validated by the workflow grant store.

	perm.ProjectID = workflow.ProjectID
	perm.WorkflowID = workflow.ID

	newPerm, err := c.workflowRoles.CreateWorkflowRole(perm)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	newPerm, err = c.workflowRoles.GetWorkflowRole(workflow.ProjectID, workflow.ID, newPerm.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.IAMWorkflowPermissionCreate,
		Target:    &audit.Target{Type: audit.TargetWorkflowPermission, ID: strconv.Itoa(newPerm.ID)},
		ProjectID: workflow.ProjectID,
		Metadata: audit.WorkflowPermissionMetadata{
			WorkflowID:  workflow.ID,
			Role:        audit.NewRoleRef(db.Role{ID: newPerm.RoleID, Slug: newPerm.RoleSlug}),
			Permissions: audit.PermissionNames(newPerm.Permissions),
		},
	})

	helpers.WriteJSON(w, http.StatusCreated, newPerm)
}

func (c *WorkflowPermController) UpdateWorkflowPerm(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	permID, ok := helpers.GetIntParamOrAbort("perm_id", w, r)
	if !ok {
		return
	}

	var perm db.WorkflowRolePerm
	if !helpers.Bind(w, r, &perm) {
		return
	}
	if perm.Permissions&^db.WorkflowGrantPermissions != 0 {
		helpers.WriteErrorStatus(w, "Permissions are not workflow permissions", http.StatusBadRequest)
		return
	}
	// A verb on a workflow includes seeing it.
	if perm.Permissions != 0 {
		perm.Permissions |= db.CanReadProject
	}

	perm.ID = permID
	perm.ProjectID = workflow.ProjectID
	perm.WorkflowID = workflow.ID

	// The update changes only permissions, so the role comes from the stored grant.
	stored, err := c.workflowRoles.GetWorkflowRole(workflow.ProjectID, workflow.ID, permID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if err = c.workflowRoles.UpdateWorkflowRole(perm); err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.IAMWorkflowPermissionUpdate,
		Target:    &audit.Target{Type: audit.TargetWorkflowPermission, ID: strconv.Itoa(permID)},
		ProjectID: workflow.ProjectID,
		Metadata: audit.WorkflowPermissionMetadata{
			WorkflowID:  workflow.ID,
			Role:        audit.NewRoleRef(db.Role{ID: stored.RoleID, Slug: stored.RoleSlug}),
			Permissions: audit.PermissionNames(perm.Permissions),
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

func (c *WorkflowPermController) DeleteWorkflowPerm(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	permID, ok := helpers.GetIntParamOrAbort("perm_id", w, r)
	if !ok {
		return
	}

	err := c.workflowRoles.DeleteWorkflowRole(workflow.ProjectID, workflow.ID, permID)
	if errors.Is(err, db.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.IAMWorkflowPermissionDelete,
		Target:    &audit.Target{Type: audit.TargetWorkflowPermission, ID: strconv.Itoa(permID)},
		ProjectID: workflow.ProjectID,
		Metadata:  audit.WorkflowPermissionMetadata{WorkflowID: workflow.ID},
	})

	w.WriteHeader(http.StatusNoContent)
}

func (c *WorkflowPermController) GetWorkflowPerm(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	permID, ok := helpers.GetIntParamOrAbort("perm_id", w, r)
	if !ok {
		return
	}

	perm, err := c.workflowRoles.GetWorkflowRole(workflow.ProjectID, workflow.ID, permID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, perm)
}
