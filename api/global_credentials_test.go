package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type globalCredentialFacadeStub struct {
	createInput  pro_interfaces.GlobalCredentialInput
	rotateInput  pro_interfaces.GlobalCredentialMaterialInput
	grantInput   pro_interfaces.GlobalCredentialGrantInput
	grantedInput struct {
		projectID int
		params    db.RetrieveQueryParams
	}
	usageInput struct {
		credentialID int
		projectID    int
		taskID       int
		query        pro_interfaces.GlobalCredentialUsageQuery
	}
	actorID      int
	credentialID int
	grantID      int
	revision     int
	grantStatus  db.GlobalCredentialGrantStatus
	err          error
}

func (s *globalCredentialFacadeStub) ListGlobalCredentialUsage(_ context.Context, credentialID int, query pro_interfaces.GlobalCredentialUsageQuery) ([]pro_interfaces.GlobalCredentialUsageDTO, error) {
	s.usageInput.credentialID, s.usageInput.query = credentialID, query
	return []pro_interfaces.GlobalCredentialUsageDTO{{ID: 1}}, s.err
}
func (s *globalCredentialFacadeStub) GetGlobalCredentialImpact(context.Context, int) (pro_interfaces.GlobalCredentialImpactDTO, error) {
	return pro_interfaces.GlobalCredentialImpactDTO{CredentialID: 7, UsageCount: 2}, s.err
}
func (s *globalCredentialFacadeStub) ListTaskGlobalCredentialUsage(_ context.Context, projectID, taskID int, query pro_interfaces.GlobalCredentialUsageQuery) ([]pro_interfaces.GlobalCredentialUsageDTO, error) {
	s.usageInput.projectID, s.usageInput.taskID, s.usageInput.query = projectID, taskID, query
	return []pro_interfaces.GlobalCredentialUsageDTO{{ID: 1}}, s.err
}

func (s *globalCredentialFacadeStub) CreateGlobalCredential(_ context.Context, actorID int, input pro_interfaces.GlobalCredentialInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	s.actorID, s.createInput = actorID, input
	return globalCredentialSummary(), s.err
}
func (s *globalCredentialFacadeStub) GetGlobalCredential(context.Context, int) (pro_interfaces.GlobalCredentialDTO, error) {
	return pro_interfaces.GlobalCredentialDTO{}, s.err
}
func (s *globalCredentialFacadeStub) ListGlobalCredentials(context.Context, db.RetrieveQueryParams) ([]pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return nil, s.err
}
func (s *globalCredentialFacadeStub) UpdateGlobalCredential(context.Context, int, int, int, pro_interfaces.GlobalCredentialMetadataInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return globalCredentialSummary(), s.err
}
func (s *globalCredentialFacadeStub) SetGlobalCredentialEnabled(context.Context, int, int, int, bool) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return globalCredentialSummary(), s.err
}
func (s *globalCredentialFacadeStub) RotateGlobalCredential(_ context.Context, actorID, credentialID, revision int, input pro_interfaces.GlobalCredentialMaterialInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	s.actorID, s.credentialID, s.revision, s.rotateInput = actorID, credentialID, revision, input
	return globalCredentialSummary(), s.err
}
func (s *globalCredentialFacadeStub) DeleteGlobalCredential(context.Context, int, int, int) error {
	return s.err
}
func (s *globalCredentialFacadeStub) CreateGlobalCredentialGrant(_ context.Context, actorID, credentialID int, input pro_interfaces.GlobalCredentialGrantInput) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	s.actorID, s.credentialID, s.grantInput = actorID, credentialID, input
	return globalCredentialGrant(db.GlobalCredentialGrantStatusActive), s.err
}
func (s *globalCredentialFacadeStub) ListGlobalCredentialGrants(context.Context, int, db.RetrieveQueryParams) ([]pro_interfaces.GlobalCredentialGrantDTO, error) {
	return nil, s.err
}
func (s *globalCredentialFacadeStub) UpdateGlobalCredentialGrant(context.Context, int, int, int, int, pro_interfaces.GlobalCredentialGrantInput) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	return globalCredentialGrant(db.GlobalCredentialGrantStatusActive), s.err
}
func (s *globalCredentialFacadeStub) SetGlobalCredentialGrantStatus(_ context.Context, actorID, credentialID, grantID, revision int, status db.GlobalCredentialGrantStatus) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	s.actorID, s.credentialID, s.grantID, s.revision, s.grantStatus = actorID, credentialID, grantID, revision, status
	return globalCredentialGrant(status), s.err
}
func (s *globalCredentialFacadeStub) DeleteGlobalCredentialGrant(context.Context, int, int, int, int) error {
	return s.err
}
func (s *globalCredentialFacadeStub) ListGrantedCredentials(_ context.Context, projectID int, params db.RetrieveQueryParams) ([]pro_interfaces.GrantedCredentialDTO, error) {
	s.grantedInput.projectID, s.grantedInput.params = projectID, params
	return []pro_interfaces.GrantedCredentialDTO{{CredentialID: 7, Type: db.GlobalCredentialTypeString, DisplayName: "Deploy", Version: 2, Operations: db.GlobalCredentialGrantOperationReference, GrantID: 9, GrantRevision: 3}}, s.err
}

