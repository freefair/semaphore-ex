package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditRecorderStub struct {
	events []pro_interfaces.AuditEvent
	err    error
}

func (r *auditRecorderStub) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	r.events = append(r.events, event)
	return r.err
}

func TestCapabilityAuthorizationAndAuditMatrix(t *testing.T) {
	tests := []struct {
		name        string
		user        *db.User
		projectRole bool
		decision    pro_interfaces.CapabilityDecision
		status      int
		outcome     pro_interfaces.AuditOutcome
		reason      string
	}{
		{
			name: "anonymous", status: http.StatusUnauthorized,
			outcome: pro_interfaces.AuditOutcomeDenied, reason: pro_interfaces.AuditReasonUnauthenticated,
		},
		{
			name: "authenticated user", user: &db.User{ID: 7}, status: http.StatusForbidden,
			decision: insufficientPermissionAuditDecision(),
			outcome:  pro_interfaces.AuditOutcomeDenied,
			reason:   string(pro_interfaces.CapabilityReasonInsufficientPermission),
		},
		{
			name: "project role", user: &db.User{ID: 8}, projectRole: true, status: http.StatusForbidden,
			decision: insufficientPermissionAuditDecision(),
			outcome:  pro_interfaces.AuditOutcomeDenied,
			reason:   string(pro_interfaces.CapabilityReasonInsufficientPermission),
		},
		{
			name: "administrator", user: &db.User{ID: 9, Admin: true}, status: http.StatusCreated,
			decision: pro_interfaces.NewCapabilityDecision(
				pro_interfaces.CapabilityLifecycleTest,
				pro_interfaces.CapabilityStateActive,
				pro_interfaces.CapabilityReasonActive,
				[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessWrite},
				nil,
			),
			outcome: pro_interfaces.AuditOutcomeAllowed,
			reason:  string(pro_interfaces.CapabilityReasonActive),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auditRecorder := &auditRecorderStub{}
			var handler http.Handler
			if tt.user == nil {
				handler = EnhancedAnonymousAuditMiddleware(auditRecorder)(http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
				))
			} else {
				facade := &capabilityFacadeStub{decision: tt.decision}
				controller := NewCapabilityController(facade, auditRecorder)
				handler = controller.SnapshotMiddleware(
					controller.Require(pro_interfaces.CapabilityAccessWrite)(
						http.HandlerFunc(controller.CreateRecord),
					),
				)
			}
			handler = helpers.CorrelationMiddleware(handler)
			request := httptest.NewRequest(http.MethodPost, "/api/capabilities/lifecycle-test/records",
				bytes.NewBufferString(`{"value":"`+securityfixtures.TripwireValues[0]+`"}`))
			if tt.user != nil {
				request = helpers.SetContextValue(request, "user", tt.user)
			}
			if tt.projectRole {
				request = helpers.SetContextValue(request, "projectUserRole", db.ProjectManager)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			assert.Equal(t, tt.status, recorder.Code, recorder.Body.String())
			require.Len(t, auditRecorder.events, 1)
			assert.Equal(t, tt.outcome, auditRecorder.events[0].Outcome)
			assert.Equal(t, tt.reason, auditRecorder.events[0].Reason)
			assert.Equal(t, recorder.Header().Get(helpers.CorrelationHeader), auditRecorder.events[0].CorrelationID)
			if tt.status != http.StatusCreated {
				securityfixtures.AssertTripwiresAbsent(t, recorder.Body.String())
			}
		})
	}
}

func TestOptionalDependencyFailureDoesNotFailPing(t *testing.T) {
	appMetrics := metrics.NewMetrics()
	appMetrics.ObserveDependency(pro_interfaces.DependencyAuditFile, 0, false)
	recorder := httptest.NewRecorder()

	pongHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/ping", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "pong", recorder.Body.String())
}

