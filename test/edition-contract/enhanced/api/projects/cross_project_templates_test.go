package projects

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const crossProjectTemplateTestFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type crossProjectTemplateAuditRecorder struct {
	mu     sync.Mutex
	events []pro_interfaces.AuditEvent
}

func (r *crossProjectTemplateAuditRecorder) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *crossProjectTemplateAuditRecorder) all() []pro_interfaces.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pro_interfaces.AuditEvent(nil), r.events...)
}

func TestCrossIDAbortsMalformedRouteParameterAndRejectsNonpositiveID(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value string
	}{
		{name: "malformed", value: "not-an-id"},
		{name: "nonpositive", value: "0"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/project/1/templates/1", nil)
			request.Header.Set("Accept", "application/json")
			request = mux.SetURLVars(request, map[string]string{"template_id": testCase.value})
			response := httptest.NewRecorder()

			id, ok := crossID(response, request, "template_id")

			assert.False(t, ok)
			assert.Zero(t, id)
			assert.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
}

type crossProjectTemplateServiceStub struct {
	version db.TemplateVersion
	grant   db.CrossProjectTemplateGrant
	refs    []pro_interfaces.CrossProjectTemplateReferenceView

	mu       sync.Mutex
	calls    []string
	params   []db.RetrieveQueryParams
	writeErr error
}

func (s *crossProjectTemplateServiceStub) call(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, name)
}

func (s *crossProjectTemplateServiceStub) recordParams(name string, params db.RetrieveQueryParams) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, name)
	s.params = append(s.params, params)
}

func (s *crossProjectTemplateServiceStub) callSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *crossProjectTemplateServiceStub) paramsSnapshot() []db.RetrieveQueryParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]db.RetrieveQueryParams(nil), s.params...)
}

func (s *crossProjectTemplateServiceStub) PublishTemplateVersion(_ int, _ int, _ *db.User) (db.TemplateVersion, bool, error) {
	s.call("publish")
	return s.version, false, s.writeErr
}

func (s *crossProjectTemplateServiceStub) ListTemplateVersions(_ int, _ int, params db.RetrieveQueryParams, _ *db.User) ([]db.TemplateVersion, error) {
	s.recordParams("versions", params)
	return []db.TemplateVersion{s.version}, nil
}

func (s *crossProjectTemplateServiceStub) CreateGrant(_ int, _ int, _ pro_interfaces.CrossProjectTemplateGrantCreate, _ *db.User) (db.CrossProjectTemplateGrant, error) {
	s.call("create")
	return s.grant, s.writeErr
}

func (s *crossProjectTemplateServiceStub) UpdateGrant(_ int, _ int, _ pro_interfaces.CrossProjectTemplateGrantUpdate, _ *db.User) (db.CrossProjectTemplateGrant, error) {
	s.call("update")
	return s.grant, s.writeErr
}

func (s *crossProjectTemplateServiceStub) DeleteGrant(_ int, _ int, _ int, _ *db.User) (db.CrossProjectTemplateGrant, error) {
	s.call("delete")
	return s.grant, s.writeErr
}

func (s *crossProjectTemplateServiceStub) AcceptGrant(_ int, _ int, _ int, _ *db.User) (db.CrossProjectTemplateGrant, error) {
	s.call("accept")
	return s.grant, s.writeErr
}

func (s *crossProjectTemplateServiceStub) RevokeGrant(_ int, _ int, _ int, _ string, _ *db.User) (db.CrossProjectTemplateGrant, error) {
	s.call("revoke")
	return s.grant, s.writeErr
}

func (s *crossProjectTemplateServiceStub) ListGrants(_ int, params db.RetrieveQueryParams, _ *db.User) ([]db.CrossProjectTemplateGrant, error) {
	s.recordParams("grants", params)
	return []db.CrossProjectTemplateGrant{s.grant}, nil
}

