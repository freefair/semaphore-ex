package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowTriggerAPIRevealsCredentialOnceAndAuthenticatesExternalFire(t *testing.T) {
	service := &workflowTriggerAPIService{}
	controller := NewWorkflowTriggerController(service)
	request := workflowTriggerManagementRequest(
		http.MethodPost, "/api/project/7/workflows/9/triggers",
		`{"name":"Deploy API","type":"api","enabled":true,"input_mappings":[]}`,
	)
	recorder := httptest.NewRecorder()
	controller.AddTrigger(recorder, request)
	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"credential":"swt_once"`)
	assert.NotContains(t, recorder.Body.String(), "credential_hash")

	invoke := workflowTriggerExternalRequest(`{"inputs":{}}`)
	invoke.Header.Set("Authorization", "Bearer swt_once")
	invoke.Header.Set("Idempotency-Key", "deploy-once")
	invokeRecorder := httptest.NewRecorder()
	controller.InvokeAPITrigger(invokeRecorder, invoke)
	require.Equal(t, http.StatusCreated, invokeRecorder.Code)
	assert.Equal(t, "swt_once", service.credential)
	assert.Equal(t, "deploy-once", service.idempotencyKey)
}

func TestWorkflowTriggerAPIRejectsMissingCredentialAndUnknownInput(t *testing.T) {
	service := &workflowTriggerAPIService{}
	controller := NewWorkflowTriggerController(service)
	missing := workflowTriggerExternalRequest(`{"inputs":{}}`)
	recorder := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(recorder, missing)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)

	unknown := workflowTriggerExternalRequest(`{"inputs":{},"unexpected":true}`)
	unknown.Header.Set("Authorization", "Bearer swt_once")
	unknown.Header.Set("Idempotency-Key", "once")
	unknownRecorder := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(unknownRecorder, unknown)
	assert.Equal(t, http.StatusBadRequest, unknownRecorder.Code)
	assert.Zero(t, service.fires)
}

func TestWorkflowTriggerAPIMapsCredentialRevisionCapabilityAndPermissionErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "credential", err: pro_interfaces.ErrWorkflowTriggerCredentialRejected, want: http.StatusUnauthorized},
		{name: "permission", err: pro_interfaces.ErrWorkflowTriggerPermissionDenied, want: http.StatusForbidden},
		{name: "revision", err: db.ErrWorkflowTriggerRevisionConflict, want: http.StatusConflict},
		{name: "state changed", err: db.ErrWorkflowTriggerStateChanged, want: http.StatusConflict},
		{name: "not found", err: db.ErrNotFound, want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &workflowTriggerAPIService{fireError: test.err}
			controller := NewWorkflowTriggerController(service)
			request := workflowTriggerExternalRequest(`{"inputs":{}}`)
			request.Header.Set("Authorization", "Bearer swt_once")
			request.Header.Set("Idempotency-Key", "once")
			recorder := httptest.NewRecorder()
			controller.InvokeAPITrigger(recorder, request)
			assert.Equal(t, test.want, recorder.Code)
		})
	}
}

func workflowTriggerManagementRequest(method string, target string, body string) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request = mux.SetURLVars(request, map[string]string{"workflow_id": "9", "trigger_id": "13"})
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	request = helpers.SetContextValue(request, "workflow", db.WorkflowTemplate{ID: 9, ProjectID: 7})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 11})
	return request
}

func workflowTriggerExternalRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/workflow-triggers/7/9/13/api", bytes.NewBufferString(body))
	return mux.SetURLVars(request, map[string]string{"project_id": "7", "workflow_id": "9", "trigger_id": "13"})
}

type workflowTriggerAPIService struct {
	credential     string
	idempotencyKey string
	fires          int
	fireError      error
}

func (s *workflowTriggerAPIService) Create(context.Context, int, int, db.WorkflowTrigger, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{Trigger: db.WorkflowTrigger{ID: 13}, Credential: "swt_once"}, nil
}
func (s *workflowTriggerAPIService) FireExternal(_ context.Context, _, _, _ int, _ db.WorkflowTriggerType, credential, key string, _ map[string]json.RawMessage) (pro_interfaces.WorkflowTriggerFireResult, error) {
	s.fires++
	s.credential = credential
	s.idempotencyKey = key
	return pro_interfaces.WorkflowTriggerFireResult{Run: db.WorkflowRun{ID: 17}}, s.fireError
}
func (s *workflowTriggerAPIService) List(context.Context, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowTrigger, error) {
	return nil, nil
}
func (s *workflowTriggerAPIService) Get(context.Context, int, int, int, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, nil
}
func (s *workflowTriggerAPIService) Update(context.Context, int, int, int, db.WorkflowTrigger, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, nil
}
func (s *workflowTriggerAPIService) Delete(context.Context, int, int, int, *db.User) error {
	return nil
}
func (s *workflowTriggerAPIService) SetEnabled(context.Context, int, int, int, int, bool, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, nil
}
func (s *workflowTriggerAPIService) RotateCredential(context.Context, int, int, int, int, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{}, nil
}
func (s *workflowTriggerAPIService) Test(context.Context, int, int, int, map[string]json.RawMessage, *db.User) (pro_interfaces.WorkflowTriggerFireResult, error) {
	return pro_interfaces.WorkflowTriggerFireResult{}, nil
}
func (s *workflowTriggerAPIService) FireScheduled(context.Context, int, int, int, time.Time) (pro_interfaces.WorkflowTriggerFireResult, error) {
	return pro_interfaces.WorkflowTriggerFireResult{}, nil
}
func (s *workflowTriggerAPIService) History(context.Context, int, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowTriggerInvocation, error) {
	return nil, nil
}
