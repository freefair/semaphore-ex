package projects

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	coreprojects "github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	deploymentWindowBodyLimit       int64 = 64 * 1024
	defaultDeploymentWindowPageSize       = 25
)

// deploymentWindowController is deliberately transport-only. The project is
// always taken from ProjectMiddleware; JSON can neither select a tenant nor
// disclose a target outside that tenant.
type deploymentWindowController struct {
	service       pro_interfaces.DeploymentWindowGovernanceServiceFacade
	workflowStore db.WorkflowManager
}

var _ pro_interfaces.DeploymentWindowController = (*deploymentWindowController)(nil)

func NewDeploymentWindowController(
	service pro_interfaces.DeploymentWindowGovernanceServiceFacade,
	workflowStore db.WorkflowManager,
) pro_interfaces.DeploymentWindowController {
	return &deploymentWindowController{service: service, workflowStore: workflowStore}
}

type deploymentWindowPolicyInput struct {
	Revision int                        `json:"revision"`
	Timezone string                     `json:"timezone"`
	Default  db.DeploymentWindowDefault `json:"default"`
	Rules    []db.DeploymentWindowRule  `json:"rules"`
}

type deploymentWindowTargetInput struct {
	TemplateID *int `json:"template_id,omitempty"`
	WorkflowID *int `json:"workflow_id,omitempty"`
}

type deploymentWindowPreviewInput struct {
	deploymentWindowPolicyInput
	deploymentWindowTargetInput
}

func (c *deploymentWindowController) GetPolicy(w http.ResponseWriter, r *http.Request) {
	project, ok := c.manageProject(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		deploymentWindowUnavailable(w)
		return
	}
	policy, err := c.service.GetPolicy(r.Context(), project.ID)
	if err != nil {
		writeDeploymentWindowError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, policy)
}

func (c *deploymentWindowController) SavePolicy(w http.ResponseWriter, r *http.Request) {
	project, ok := c.manageProject(w, r)
	if !ok {
		return
	}
	var input deploymentWindowPolicyInput
	if !decodeDeploymentWindowJSON(w, r, &input) || input.Revision <= 0 {
		if input.Revision <= 0 {
			deploymentWindowBadRequest(w)
		}
		return
	}
	if c.service == nil {
		deploymentWindowUnavailable(w)
		return
	}
	policy, err := c.service.SavePolicy(r.Context(), db.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: input.Revision, Timezone: input.Timezone, Default: input.Default, Rules: input.Rules,
	}, input.Revision)
	if err != nil {
		writeDeploymentWindowError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, policy)
}

// ResetPolicy resets policy content to the default with a CAS revision. The
// expected revision is a query parameter so DELETE remains body-free.
func (c *deploymentWindowController) ResetPolicy(w http.ResponseWriter, r *http.Request) {
	project, ok := c.manageProject(w, r)
	if !ok {
		return
	}
	revision, ok := deploymentWindowExpectedRevision(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		deploymentWindowUnavailable(w)
		return
	}
	if err := c.service.ResetPolicy(r.Context(), project.ID, revision); err != nil {
		writeDeploymentWindowError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *deploymentWindowController) Preview(w http.ResponseWriter, r *http.Request) {
	project, ok := c.manageProject(w, r)
	if !ok {
		return
	}
	var input deploymentWindowPreviewInput
	if !decodeDeploymentWindowJSON(w, r, &input) || input.Revision <= 0 {
		if input.Revision <= 0 {
			deploymentWindowBadRequest(w)
		}
		return
	}
	request, ok := deploymentWindowTarget(w, project.ID, input.deploymentWindowTargetInput)
	if !ok {
		return
	}
	if !c.authorizeTarget(w, r, project, request) {
		return
	}
	if c.service == nil {
		deploymentWindowUnavailable(w)
		return
	}
	decision, err := c.service.Preview(r.Context(), db.DeploymentWindowPolicy{
		ProjectID: project.ID, Revision: input.Revision, Timezone: input.Timezone, Default: input.Default, Rules: input.Rules,
	}, request)
	if err != nil {
		writeDeploymentWindowError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, decision)
}

// CurrentStatus is deliberately available to an actor who can see and start
// the target. Unlike preview/history it returns no policy provenance.
func (c *deploymentWindowController) CurrentStatus(w http.ResponseWriter, r *http.Request) {
	project, ok := deploymentWindowProject(w, r)
	if !ok {
		return
	}
	target, ok := deploymentWindowStatusTarget(w, r, project.ID)
	if !ok || !c.authorizeTarget(w, r, project, target) {
		return
	}
	if c.service == nil {
		deploymentWindowUnavailable(w)
		return
	}
	decision, err := c.service.CurrentStatus(r.Context(), target)
	if err != nil {
		writeDeploymentWindowError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, pro_interfaces.DeploymentWindowPublicDecision{
		State: decision.State, Reason: decision.Reason, NextEligibleAt: decision.NextEligibleAt, NextEligibleKnown: decision.NextEligibleKnown,
	})
}

func (c *deploymentWindowController) DecisionHistory(w http.ResponseWriter, r *http.Request) {
	project, ok := c.manageProject(w, r)
	if !ok {
		return
	}
	params, ok := deploymentWindowHistoryParams(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		deploymentWindowUnavailable(w)
		return
	}
	history, err := c.service.DecisionHistory(r.Context(), project.ID, params)
	if err != nil {
		writeDeploymentWindowError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, history)
}

func (c *deploymentWindowController) manageProject(w http.ResponseWriter, r *http.Request) (db.Project, bool) {
	project, ok := deploymentWindowProject(w, r)
	if !ok {
		return db.Project{}, false
	}
	user := helpers.UserFromContext(r)
	permissions, permissionsOK := helpers.GetFromContext(r, "basePermissions").(db.ProjectUserPermission)
	if !permissionsOK {
		permissions, permissionsOK = helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
	}
	if user == nil || (!user.Admin && (!permissionsOK || !permissions.Can(db.CanManageProjectResources))) {
		w.WriteHeader(http.StatusForbidden)
		return db.Project{}, false
	}
	return project, true
}

func deploymentWindowProject(w http.ResponseWriter, r *http.Request) (db.Project, bool) {
	project, ok := helpers.GetFromContext(r, "project").(db.Project)
	if !ok || project.ID <= 0 {
		w.WriteHeader(http.StatusNotFound)
		return db.Project{}, false
	}
	return project, true
}

func deploymentWindowTarget(w http.ResponseWriter, projectID int, input deploymentWindowTargetInput) (pro_interfaces.DeploymentWindowStatusRequest, bool) {
	request := pro_interfaces.DeploymentWindowStatusRequest{ProjectID: projectID, TemplateID: input.TemplateID, WorkflowID: input.WorkflowID}
	if request.Validate() != nil || (input.TemplateID != nil && input.WorkflowID != nil) {
		deploymentWindowBadRequest(w)
		return pro_interfaces.DeploymentWindowStatusRequest{}, false
	}
	return request, true
}

func deploymentWindowStatusTarget(w http.ResponseWriter, r *http.Request, projectID int) (pro_interfaces.DeploymentWindowStatusRequest, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "template_id" && key != "workflow_id") || len(values) != 1 {
			deploymentWindowBadRequest(w)
			return pro_interfaces.DeploymentWindowStatusRequest{}, false
		}
	}
	input := deploymentWindowTargetInput{}
	if raw, exists := query["template_id"]; exists {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 {
			deploymentWindowBadRequest(w)
			return pro_interfaces.DeploymentWindowStatusRequest{}, false
		}
		input.TemplateID = &value
	}
	if raw, exists := query["workflow_id"]; exists {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 {
			deploymentWindowBadRequest(w)
			return pro_interfaces.DeploymentWindowStatusRequest{}, false
		}
		input.WorkflowID = &value
	}
	return deploymentWindowTarget(w, projectID, input)
}