func (s *crossProjectTemplateServiceStub) ListReferences(_ int, _ int, params db.RetrieveQueryParams, _ *db.User) ([]pro_interfaces.CrossProjectTemplateReferenceView, db.CrossProjectTemplateGrant, error) {
	s.recordParams("references", params)
	return s.refs, s.grant, nil
}

var _ pro_interfaces.CrossProjectTemplateService = (*crossProjectTemplateServiceStub)(nil)

func newCrossProjectTemplateServiceStub() *crossProjectTemplateServiceStub {
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	return &crossProjectTemplateServiceStub{
		version: db.TemplateVersion{
			ID: 51, OwnerProjectID: 1, TemplateID: 3, VersionNumber: 1,
			AuthorUserID: 91, ContentFingerprint: crossProjectTemplateTestFingerprint,
			SnapshotJSON: `{"execution":{"survey_vars":[{"name":"do-not-expose"}]},"dependencies":{"repository_id":81,"environment_ids":[82],"vaults":[{"id":83}]}}`,
			Created:      now,
		},
		grant: db.CrossProjectTemplateGrant{
			ID: 4, OwnerProjectID: 1, ConsumerProjectID: 2, TemplateID: 3,
			MinTemplateVersion: 1, MaxTemplateVersion: 1,
			Operations: db.CrossProjectTemplateGrantReference | db.CrossProjectTemplateGrantRun,
			Status:     db.CrossProjectTemplateGrantActive, Revision: 2, Reason: "approved scope",
			CreatedByUserID: 91, Created: now, AcceptedByUserID: 92, RevokedByUserID: 93,
		},
		refs: []pro_interfaces.CrossProjectTemplateReferenceView{{
			GrantID: 4, GrantRevision: 2, OwnerProjectID: 1, TemplateID: 3,
			TemplateVersionID: 51, TemplateVersionNumber: 1, ContentFingerprint: crossProjectTemplateTestFingerprint,
			Name: "Deploy", Type: db.TemplateDeploy,
		}},
	}
}