func TestCapabilityConfigurationAdminDenialIsAudited(t *testing.T) {
	auditRecorder := &auditRecorderStub{}
	handler := helpers.CorrelationMiddleware(
		EnhancedAdminAuditMiddleware(auditRecorder)(
			adminMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("non-administrator must not reach configuration handler")
			})),
		),
	)
	request := httptest.NewRequest(http.MethodPut, "/api/capabilities/lifecycle-test",
		bytes.NewBufferString(`{"state":"active"}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 10})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	require.Len(t, auditRecorder.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionCapabilityConfigure, auditRecorder.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, auditRecorder.events[0].Outcome)
	assert.Equal(t, string(pro_interfaces.CapabilityReasonInsufficientPermission), auditRecorder.events[0].Reason)
	require.NotNil(t, auditRecorder.events[0].ActorID)
	assert.Equal(t, 10, *auditRecorder.events[0].ActorID)
}

func TestProjectRunnerPermissionDenialIsAudited(t *testing.T) {
	auditRecorder := &auditRecorderStub{}
	handler := helpers.CorrelationMiddleware(
		EnhancedProjectPermissionAuditMiddleware(auditRecorder)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		)),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/project/12/runners", bytes.NewBufferString(`{"name":"denied"}`))
	request = mux.SetURLVars(request, map[string]string{"project_id": "12"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 11})
	request = helpers.SetContextValue(request, "permissions", db.ProjectUserPermission(0))
	request = helpers.SetContextValue(request, "project", db.Project{ID: 12})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	require.Len(t, auditRecorder.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionProjectRunnerCreate, auditRecorder.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditTargetProjectRunner, auditRecorder.events[0].TargetType)
	assert.Equal(t, "project:12", auditRecorder.events[0].TargetID)
	require.NotNil(t, auditRecorder.events[0].ProjectID)
	assert.Equal(t, 12, *auditRecorder.events[0].ProjectID)
	assert.Equal(t, string(pro_interfaces.CapabilityReasonInsufficientPermission), auditRecorder.events[0].Reason)
	require.NoError(t, auditRecorder.events[0].Validate())
}

func TestProjectRunnerPermissionAuditDoesNotDuplicateDownstreamCapabilityDenial(t *testing.T) {
	auditRecorder := &auditRecorderStub{}
	handler := helpers.CorrelationMiddleware(
		EnhancedProjectPermissionAuditMiddleware(auditRecorder)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		)),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/project/12/runners", bytes.NewBufferString(`{"name":"capability-denied"}`))
	request = mux.SetURLVars(request, map[string]string{"project_id": "12"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 11})
	request = helpers.SetContextValue(request, "permissions", db.CanManageProjectResources)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Empty(t, auditRecorder.events)
}

func TestProjectRunnerLifecycleRoutesUseSpecificAuditActions(t *testing.T) {
	tests := []struct {
		method string
		path   string
		action pro_interfaces.AuditAction
	}{
		{http.MethodPut, "/api/project/12/runners/34", pro_interfaces.AuditActionProjectRunnerUpdate},
		{http.MethodPost, "/api/project/12/runners/34/active", pro_interfaces.AuditActionProjectRunnerActive},
		{http.MethodPost, "/api/project/12/runners/34/registration-token", pro_interfaces.AuditActionProjectRunnerIssue},
		{http.MethodDelete, "/api/project/12/runners/34/cache", pro_interfaces.AuditActionProjectRunnerCache},
		{http.MethodDelete, "/api/project/12/runners/34", pro_interfaces.AuditActionProjectRunnerDelete},
	}
	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request = mux.SetURLVars(request, map[string]string{"project_id": "12", "runner_id": "34"})

			descriptor, enhanced := enhancedAuditForRoute(request)

			require.True(t, enhanced)
			assert.Equal(t, test.action, descriptor.Action)
			assert.Equal(t, pro_interfaces.AuditTargetProjectRunner, descriptor.TargetType)
			assert.Equal(t, "runner:34", descriptor.TargetID)
			require.NotNil(t, descriptor.ProjectID)
			assert.Equal(t, 12, *descriptor.ProjectID)
		})
	}
}

func TestAnonymousProjectRunnerRequestIsAudited(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "anonymous-audit"})
	require.NoError(t, err)
	auditRecorder := &auditRecorderStub{}
	handler := helpers.CorrelationMiddleware(
		EnhancedAnonymousAuditMiddleware(auditRecorder)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		)),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/project/1/runners", nil)
	request = mux.SetURLVars(request, map[string]string{"project_id": strconv.Itoa(project.ID)})
	request = helpers.SetContextValue(request, "store", store)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Len(t, auditRecorder.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionProjectRunnerList, auditRecorder.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditReasonUnauthenticated, auditRecorder.events[0].Reason)
	require.NotNil(t, auditRecorder.events[0].ProjectID)
	assert.Equal(t, project.ID, *auditRecorder.events[0].ProjectID)
	require.NoError(t, auditRecorder.events[0].Validate())
}

func insufficientPermissionAuditDecision() pro_interfaces.CapabilityDecision {
	return pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateInsufficientPermission,
		pro_interfaces.CapabilityReasonInsufficientPermission,
		nil,
		nil,
	)
}
