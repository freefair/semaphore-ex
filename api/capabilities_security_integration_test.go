package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	auditservice "github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type integrationAuditWriter struct{ record pro_interfaces.EventLogRecord }

func (w *integrationAuditWriter) WriteEventLog(record pro_interfaces.EventLogRecord) error {
	w.record = record
	return nil
}
func (*integrationAuditWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error { return nil }
func (*integrationAuditWriter) WriteResult(any) error                           { return nil }

func TestDeniedCapabilityActionPersistsRedactedAuditAndMetrics(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "audit-user", Name: "Audit User", Email: "audit@example.com",
	})
	require.NoError(t, err)
	writer := &integrationAuditWriter{}
	appMetrics := metrics.NewMetrics()
	auditFacade := auditservice.NewServiceFacade(store, writer, appMetrics)
	facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateDisabled,
		pro_interfaces.CapabilityReasonDisabledByAdmin,
		nil,
		nil,
	)}
	controller := NewCapabilityController(facade, auditFacade)
	handler := helpers.CorrelationMiddleware(controller.SnapshotMiddleware(
		controller.Require(pro_interfaces.CapabilityAccessWrite)(http.HandlerFunc(controller.CreateRecord)),
	))
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/lifecycle-test/records",
		bytes.NewBufferString(`{"value":"`+securityfixtures.TripwireValues[0]+`"}`))
	request = helpers.SetContextValue(request, "user", &user)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Description)
	filePayload, err := json.Marshal(writer.record)
	require.NoError(t, err)
	metricsResponse := httptest.NewRecorder()
	appMetrics.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	securityfixtures.AssertTripwiresAbsent(t,
		response.Body.String(), *events[0].Description, string(filePayload), metricsResponse.Body.String())
	assert.Contains(t, *events[0].Description, `"outcome":"denied"`)
	assert.Contains(t, metricsResponse.Body.String(),
		`semaphore_enhanced_actions_total{action="capability_write",outcome="denied",source="api"} 1`)
}

func TestDeniedWorkflowRoutesPersistBoundedAuditEvents(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "workflow audit"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "workflow-audit", Name: "Workflow Audit", Email: "workflow-audit@example.test"})
	require.NoError(t, err)
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: project.ID, AccessPolicyRevision: 4}
	writer := &integrationAuditWriter{}
	face := auditservice.NewServiceFacade(store, writer, metrics.NewMetrics())
	for _, test := range []struct {
		method     string
		path       string
		vars       map[string]string
		status     int
		action     pro_interfaces.AuditAction
		targetType pro_interfaces.AuditTargetType
		target     string
		task       bool
	}{
		{http.MethodGet, "/api/project/1/workflows", map[string]string{"project_id": "1"}, http.StatusForbidden, pro_interfaces.AuditActionWorkflowList, pro_interfaces.AuditTargetWorkflow, "project:1", false},
		{http.MethodGet, "/api/project/1/workflows/41", map[string]string{"project_id": "1", "workflow_id": "41"}, http.StatusNotFound, pro_interfaces.AuditActionWorkflowRead, pro_interfaces.AuditTargetWorkflow, "workflow:41", false},
		{http.MethodPost, "/api/project/1/workflows/41/run", map[string]string{"project_id": "1", "workflow_id": "41"}, http.StatusForbidden, pro_interfaces.AuditActionWorkflowStart, pro_interfaces.AuditTargetWorkflow, "workflow:41", false},
		{http.MethodPost, "/api/project/1/workflows/41/runs/91/stop", map[string]string{"project_id": "1", "workflow_id": "41", "run_id": "91"}, http.StatusForbidden, pro_interfaces.AuditActionWorkflowStop, pro_interfaces.AuditTargetWorkflowRun, "run:91", false},
		{http.MethodDelete, "/api/project/1/workflows/41", map[string]string{"project_id": "1", "workflow_id": "41"}, http.StatusForbidden, pro_interfaces.AuditActionWorkflowDelete, pro_interfaces.AuditTargetWorkflow, "workflow:41", false},
		{http.MethodGet, "/api/project/1/workflow-approvals", map[string]string{"project_id": "1"}, http.StatusForbidden, pro_interfaces.AuditActionWorkflowApprovalInbox, pro_interfaces.AuditTargetWorkflowApprovalInbox, "project:1", false},
		{http.MethodGet, "/api/project/1/tasks/9/output", map[string]string{"project_id": "1", "task_id": "9"}, http.StatusNotFound, pro_interfaces.AuditActionWorkflowRunLogsRead, pro_interfaces.AuditTargetWorkflowRun, "run:91", true},
	} {
		t.Run(test.target, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path+"?secret=tripwire", nil)
			r = mux.SetURLVars(r, test.vars)
			r = helpers.SetContextValue(r, "store", store)
			r = helpers.SetContextValue(r, "project", project)
			r = helpers.SetContextValue(r, "user", &user)
			r = helpers.SetContextValue(r, "workflow", workflow)
			if test.task {
				runID := 91
				r = helpers.SetContextValue(r, "task", db.Task{ProjectID: project.ID, WorkflowRunID: &runID})
			}
			response := httptest.NewRecorder()
			EnhancedWorkflowDeniedAuditMiddleware(face)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(test.status) })).ServeHTTP(response, r)
			assert.Equal(t, test.status, response.Code)
		})
	}
	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 7)
	seen := make(map[pro_interfaces.AuditAction]pro_interfaces.AuditEvent, len(events))
	for _, event := range events {
		require.NotNil(t, event.Description)
		var payload pro_interfaces.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(*event.Description), &payload))
		assert.Equal(t, pro_interfaces.AuditOutcomeDenied, payload.Outcome)
		assert.Equal(t, pro_interfaces.AuditReasonWorkflowPolicyDenied, payload.Reason)
		assert.Equal(t, 4, payload.WorkflowPolicyRevision)
		assert.Equal(t, pro_interfaces.AuditSourceAPI, payload.Source)
		assert.NotContains(t, *event.Description, "tripwire")
		seen[payload.Action] = payload
	}
	for _, test := range []struct {
		action     pro_interfaces.AuditAction
		targetType pro_interfaces.AuditTargetType
		target     string
	}{
		{pro_interfaces.AuditActionWorkflowList, pro_interfaces.AuditTargetWorkflow, "project:1"}, {pro_interfaces.AuditActionWorkflowRead, pro_interfaces.AuditTargetWorkflow, "workflow:41"}, {pro_interfaces.AuditActionWorkflowStart, pro_interfaces.AuditTargetWorkflow, "workflow:41"}, {pro_interfaces.AuditActionWorkflowStop, pro_interfaces.AuditTargetWorkflowRun, "run:91"}, {pro_interfaces.AuditActionWorkflowDelete, pro_interfaces.AuditTargetWorkflow, "workflow:41"}, {pro_interfaces.AuditActionWorkflowApprovalInbox, pro_interfaces.AuditTargetWorkflowApprovalInbox, "project:1"}, {pro_interfaces.AuditActionWorkflowRunLogsRead, pro_interfaces.AuditTargetWorkflowRun, "run:91"},
	} {
		payload, ok := seen[test.action]
		require.True(t, ok)
		assert.Equal(t, test.targetType, payload.TargetType)
		assert.Equal(t, test.target, payload.TargetID)
	}
}