func TestCrossProjectTemplateControllerRejectsStrictBodiesAndBounds(t *testing.T) {
	service := newCrossProjectTemplateServiceStub()
	controller := NewCrossProjectTemplateController(service)

	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		method  string
		target  string
		vars    map[string]string
		body    string
	}{
		{"case variant", controller.CreateGrant, http.MethodPost, "/templates/3/cross-project-grants", map[string]string{"template_id": "3"}, `{"CONSUMER_PROJECT_ID":2,"min_version":1,"max_version":1,"operations":1}`},
		{"unknown field", controller.CreateGrant, http.MethodPost, "/templates/3/cross-project-grants", map[string]string{"template_id": "3"}, `{"consumer_project_id":2,"min_version":1,"max_version":1,"operations":1,"unknown":true}`},
		{"trailing body", controller.CreateGrant, http.MethodPost, "/templates/3/cross-project-grants", map[string]string{"template_id": "3"}, `{"consumer_project_id":2,"min_version":1,"max_version":1,"operations":1} {}`},
		{"oversized body", controller.CreateGrant, http.MethodPost, "/templates/3/cross-project-grants", map[string]string{"template_id": "3"}, `{"consumer_project_id":2,"min_version":1,"max_version":1,"operations":1,"reason":"` + strings.Repeat("x", int(crossProjectTemplateBodyLimit)) + `"}`},
		{"create bounds", controller.CreateGrant, http.MethodPost, "/templates/3/cross-project-grants", map[string]string{"template_id": "3"}, `{"consumer_project_id":0,"min_version":0,"max_version":1,"operations":1}`},
		{"update bounds", controller.UpdateGrant, http.MethodPut, "/grants/4", map[string]string{"grant_id": "4"}, `{"min_version":1,"max_version":1,"operations":1,"expected_revision":0}`},
		{"accept bounds", controller.AcceptGrant, http.MethodPost, "/grants/4/accept", map[string]string{"grant_id": "4"}, `{"expected_revision":0}`},
		{"revoke bounds", controller.RevokeGrant, http.MethodPost, "/grants/4/revoke", map[string]string{"grant_id": "4"}, `{"expected_revision":1,"reason":" "}`},
		{"nonpositive route id", controller.ListTemplateVersions, http.MethodGet, "/templates/0/versions", map[string]string{"template_id": "0"}, ""},
		{"malformed delete revision", controller.DeleteGrant, http.MethodDelete, "/grants/4?expected_revision=abc", map[string]string{"grant_id": "4"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := crossProjectTemplateServe(t, tt.handler, crossProjectTemplateRequest(tt.method, tt.target, tt.vars, tt.body, 1))
			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
	assert.Empty(t, service.callSnapshot(), "invalid requests must not reach the service boundary")
}

func TestCrossProjectTemplateControllerUsesStrictKeysetPaginationAndSafeWireViews(t *testing.T) {
	service := newCrossProjectTemplateServiceStub()
	controller := NewCrossProjectTemplateController(service)

	for _, endpoint := range []struct {
		handler func(http.ResponseWriter, *http.Request)
		target  string
		vars    map[string]string
	}{
		{controller.ListTemplateVersions, "/templates/3/versions", map[string]string{"template_id": "3"}},
		{controller.ListGrants, "/grants", nil},
		{controller.ListReferences, "/grants/4/references", map[string]string{"grant_id": "4"}},
	} {
		recorder := crossProjectTemplateServe(t, endpoint.handler, crossProjectTemplateRequest(http.MethodGet, endpoint.target, endpoint.vars, "", 2))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		for _, forbidden := range []string{"execution_snapshot", "snapshot", "dependencies", "survey_vars", "vault", "environment_ids", "repository_id", "created_by_user_id", "accepted_by_user_id", "revoked_by_user_id", "author_user_id"} {
			assert.NotContains(t, recorder.Body.String(), forbidden)
		}
	}

	params := service.paramsSnapshot()
	require.Len(t, params, 3)
	for _, value := range params {
		assert.Equal(t, defaultCrossProjectTemplatePageSize, value.Count)
		assert.Zero(t, value.BeforeID)
	}

	recorder := crossProjectTemplateServe(t, controller.ListReferences, crossProjectTemplateRequest(http.MethodGet, "/grants/4/references?count=999&before=12", map[string]string{"grant_id": "4"}, "", 2))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	params = service.paramsSnapshot()
	require.Len(t, params, 4)
	assert.Equal(t, db.MaxCrossProjectTemplateGrantPageSize, params[3].Count)
	assert.Equal(t, 12, params[3].BeforeID)

	for _, target := range []string{
		"/grants?offset=1", "/grants?sort=id", "/grants?count=0", "/grants?before=0", "/grants?count=1&count=2",
	} {
		recorder = crossProjectTemplateServe(t, controller.ListGrants, crossProjectTemplateRequest(http.MethodGet, target, nil, "", 2))
		assert.Equal(t, http.StatusBadRequest, recorder.Code, target)
	}
	assert.Len(t, service.paramsSnapshot(), 4, "invalid pagination must not reach the service boundary")
}

func TestCrossProjectTemplateControllerMapsCASConflictAndRecordsEveryMutationAndResolution(t *testing.T) {
	service := newCrossProjectTemplateServiceStub()
	controller := NewCrossProjectTemplateController(service)
	audit := &crossProjectTemplateAuditRecorder{}
	controller.(pro_interfaces.CrossProjectTemplateAuditConfigurer).ConfigureCrossProjectTemplateAudit(audit)

	service.writeErr = db.ErrCrossProjectTemplateGrantRevisionConflict
	for _, conflict := range []struct {
		handler func(http.ResponseWriter, *http.Request)
		method  string
		target  string
		body    string
		project int
	}{
		{controller.UpdateGrant, http.MethodPut, "/grants/4", `{"min_version":1,"max_version":1,"operations":1,"expected_revision":1}`, 1},
		{controller.DeleteGrant, http.MethodDelete, "/grants/4?expected_revision=1", "", 1},
		{controller.AcceptGrant, http.MethodPost, "/grants/4/accept", `{"expected_revision":1}`, 2},
		{controller.RevokeGrant, http.MethodPost, "/grants/4/revoke", `{"expected_revision":1,"reason":"withdrawn"}`, 1},
	} {
		recorder := crossProjectTemplateServe(t, conflict.handler, crossProjectTemplateRequest(conflict.method, conflict.target, map[string]string{"grant_id": "4"}, conflict.body, conflict.project))
		assert.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
	}
	assert.Empty(t, audit.all(), "failed mutations must not produce allowed audit events")
	service.writeErr = nil

	requests := []struct {
		handler func(http.ResponseWriter, *http.Request)
		method  string
		target  string
		vars    map[string]string
		body    string
		project int
		status  int
	}{
		{controller.PublishTemplateVersion, http.MethodPost, "/templates/3/versions", map[string]string{"template_id": "3"}, "", 1, http.StatusCreated},
		{controller.CreateGrant, http.MethodPost, "/templates/3/cross-project-grants", map[string]string{"template_id": "3"}, `{"consumer_project_id":2,"min_version":1,"max_version":1,"operations":3,"reason":"approved scope"}`, 1, http.StatusCreated},
		{controller.UpdateGrant, http.MethodPut, "/grants/4", map[string]string{"grant_id": "4"}, `{"min_version":1,"max_version":1,"operations":3,"reason":"approved scope","expected_revision":1}`, 1, http.StatusOK},
		{controller.AcceptGrant, http.MethodPost, "/grants/4/accept", map[string]string{"grant_id": "4"}, `{"expected_revision":1}`, 2, http.StatusOK},
		{controller.RevokeGrant, http.MethodPost, "/grants/4/revoke", map[string]string{"grant_id": "4"}, `{"expected_revision":1,"reason":"withdrawn"}`, 1, http.StatusOK},
		{controller.DeleteGrant, http.MethodDelete, "/grants/4?expected_revision=1", map[string]string{"grant_id": "4"}, "", 1, http.StatusNoContent},
		{controller.ListReferences, http.MethodGet, "/grants/4/references", map[string]string{"grant_id": "4"}, "", 2, http.StatusOK},
	}
	for _, request := range requests {
		recorder := crossProjectTemplateServe(t, request.handler, crossProjectTemplateRequest(request.method, request.target, request.vars, request.body, request.project))
		assert.Equal(t, request.status, recorder.Code, recorder.Body.String())
	}

	events := audit.all()
	require.Len(t, events, len(requests))
	assert.Equal(t, []pro_interfaces.AuditAction{
		pro_interfaces.AuditActionCrossProjectTemplateVersionPublish,
		pro_interfaces.AuditActionCrossProjectTemplateGrantCreate,
		pro_interfaces.AuditActionCrossProjectTemplateGrantUpdate,
		pro_interfaces.AuditActionCrossProjectTemplateGrantAccept,
		pro_interfaces.AuditActionCrossProjectTemplateGrantRevoke,
		pro_interfaces.AuditActionCrossProjectTemplateGrantDelete,
		pro_interfaces.AuditActionCrossProjectTemplateReferenceResolve,
	}, []pro_interfaces.AuditAction{events[0].Action, events[1].Action, events[2].Action, events[3].Action, events[4].Action, events[5].Action, events[6].Action})
	for _, event := range events {
		assert.NoError(t, event.Validate(), string(event.Action))
		payload := event.SafeFields()
		assert.NotContains(t, payload, "cross_project_template_provenance", "safe fields must not expose cross-project provenance beyond the audit store")
	}
}

func crossProjectTemplateServe(t *testing.T, handler func(http.ResponseWriter, *http.Request), request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	helpers.CorrelationMiddleware(http.HandlerFunc(handler)).ServeHTTP(recorder, request)
	return recorder
}

func crossProjectTemplateRequest(method, target string, variables map[string]string, body string, projectID int) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request = mux.SetURLVars(request, variables)
	request = helpers.SetContextValue(request, "project", db.Project{ID: projectID})
	return helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
}