func (c *deploymentWindowController) authorizeTarget(w http.ResponseWriter, r *http.Request, project db.Project, target pro_interfaces.DeploymentWindowStatusRequest) bool {
	user := helpers.UserFromContext(r)
	if user == nil {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	if user.Admin {
		return true
	}
	if target.TemplateID != nil {
		permissions, err := helpers.Store(r).GetTemplatePermissionContext(project.ID, *target.TemplateID, user.ID)
		if err != nil || !permissions.EffectivePermissions.Can(db.CanReadTemplate) {
			w.WriteHeader(http.StatusNotFound)
			return false
		}
		if !permissions.EffectivePermissions.Can(db.CanRunTemplate) {
			w.WriteHeader(http.StatusForbidden)
			return false
		}
		return true
	}
	if c.workflowStore == nil {
		deploymentWindowUnavailable(w)
		return false
	}
	workflow, err := c.workflowStore.GetWorkflowTemplate(project.ID, *target.WorkflowID)
	if err != nil || workflow.ProjectID != project.ID {
		w.WriteHeader(http.StatusNotFound)
		return false
	}
	viewAllowed, startAllowed, err := coreprojects.AuthorizeWorkflowRequest(r, workflow, pro_interfaces.PermissionStartWorkflow)
	if err != nil || !viewAllowed {
		w.WriteHeader(http.StatusNotFound)
		return false
	}
	if !startAllowed {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return true
}

func decodeDeploymentWindowJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if r.Body == nil {
		deploymentWindowBadRequest(w)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, deploymentWindowBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		deploymentWindowBadRequest(w)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		deploymentWindowBadRequest(w)
		return false
	}
	return true
}

func deploymentWindowExpectedRevision(w http.ResponseWriter, r *http.Request) (int, bool) {
	query := r.URL.Query()
	values, exists := query["expected_revision"]
	if len(query) != 1 || !exists || len(values) != 1 {
		deploymentWindowBadRequest(w)
		return 0, false
	}
	revision, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil || revision <= 0 {
		deploymentWindowBadRequest(w)
		return 0, false
	}
	return revision, true
}

func deploymentWindowHistoryParams(w http.ResponseWriter, r *http.Request) (db.RetrieveQueryParams, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "count" && key != "before") || len(values) != 1 {
			deploymentWindowBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
	}
	params := db.RetrieveQueryParams{Count: defaultDeploymentWindowPageSize}
	if raw, exists := query["count"]; exists {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 || value > db.MaxDeploymentWindowHistoryPage {
			deploymentWindowBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
		params.Count = value
	}
	if raw, exists := query["before"]; exists {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 {
			deploymentWindowBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
		params.BeforeID = value
	}
	return params, true
}

func deploymentWindowBadRequest(w http.ResponseWriter) {
	helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOW_INVALID_INPUT", http.StatusBadRequest)
}

func deploymentWindowUnavailable(w http.ResponseWriter) {
	helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOWS_UNAVAILABLE", http.StatusServiceUnavailable)
}

func writeDeploymentWindowError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrDeploymentWindowRevisionConflict):
		helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOW_REVISION_CONFLICT", http.StatusConflict)
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrDeploymentWindowTenantMismatch):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, db.ErrInvalidOperation):
		deploymentWindowBadRequest(w)
	default:
		deploymentWindowUnavailable(w)
	}
}
