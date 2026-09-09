package projects

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const workflowTriggerBodyLimit int64 = 256 * 1024

type workflowTriggerController struct {
	service pro_interfaces.WorkflowTriggerService
}

var _ pro_interfaces.WorkflowTriggerController = (*workflowTriggerController)(nil)

func NewWorkflowTriggerController(service pro_interfaces.WorkflowTriggerService) pro_interfaces.WorkflowTriggerController {
	return &workflowTriggerController{service: service}
}

func (c *workflowTriggerController) GetTriggers(w http.ResponseWriter, r *http.Request) {
	project, workflow, ok := workflowTriggerContext(w, r)
	if !ok {
		return
	}
	triggers, err := c.service.List(r.Context(), project.ID, workflow.ID, helpers.QueryParams(r.URL), helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, triggers)
}

func (c *workflowTriggerController) AddTrigger(w http.ResponseWriter, r *http.Request) {
	project, workflow, ok := workflowTriggerContext(w, r)
	if !ok {
		return
	}
	var trigger db.WorkflowTrigger
	if !bindWorkflowTriggerBody(w, r, &trigger) {
		return
	}
	created, err := c.service.Create(r.Context(), project.ID, workflow.ID, trigger, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, created)
}

func (c *workflowTriggerController) GetTrigger(w http.ResponseWriter, r *http.Request) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	trigger, err := c.service.Get(r.Context(), project.ID, workflow.ID, triggerID, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, trigger)
}

func (c *workflowTriggerController) UpdateTrigger(w http.ResponseWriter, r *http.Request) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	var trigger db.WorkflowTrigger
	if !bindWorkflowTriggerBody(w, r, &trigger) {
		return
	}
	updated, err := c.service.Update(r.Context(), project.ID, workflow.ID, triggerID, trigger, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *workflowTriggerController) DeleteTrigger(w http.ResponseWriter, r *http.Request) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	if err := c.service.Delete(r.Context(), project.ID, workflow.ID, triggerID, helpers.UserFromContext(r)); err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *workflowTriggerController) SetTriggerEnabled(w http.ResponseWriter, r *http.Request) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision int  `json:"revision"`
		Enabled  bool `json:"enabled"`
	}
	if !bindWorkflowTriggerBody(w, r, &input) {
		return
	}
	updated, err := c.service.SetEnabled(r.Context(), project.ID, workflow.ID, triggerID, input.Revision, input.Enabled, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *workflowTriggerController) RotateTriggerCredential(w http.ResponseWriter, r *http.Request) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision int `json:"revision"`
	}
	if !bindWorkflowTriggerBody(w, r, &input) {
		return
	}
	rotated, err := c.service.RotateCredential(r.Context(), project.ID, workflow.ID, triggerID, input.Revision, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, rotated)
}

func (c *workflowTriggerController) StageWebhookSigningKey(w http.ResponseWriter, r *http.Request) {
	c.mutateWebhookSigningKey(w, r, func(projectID, workflowID, triggerID, revision int, actor *db.User) (any, error) {
		return c.service.StageWebhookSigningKey(r.Context(), projectID, workflowID, triggerID, revision, actor)
	})
}

func (c *workflowTriggerController) BootstrapWebhookSigningKey(w http.ResponseWriter, r *http.Request) {
	c.mutateWebhookSigningKey(w, r, func(projectID, workflowID, triggerID, revision int, actor *db.User) (any, error) {
		return c.service.BootstrapWebhookSigningKey(r.Context(), projectID, workflowID, triggerID, revision, actor)
	})
}

func (c *workflowTriggerController) PromoteWebhookSigningKey(w http.ResponseWriter, r *http.Request) {
	c.mutateWebhookSigningKey(w, r, func(projectID, workflowID, triggerID, revision int, actor *db.User) (any, error) {
		return c.service.PromoteWebhookSigningKey(r.Context(), projectID, workflowID, triggerID, revision, actor)
	})
}

func (c *workflowTriggerController) RevokeWebhookSigningKey(w http.ResponseWriter, r *http.Request) {
	c.mutateWebhookSigningKey(w, r, func(projectID, workflowID, triggerID, revision int, actor *db.User) (any, error) {
		return c.service.RevokeWebhookSigningKey(r.Context(), projectID, workflowID, triggerID, revision, actor)
	})
}

