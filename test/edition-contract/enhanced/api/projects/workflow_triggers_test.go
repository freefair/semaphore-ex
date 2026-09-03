package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	unknownRecorder := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(unknownRecorder, unknown)
	assert.Equal(t, http.StatusUnauthorized, unknownRecorder.Code)
	assert.Zero(t, service.fires)
}

func TestWorkflowTriggerAPIForwardsExactSignedWebhookRequest(t *testing.T) {
	service := &workflowTriggerAPIService{}
	controller := NewWorkflowTriggerController(service)
	key, err := pro_interfaces.NewWebhookSigningKey()
	require.NoError(t, err)
	body := []byte("{ \"inputs\" : { \"target\" : \"eu\" } }\n")
	target := "/api/workflow-triggers/7/9/13/webhook?exact=%2Fvalue&space=%20"
	headers := pro_interfaces.WebhookSignatureHeaders{Version: pro_interfaces.WebhookSignatureProtocolVersion, EventID: "evt_1234567890123456", Timestamp: fmt.Sprintf("%d", time.Now().Unix()), KeyID: key.ID}
	bound, err := pro_interfaces.BindWebhookSignedRequest(http.MethodPost, target, headers, body)
	require.NoError(t, err)
	bound.Signature, err = pro_interfaces.SignWebhookRequest(key.Secret, bound)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "http://example.test"+target, bytes.NewReader(body))
	request.RequestURI = target
	request = mux.SetURLVars(request, map[string]string{"project_id": "7", "workflow_id": "9", "trigger_id": "13"})
	request.Header.Set(pro_interfaces.WebhookHeaderVersion, headers.Version)
	request.Header.Set(pro_interfaces.WebhookHeaderEventID, headers.EventID)
	request.Header.Set(pro_interfaces.WebhookHeaderTimestamp, headers.Timestamp)
	request.Header.Set(pro_interfaces.WebhookHeaderKeyID, headers.KeyID)
	request.Header.Set(pro_interfaces.WebhookHeaderSignature, bound.Signature)
	recorder := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(recorder, request)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, target, service.signed.RequestTarget)
	assert.Equal(t, body, service.signed.Payload)

	for _, test := range []struct{ name, header string }{{"missing", pro_interfaces.WebhookHeaderSignature}, {"duplicate", pro_interfaces.WebhookHeaderEventID}} {
		t.Run(test.name, func(t *testing.T) {
			candidate := httptest.NewRequest(http.MethodPost, "http://example.test"+target, bytes.NewReader(body))
			candidate.RequestURI = target
			candidate = mux.SetURLVars(candidate, map[string]string{"project_id": "7", "workflow_id": "9", "trigger_id": "13"})
			candidate.Header = request.Header.Clone()
			if test.name == "missing" {
				candidate.Header.Del(test.header)
			} else {
				candidate.Header.Add(test.header, headers.EventID)
			}
			before := service.signedFires
			rec := httptest.NewRecorder()
			controller.InvokeWebhookTrigger(rec, candidate)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, before, service.signedFires)
		})
	}

	bearer := httptest.NewRequest(http.MethodPost, "http://example.test"+target, bytes.NewReader(body))
	bearer = mux.SetURLVars(bearer, map[string]string{"project_id": "7", "workflow_id": "9", "trigger_id": "13"})
	bearer.Header = request.Header.Clone()
	bearer.Header.Set("Authorization", "Bearer swt_once")
	beforeBearer := service.signedFires
	bearerRec := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(bearerRec, bearer)
	assert.Equal(t, http.StatusUnauthorized, bearerRec.Code)
	assert.Equal(t, beforeBearer, service.signedFires)

	overflow := httptest.NewRequest(http.MethodPost, "http://example.test"+target, bytes.NewReader(make([]byte, workflowTriggerBodyLimit+1)))
	overflow = mux.SetURLVars(overflow, map[string]string{"project_id": "7", "workflow_id": "9", "trigger_id": "13"})
	overflowRec := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(overflowRec, overflow)
	assert.Equal(t, http.StatusRequestEntityTooLarge, overflowRec.Code)

	service.signedError = pro_interfaces.ErrWorkflowTriggerWebhookReplay
	replay := httptest.NewRequest(http.MethodPost, "http://example.test"+target, bytes.NewReader(body))
	replay.RequestURI = target
	replay = mux.SetURLVars(replay, map[string]string{"project_id": "7", "workflow_id": "9", "trigger_id": "13"})
	replay.Header = request.Header.Clone()
	replayRec := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(replayRec, replay)
	assert.Equal(t, http.StatusConflict, replayRec.Code)
	service.signedError = errors.New("internal signed service failure")
	failure := httptest.NewRequest(http.MethodPost, "http://example.test"+target, bytes.NewReader(body))
	failure.RequestURI = target
	failure = mux.SetURLVars(failure, map[string]string{"project_id": "7", "workflow_id": "9", "trigger_id": "13"})
	failure.Header = request.Header.Clone()
	failureRec := httptest.NewRecorder()
	controller.InvokeWebhookTrigger(failureRec, failure)
	assert.Equal(t, http.StatusUnauthorized, failureRec.Code)
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
		{name: "signing conflict", err: pro_interfaces.ErrWorkflowTriggerSigningStateConflict, want: http.StatusConflict},
		{name: "signing unavailable", err: pro_interfaces.ErrWorkflowTriggerSigningUnavailable, want: http.StatusConflict},
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
	signed         pro_interfaces.WebhookSignedRequest
	signedFires    int
	signedError    error
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
func (s *workflowTriggerAPIService) FireSignedWebhook(_ context.Context, _ int, _ int, _ int, request pro_interfaces.WebhookSignedRequest) (pro_interfaces.WorkflowTriggerFireResult, error) {
	s.signedFires++
	s.signed = request
	return pro_interfaces.WorkflowTriggerFireResult{Invocation: db.WorkflowTriggerInvocation{ID: 19}, Run: db.WorkflowRun{ID: 17}}, s.signedError
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
func (s *workflowTriggerAPIService) History(context.Context, int, int, int, db.RetrieveQueryParams, *db.User) ([]pro_interfaces.WorkflowTriggerHistoryEntry, error) {
	return nil, nil
}
func (s *workflowTriggerAPIService) StageWebhookSigningKey(context.Context, int, int, int, int, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{}, nil
}
func (s *workflowTriggerAPIService) BootstrapWebhookSigningKey(context.Context, int, int, int, int, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{}, nil
}
func (s *workflowTriggerAPIService) PromoteWebhookSigningKey(context.Context, int, int, int, int, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, nil
}
func (s *workflowTriggerAPIService) RevokeWebhookSigningKey(context.Context, int, int, int, int, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, nil
}