func (s *globalCredentialFacadeStub) ListGlobalCredentialGrantProjects(context.Context) ([]pro_interfaces.GlobalCredentialGrantProjectDTO, error) {
	return []pro_interfaces.GlobalCredentialGrantProjectDTO{{ID: 7, Name: "Project seven"}}, s.err
}

func globalCredentialSummary() pro_interfaces.GlobalCredentialSummaryDTO {
	return pro_interfaces.GlobalCredentialSummaryDTO{ID: 7, Type: db.GlobalCredentialTypeString, DisplayName: "Deploy", Enabled: true, Revision: 2, CurrentVersion: 3, Fingerprint: strings.Repeat("a", 64), MaterialKind: db.GlobalCredentialMaterialLocalEncrypted}
}

func globalCredentialGrant(status db.GlobalCredentialGrantStatus) pro_interfaces.GlobalCredentialGrantDTO {
	return pro_interfaces.GlobalCredentialGrantDTO{ID: 9, CredentialID: 7, ProjectID: 4, Operations: db.GlobalCredentialGrantOperationReference, Status: status, Revision: 3, Created: time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC), Updated: time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)}
}

func globalCredentialRequest(method, target, body string, vars map[string]string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 42})
	return mux.SetURLVars(request, vars)
}

func TestGlobalCredentialListRejectsAmbiguousOrUnknownPagination(t *testing.T) {
	controller := NewGlobalCredentialController(nil)
	for _, target := range []string{
		"/global-credentials?count=10&count=11",
		"/global-credentials?count=10&ignored=1",
		"/global-credentials?offset=-1",
		"/global-credentials?count=101",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		controller.List(response, request)
		assert.Equalf(t, http.StatusBadRequest, response.Code, "target %s", target)
	}
}

func TestGlobalCredentialGrantProjectListReturnsOnlySelectorFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/global-credentials/grant-projects", nil)
	response := httptest.NewRecorder()
	NewGlobalCredentialController(&globalCredentialFacadeStub{}).ListGrantProjects(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var result []map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "Project seven", result[0]["name"])
	assert.Len(t, result[0], 2)
}