func (c *workflowTriggerController) mutateWebhookSigningKey(w http.ResponseWriter, r *http.Request, mutate func(int, int, int, int, *db.User) (any, error)) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision int `json:"revision"`
	}
	if !bindWorkflowTriggerBody(w, r, &input) {
		return
	}
	result, err := mutate(project.ID, workflow.ID, triggerID, input.Revision, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *workflowTriggerController) TestTrigger(w http.ResponseWriter, r *http.Request) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	input, ok := bindWorkflowTriggerInvocation(w, r)
	if !ok {
		return
	}
	result, err := c.service.Test(r.Context(), project.ID, workflow.ID, triggerID, input.Inputs, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, newWorkflowTriggerFireView(result))
}

func (c *workflowTriggerController) GetTriggerHistory(w http.ResponseWriter, r *http.Request) {
	project, workflow, triggerID, ok := workflowTriggerResourceContext(w, r)
	if !ok {
		return
	}
	history, err := c.service.History(r.Context(), project.ID, workflow.ID, triggerID, helpers.QueryParams(r.URL), helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, history)
}

func (c *workflowTriggerController) InvokeAPITrigger(w http.ResponseWriter, r *http.Request) {
	c.invokeAPITrigger(w, r)
}

func (c *workflowTriggerController) InvokeWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	projectID, workflowID, triggerID, ok := workflowTriggerExternalIDs(w, r)
	if !ok {
		return
	}
	// A webhook is authenticated only by the signed protocol. In particular an
	// Authorization header cannot select the legacy API credential path.
	if bearerWorkflowTriggerCredential(r.Header.Get("Authorization")) != "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, ok := readWorkflowWebhookBody(w, r)
	if !ok {
		return
	}
	bound, err := pro_interfaces.BindWebhookSignedRequestFromHTTP(r.Method, r.RequestURI, r.Header, body)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	result, err := c.service.FireSignedWebhook(r.Context(), projectID, workflowID, triggerID, bound)
	if err != nil {
		// Verification errors intentionally collapse to one response to avoid
		// revealing key, freshness, or trigger state to an unauthenticated peer.
		if errors.Is(err, pro_interfaces.ErrWorkflowTriggerWebhookReplay) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, newWorkflowTriggerFireView(result))
}

func (c *workflowTriggerController) invokeAPITrigger(w http.ResponseWriter, r *http.Request) {
	projectID, workflowID, triggerID, ok := workflowTriggerExternalIDs(w, r)
	if !ok {
		return
	}
	credential := bearerWorkflowTriggerCredential(r.Header.Get("Authorization"))
	if credential == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	input, ok := bindWorkflowTriggerInvocation(w, r)
	if !ok {
		return
	}
	result, err := c.service.FireExternal(r.Context(), projectID, workflowID, triggerID, db.WorkflowTriggerAPI, credential, strings.TrimSpace(r.Header.Get("Idempotency-Key")), input.Inputs)
	if err != nil {
		writeWorkflowTriggerError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	helpers.WriteJSON(w, status, newWorkflowTriggerFireView(result))
}

func workflowTriggerExternalIDs(w http.ResponseWriter, r *http.Request) (int, int, int, bool) {
	projectID, ok := helpers.GetIntParamOrAbort("project_id", w, r)
	if !ok {
		return 0, 0, 0, false
	}
	workflowID, ok := helpers.GetIntParamOrAbort("workflow_id", w, r)
	if !ok {
		return 0, 0, 0, false
	}
	triggerID, ok := helpers.GetIntParamOrAbort("trigger_id", w, r)
	if !ok {
		return 0, 0, 0, false
	}
	return projectID, workflowID, triggerID, true
}

func readWorkflowWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return []byte{}, true
	}
	r.Body = http.MaxBytesReader(w, r.Body, workflowTriggerBodyLimit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return nil, false
	}
	return body, true
}

type workflowTriggerInvocationView struct {
	ID        int                                `json:"id"`
	TriggerID int                                `json:"workflow_trigger_id"`
	Status    db.WorkflowTriggerInvocationStatus `json:"status"`
	RunID     *int                               `json:"run_id,omitempty"`
	Result    string                             `json:"result,omitempty"`
	Reason    string                             `json:"reason,omitempty"`
}