func TestAnonymousMissingProjectDenialIsRetainedWithoutUserFeedExposure(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "unrelated-audit-user", Name: "Unrelated Audit User", Email: "unrelated-audit@example.invalid",
	})
	require.NoError(t, err)
	auditFacade := auditservice.NewServiceFacade(store, &integrationAuditWriter{}, metrics.NewMetrics())
	handler := EnhancedAnonymousAuditMiddleware(auditFacade)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/project/999999/runners", nil)
	request = mux.SetURLVars(request, map[string]string{"project_id": "999999"})
	request = helpers.SetContextValue(request, "store", store)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Nil(t, events[0].ProjectID)
	require.NotNil(t, events[0].ObjectType)
	assert.Equal(t, db.EventProjectRunnerAudit, *events[0].ObjectType)
	userEvents, err := store.GetUserEvents(user.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, userEvents)
}

func TestGlobalAndTemplateRoleMutationAuditPersistsWithoutRequestPayload(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "role-audit-persistence", Name: "Role audit persistence",
		Email: "role-audit-persistence@example.test",
	})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Role audit persistence project"})
	require.NoError(t, err)
	auditFacade := auditservice.NewServiceFacade(store, &integrationAuditWriter{}, metrics.NewMetrics())

	globalHandler := helpers.CorrelationMiddleware(
		EnhancedGlobalPermissionAuditMiddleware(auditFacade)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) },
		)),
	)
	globalRequest := httptest.NewRequest(
		http.MethodPost, "/api/roles", bytes.NewBufferString(securityfixtures.TripwireValues[0]),
	)
	globalRequest = helpers.SetContextValue(globalRequest, "user", &user)
	globalResponse := httptest.NewRecorder()
	globalHandler.ServeHTTP(globalResponse, globalRequest)
	assert.Equal(t, http.StatusCreated, globalResponse.Code)

	templateHandler := helpers.CorrelationMiddleware(
		EnhancedProjectPermissionAuditMiddleware(auditFacade)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) },
		)),
	)
	templateRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/project/1/templates/2/perms",
		bytes.NewBufferString(securityfixtures.TripwireValues[0]),
	)
	templateRequest = mux.SetURLVars(templateRequest, map[string]string{
		"project_id": strconv.Itoa(project.ID), "template_id": "2",
	})
	templateRequest = helpers.SetContextValue(templateRequest, "user", &user)
	templateRequest = helpers.SetContextValue(
		templateRequest, "permissions", db.CanManageProjectResources,
	)
	templateRequest = helpers.SetContextValue(templateRequest, "project", project)
	templateResponse := httptest.NewRecorder()
	templateHandler.ServeHTTP(templateResponse, templateRequest)
	assert.Equal(t, http.StatusCreated, templateResponse.Code)

	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	payloads := make([]string, 0, len(events))
	for _, event := range events {
		require.NotNil(t, event.Description)
		payloads = append(payloads, *event.Description)
	}
	joined := strings.Join(payloads, "\n")
	assert.Contains(t, joined, string(pro_interfaces.AuditActionGlobalRoleCreate))
	assert.Contains(t, joined, string(pro_interfaces.AuditActionTemplateRoleCreate))
	securityfixtures.AssertTripwiresAbsent(t, joined)
}