func TestGlobalCredentialUsageRoutesUseBoundedValueFreeFilters(t *testing.T) {
	service := &globalCredentialFacadeStub{}
	controller := NewGlobalCredentialController(service)
	response := httptest.NewRecorder()
	request := globalCredentialRequest(http.MethodGet,
		"/global-credentials/7/usage?count=10&before_id=20&project_id=4&task_id=8&outcome=denied", "",
		map[string]string{"credential_id": "7"})
	controller.ListUsage(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.NotNil(t, service.usageInput.query.Outcome)
	assert.Equal(t, pro_interfaces.GlobalCredentialResolutionDenied, *service.usageInput.query.Outcome)
	assert.Equal(t, 7, service.usageInput.credentialID)
	assert.Equal(t, 10, service.usageInput.query.Count)
	assert.Equal(t, 20, service.usageInput.query.BeforeID)
	require.NotNil(t, service.usageInput.query.ProjectID)
	require.NotNil(t, service.usageInput.query.TaskID)
	assert.Equal(t, 4, *service.usageInput.query.ProjectID)
	assert.Equal(t, 8, *service.usageInput.query.TaskID)

	response = httptest.NewRecorder()
	request = globalCredentialRequest(http.MethodGet,
		"/project/4/tasks/8/credential-usage?count=5&outcome=allowed", "",
		map[string]string{"project_id": "4", "task_id": "8"})
	controller.ListTaskUsage(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 4, service.usageInput.projectID)
	assert.Equal(t, 8, service.usageInput.taskID)
	assert.Nil(t, service.usageInput.query.ProjectID)
	assert.Nil(t, service.usageInput.query.TaskID)
}

func TestGlobalCredentialUsageRoutesRejectAmbiguousAndUnknownFilters(t *testing.T) {
	controller := NewGlobalCredentialController(&globalCredentialFacadeStub{})
	for _, target := range []string{
		"/global-credentials/7/usage?outcome=unknown",
		"/global-credentials/7/usage?count=10&count=11",
		"/global-credentials/7/usage?offset=1",
		"/project/4/tasks/8/credential-usage?project_id=4",
	} {
		response := httptest.NewRecorder()
		request := globalCredentialRequest(http.MethodGet, target, "", map[string]string{
			"credential_id": "7", "project_id": "4", "task_id": "8",
		})
		if strings.Contains(target, "credential-usage") {
			controller.ListTaskUsage(response, request)
		} else {
			controller.ListUsage(response, request)
		}
		assert.Equal(t, http.StatusBadRequest, response.Code, target)
	}
}

func TestGlobalCredentialDeleteRejectsAmbiguousRevision(t *testing.T) {
	controller := NewGlobalCredentialController(nil)
	request := httptest.NewRequest(http.MethodDelete, "/global-credentials/7?expected_revision=1&expected_revision=2", nil)
	request = mux.SetURLVars(request, map[string]string{"credential_id": "7"})
	response := httptest.NewRecorder()
	controller.Delete(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestGlobalCredentialControllerMutationsAreValueFree(t *testing.T) {
	secret := "write-only-secret"
	tests := []struct {
		name       string
		handler    func(*GlobalCredentialController, http.ResponseWriter, *http.Request)
		method     string
		target     string
		body       string
		vars       map[string]string
		wantStatus int
		wantBody   string
		assertCall func(*testing.T, *globalCredentialFacadeStub)
	}{
		{
			name: "create", handler: (*GlobalCredentialController).Create, method: http.MethodPost, target: "/global-credentials",
			body: `{"type":"string","display_name":"Deploy","material":{"string_value":"` + secret + `"}}`, wantStatus: http.StatusCreated,
			wantBody: `{"id":7,"type":"string","display_name":"Deploy","enabled":true,"revision":2,"current_version":3,"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","material_kind":"local_encrypted"}`,
			assertCall: func(t *testing.T, service *globalCredentialFacadeStub) {
				assert.Equal(t, 42, service.actorID)
				assert.Equal(t, db.GlobalCredentialTypeString, service.createInput.Type)
				require.NotNil(t, service.createInput.Material)
				require.NotNil(t, service.createInput.Material.StringValue)
				assert.Equal(t, secret, *service.createInput.Material.StringValue)
			},
		},
		{
			name: "rotate", handler: (*GlobalCredentialController).Rotate, method: http.MethodPost, target: "/global-credentials/7/rotate",
			body: `{"revision":2,"material":{"string_value":"` + secret + `"}}`, vars: map[string]string{"credential_id": "7"}, wantStatus: http.StatusOK,
			wantBody: `{"id":7,"type":"string","display_name":"Deploy","enabled":true,"revision":2,"current_version":3,"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","material_kind":"local_encrypted"}`,
			assertCall: func(t *testing.T, service *globalCredentialFacadeStub) {
				assert.Equal(t, 42, service.actorID)
				assert.Equal(t, 7, service.credentialID)
				assert.Equal(t, 2, service.revision)
				require.NotNil(t, service.rotateInput.StringValue)
				assert.Equal(t, secret, *service.rotateInput.StringValue)
			},
		},
		{
			name: "create grant", handler: (*GlobalCredentialController).CreateGrant, method: http.MethodPost, target: "/global-credentials/7/grants",
			body: `{"project_id":4,"operations":1}`, vars: map[string]string{"credential_id": "7"}, wantStatus: http.StatusCreated,
			wantBody: `{"id":9,"credential_id":7,"project_id":4,"operations":1,"status":"active","revision":3,"created":"2026-09-02T10:00:00Z","updated":"2026-09-02T10:00:00Z"}`,
			assertCall: func(t *testing.T, service *globalCredentialFacadeStub) {
				assert.Equal(t, 42, service.actorID)
				assert.Equal(t, 7, service.credentialID)
				assert.Equal(t, 4, service.grantInput.ProjectID)
			},
		},
		{
			name: "revoke grant", handler: (*GlobalCredentialController).RevokeGrant, method: http.MethodPost, target: "/global-credentials/7/grants/9/revoke",
			body: `{"revision":3}`, vars: map[string]string{"credential_id": "7", "grant_id": "9"}, wantStatus: http.StatusOK,
			wantBody: `{"id":9,"credential_id":7,"project_id":4,"operations":1,"status":"revoked","revision":3,"created":"2026-09-02T10:00:00Z","updated":"2026-09-02T10:00:00Z"}`,
			assertCall: func(t *testing.T, service *globalCredentialFacadeStub) {
				assert.Equal(t, 42, service.actorID)
				assert.Equal(t, 7, service.credentialID)
				assert.Equal(t, 9, service.grantID)
				assert.Equal(t, 3, service.revision)
				assert.Equal(t, db.GlobalCredentialGrantStatusRevoked, service.grantStatus)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &globalCredentialFacadeStub{}
			controller := NewGlobalCredentialController(service)
			response := httptest.NewRecorder()
			tt.handler(controller, response, globalCredentialRequest(tt.method, tt.target, tt.body, tt.vars))
			require.Equal(t, tt.wantStatus, response.Code, response.Body.String())
			assert.JSONEq(t, tt.wantBody, response.Body.String())
			tt.assertCall(t, service)
			assert.NotContains(t, response.Body.String(), secret)
			assert.NotContains(t, response.Body.String(), "string_value")
			assert.NotContains(t, response.Body.String(), "encrypted_material")
		})
	}
}

func TestGlobalCredentialControllerGrantedListIsValueFree(t *testing.T) {
	service := &globalCredentialFacadeStub{}
	controller := NewGlobalCredentialController(service)
	response := httptest.NewRecorder()
	request := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/projects/4/granted-credentials?count=10&offset=2", nil), map[string]string{"project_id": "4"})
	controller.ListGranted(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, 4, service.grantedInput.projectID)
	assert.Equal(t, db.RetrieveQueryParams{Count: 10, Offset: 2}, service.grantedInput.params)
	assert.JSONEq(t, `[{"credential_id":7,"type":"string","display_name":"Deploy","version":2,"operations":1,"grant_id":9,"grant_revision":3}]`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "owner_user_id")
	assert.NotContains(t, response.Body.String(), "fingerprint")
	assert.NotContains(t, response.Body.String(), "external_reference")
	assert.NotContains(t, response.Body.String(), "encrypted_material")
}

func TestGlobalCredentialControllerRejectsUnknownAndOversizedBodiesBeforeFacade(t *testing.T) {
	service := &globalCredentialFacadeStub{}
	controller := NewGlobalCredentialController(service)
	for name, body := range map[string]string{
		"unknown field":  `{"type":"string","display_name":"Deploy","material":{"string_value":"value"},"unexpected":true}`,
		"oversized body": `{"type":"string","display_name":"` + strings.Repeat("a", globalCredentialBodyLimit) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			controller.Create(response, globalCredentialRequest(http.MethodPost, "/global-credentials", body, nil))
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			assert.JSONEq(t, `{"error":"Invalid global credential input"}`, response.Body.String())
			assert.Equal(t, 0, service.actorID, "facade must not run after a decode rejection")
		})
	}
}

func TestGlobalCredentialControllerConflictMessagesAreDistinctAndValueFree(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    string
		handler func(*GlobalCredentialController, http.ResponseWriter, *http.Request)
		method  string
		target  string
		body    string
		vars    map[string]string
	}{
		{name: "revision", err: pro_interfaces.ErrGlobalCredentialRevisionConflict, want: "Global credential revision conflict", handler: (*GlobalCredentialController).Rotate, method: http.MethodPost, target: "/global-credentials/7/rotate", body: `{"revision":2,"material":{"string_value":"secret"}}`, vars: map[string]string{"credential_id": "7"}},
		{name: "duplicate grant", err: pro_interfaces.ErrGlobalCredentialGrantExists, want: "Global credential grant already exists", handler: (*GlobalCredentialController).CreateGrant, method: http.MethodPost, target: "/global-credentials/7/grants", body: `{"project_id":4,"operations":1}`, vars: map[string]string{"credential_id": "7"}},
		{name: "dependency", err: pro_interfaces.ErrGlobalCredentialDependencyConflict, want: "Global credential dependency conflict", handler: (*GlobalCredentialController).Delete, method: http.MethodDelete, target: "/global-credentials/7?expected_revision=2", vars: map[string]string{"credential_id": "7"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &globalCredentialFacadeStub{err: tt.err}
			response := httptest.NewRecorder()
			tt.handler(NewGlobalCredentialController(service), response, globalCredentialRequest(tt.method, tt.target, tt.body, tt.vars))
			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			assert.JSONEq(t, `{"error":"`+tt.want+`"}`, response.Body.String())
			assert.NotContains(t, response.Body.String(), "secret")
		})
	}
}

var _ pro_interfaces.GlobalCredentialServiceFacade = (*globalCredentialFacadeStub)(nil)