type workflowTriggerFireView struct {
	Invocation workflowTriggerInvocationView `json:"invocation"`
	Run        workflowRunView               `json:"run"`
	Duplicate  bool                          `json:"duplicate"`
}

func newWorkflowTriggerFireView(result pro_interfaces.WorkflowTriggerFireResult) workflowTriggerFireView {
	return workflowTriggerFireView{
		Invocation: workflowTriggerInvocationView{
			ID: result.Invocation.ID, TriggerID: result.Invocation.WorkflowTriggerID,
			Status: result.Invocation.Status, RunID: result.Invocation.RunID,
			Result: result.Invocation.Result, Reason: result.Invocation.Reason,
		},
		Run: newWorkflowRunView(result.Run), Duplicate: result.Duplicate,
	}
}

type workflowTriggerInvocationInput struct {
	Inputs map[string]json.RawMessage `json:"inputs"`
}

func bindWorkflowTriggerInvocation(w http.ResponseWriter, r *http.Request) (workflowTriggerInvocationInput, bool) {
	input := workflowTriggerInvocationInput{Inputs: map[string]json.RawMessage{}}
	if r.Body == nil {
		return input, true
	}
	if !bindWorkflowTriggerBody(w, r, &input) {
		return workflowTriggerInvocationInput{}, false
	}
	if input.Inputs == nil {
		input.Inputs = map[string]json.RawMessage{}
	}
	return input, true
}

func bindWorkflowTriggerBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, workflowTriggerBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func workflowTriggerContext(w http.ResponseWriter, r *http.Request) (db.Project, db.WorkflowTemplate, bool) {
	project, projectOK := helpers.GetFromContext(r, "project").(db.Project)
	workflow, workflowOK := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	if !projectOK || !workflowOK || workflow.ProjectID != project.ID {
		w.WriteHeader(http.StatusNotFound)
		return db.Project{}, db.WorkflowTemplate{}, false
	}
	return project, workflow, true
}

func workflowTriggerResourceContext(w http.ResponseWriter, r *http.Request) (db.Project, db.WorkflowTemplate, int, bool) {
	project, workflow, ok := workflowTriggerContext(w, r)
	if !ok {
		return db.Project{}, db.WorkflowTemplate{}, 0, false
	}
	triggerID, ok := helpers.GetIntParamOrAbort("trigger_id", w, r)
	if !ok {
		return db.Project{}, db.WorkflowTemplate{}, 0, false
	}
	return project, workflow, triggerID, true
}

func bearerWorkflowTriggerCredential(header string) string {
	scheme, credential, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !strings.HasPrefix(credential, db.WorkflowTriggerCredentialPrefix) {
		return ""
	}
	return credential
}

func writeWorkflowTriggerError(w http.ResponseWriter, err error) {
	var capabilityDenied pro_interfaces.CapabilityDeniedError
	switch {
	case errors.Is(err, pro_interfaces.ErrWorkflowTriggerCredentialRejected):
		w.WriteHeader(http.StatusUnauthorized)
	case errors.Is(err, pro_interfaces.ErrWorkflowTriggerPermissionDenied), errors.As(err, &capabilityDenied):
		w.WriteHeader(http.StatusForbidden)
	case errors.Is(err, pro_interfaces.ErrWorkflowTriggerTypeMismatch), errors.Is(err, db.ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, db.ErrWorkflowTriggerRevisionConflict):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "WORKFLOW_TRIGGER_REVISION_CONFLICT", "message": err.Error(),
		})
	case errors.Is(err, db.ErrWorkflowTriggerStateChanged):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "WORKFLOW_TRIGGER_STATE_CHANGED", "message": err.Error(),
		})
	case errors.Is(err, pro_interfaces.ErrWorkflowTriggerSigningStateConflict):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "WORKFLOW_TRIGGER_SIGNING_STATE_CONFLICT", "message": "Webhook signing state conflict",
		})
	case errors.Is(err, pro_interfaces.ErrWorkflowTriggerSigningUnavailable):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "WORKFLOW_TRIGGER_SIGNING_UNAVAILABLE", "message": "Webhook signing is unavailable",
		})
	default:
		helpers.WriteError(w, err)
	}
}
