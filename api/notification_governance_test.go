package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type notificationGovernanceStub struct {
	createDestinationInput pro_interfaces.NotificationDestinationInput
	createDestinationScope *int
	getDestinationScope    *int
	getDestinationID       int
	listParams             db.RetrieveQueryParams
	deleteDestinationScope *int
	deleteDestinationID    int
	deleteRevision         int
	deleteRuleScope        *int
	deleteRuleID           int
	retryScope             *int
	retryID                int
	history                []pro_interfaces.NotificationDeliveryDTO
	events                 []pro_interfaces.NotificationEventHistoryDTO
	err                    error
}

type notificationGovernanceLogWriter struct{}

func (notificationGovernanceLogWriter) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (notificationGovernanceLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (notificationGovernanceLogWriter) WriteResult(any) error                             { return nil }

func (s *notificationGovernanceStub) CreateDestination(_ context.Context, scope *int, input pro_interfaces.NotificationDestinationInput) (pro_interfaces.NotificationDestinationDTO, error) {
	s.createDestinationScope, s.createDestinationInput = scope, input
	return pro_interfaces.NotificationDestinationDTO{ID: 7, ProjectID: scope, Name: input.Name, Provider: input.Provider, CredentialConfigured: input.Credential != nil, Revision: 1}, s.err
}
func (s *notificationGovernanceStub) GetDestination(_ context.Context, scope *int, id int) (pro_interfaces.NotificationDestinationDTO, error) {
	s.getDestinationScope, s.getDestinationID = scope, id
	return pro_interfaces.NotificationDestinationDTO{ID: id, ProjectID: scope, CredentialConfigured: true}, s.err
}
func (s *notificationGovernanceStub) ListDestinations(_ context.Context, _ *int, params db.RetrieveQueryParams) ([]pro_interfaces.NotificationDestinationDTO, error) {
	s.listParams = params
	return []pro_interfaces.NotificationDestinationDTO{}, s.err
}
func (*notificationGovernanceStub) UpdateDestination(context.Context, *int, int, int, pro_interfaces.NotificationDestinationInput) (pro_interfaces.NotificationDestinationDTO, error) {
	return pro_interfaces.NotificationDestinationDTO{}, nil
}
func (s *notificationGovernanceStub) DeleteDestination(_ context.Context, scope *int, id, revision int) error {
	s.deleteDestinationScope, s.deleteDestinationID, s.deleteRevision = scope, id, revision
	return s.err
}
func (*notificationGovernanceStub) SetDestinationPaused(context.Context, *int, int, int, bool) (pro_interfaces.NotificationDestinationDTO, error) {
	return pro_interfaces.NotificationDestinationDTO{}, nil
}
func (*notificationGovernanceStub) CreateRule(context.Context, *int, pro_interfaces.NotificationRuleInput) (pro_interfaces.NotificationRuleDTO, error) {
	return pro_interfaces.NotificationRuleDTO{}, nil
}
func (*notificationGovernanceStub) ListRules(context.Context, *int, db.RetrieveQueryParams) ([]pro_interfaces.NotificationRuleDTO, error) {
	return []pro_interfaces.NotificationRuleDTO{}, nil
}
func (*notificationGovernanceStub) UpdateRule(context.Context, *int, int, int, pro_interfaces.NotificationRuleInput) (pro_interfaces.NotificationRuleDTO, error) {
	return pro_interfaces.NotificationRuleDTO{}, nil
}
func (s *notificationGovernanceStub) DeleteRule(_ context.Context, scope *int, id, revision int) error {
	s.deleteRuleScope, s.deleteRuleID, s.deleteRevision = scope, id, revision
	return s.err
}
func (*notificationGovernanceStub) PreviewRouting(context.Context, *int, pro_interfaces.NotificationEvent) ([]pro_interfaces.NotificationRoutingPreviewDTO, error) {
	return []pro_interfaces.NotificationRoutingPreviewDTO{}, nil
}
func (*notificationGovernanceStub) EnqueueTestDelivery(context.Context, *int, int) (pro_interfaces.NotificationDeliveryDTO, error) {
	return pro_interfaces.NotificationDeliveryDTO{}, nil
}
func (s *notificationGovernanceStub) DeliveryHistory(context.Context, *int, db.RetrieveQueryParams) ([]pro_interfaces.NotificationDeliveryDTO, error) {
	return s.history, s.err
}
func (s *notificationGovernanceStub) EventHistory(context.Context, *int, db.RetrieveQueryParams) ([]pro_interfaces.NotificationEventHistoryDTO, error) {
	return s.events, s.err
}
func (s *notificationGovernanceStub) RetryDelivery(_ context.Context, scope *int, id int) (pro_interfaces.NotificationDeliveryDTO, error) {
	s.retryScope, s.retryID = scope, id
	return pro_interfaces.NotificationDeliveryDTO{ID: id}, s.err
}

func TestNotificationGovernanceControllerStrictInputRedactsCredentialAndBoundsPagination(t *testing.T) {
	service := &notificationGovernanceStub{}
	controller := NewNotificationGovernanceController(service)
	credential := "controller-write-only-secret"
	request := httptest.NewRequest(http.MethodPost, "/api/notification-governance/destinations", strings.NewReader(`{"name":"primary","provider":"pagerduty","environment":"prod","region":"us","credential":"`+credential+`","enabled":true}`))
	response := httptest.NewRecorder()
	controller.CreateGlobalDestination(response, request)
	require.Equal(t, http.StatusCreated, response.Code)
	assert.Equal(t, credential, *service.createDestinationInput.Credential)
	assert.Equal(t, pro_interfaces.NotificationProviderRegionUS, service.createDestinationInput.Region)
	assert.Nil(t, service.createDestinationScope)
	assert.NotContains(t, response.Body.String(), credential)
	response = httptest.NewRecorder()
	controller.CreateGlobalDestination(response, httptest.NewRequest(http.MethodPost, "/api/notification-governance/destinations", strings.NewReader(`{"name":"ops","provider":"opsgenie","environment":"prod","region":"eu","credential":"`+credential+`","opsgenie":{"priority":"P2","responders":[{"type":"team","id":"team-1"}]},"enabled":true}`)))
	require.Equal(t, http.StatusCreated, response.Code)
	require.NotNil(t, service.createDestinationInput.Opsgenie)
	assert.Equal(t, pro_interfaces.NotificationOpsgeniePriorityP2, service.createDestinationInput.Opsgenie.Priority)
	require.Len(t, service.createDestinationInput.Opsgenie.Responders, 1)
	assert.Equal(t, pro_interfaces.NotificationProviderRegionEU, service.createDestinationInput.Region)
	assert.NotContains(t, response.Body.String(), credential)
	response = httptest.NewRecorder()
	controller.CreateGlobalDestination(response, httptest.NewRequest(http.MethodPost, "/api/notification-governance/destinations", strings.NewReader(`{"name":"snow","provider":"servicenow","environment":"prod","credential":"`+credential+`","servicenow":{"instance_origin":"https://example.service-now.com","auth_mode":"oauth_client_credentials","client_id":"semaphore","scope":"incident_read incident_write","field_mappings":[{"incident_field":"short_description","source_field":"summary"}]},"enabled":true}`)))
	require.Equal(t, http.StatusCreated, response.Code)
	require.NotNil(t, service.createDestinationInput.ServiceNow)
	assert.Equal(t, "https://example.service-now.com", service.createDestinationInput.ServiceNow.InstanceOrigin)
	assert.Equal(t, pro_interfaces.NotificationServiceNowAuthOAuthClientCredentials, service.createDestinationInput.ServiceNow.AuthMode)
	assert.Equal(t, "incident_read incident_write", service.createDestinationInput.ServiceNow.Scope)
	require.Len(t, service.createDestinationInput.ServiceNow.FieldMappings, 1)
	assert.Equal(t, pro_interfaces.NotificationServiceNowIncidentShortDescription, service.createDestinationInput.ServiceNow.FieldMappings[0].IncidentField)
	assert.NotContains(t, response.Body.String(), credential)
	service.err = pro_interfaces.ErrNotificationInvalidInput
	invalidCredential := "not-a-routing-key"
	response = httptest.NewRecorder()
	controller.CreateGlobalDestination(response, httptest.NewRequest(http.MethodPost, "/api/notification-governance/destinations", strings.NewReader(`{"name":"primary","provider":"pagerduty","environment":"prod","region":"us","credential":"`+invalidCredential+`","enabled":true}`)))
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.NotContains(t, response.Body.String(), invalidCredential)
	service.err = nil

	for _, body := range []string{
		`{"name":"primary","provider":"pagerduty","unknown":true}`,
		`{"name":"ops","provider":"opsgenie","region":"us","opsgenie":{"unknown":true}}`,
		`{"name":"snow","provider":"servicenow","servicenow":{"instance_origin":"https://example.service-now.com","unknown":true}}`,
		`{"name":"primary"} {}`,
	} {
		response = httptest.NewRecorder()
		controller.CreateGlobalDestination(response, httptest.NewRequest(http.MethodPost, "/api/notification-governance/destinations", strings.NewReader(body)))
		assert.Equal(t, http.StatusBadRequest, response.Code)
	}
	response = httptest.NewRecorder()
	controller.ListGlobalDestinations(response, httptest.NewRequest(http.MethodGet, "/api/notification-governance/destinations?count=2&count=3", nil))
	assert.Equal(t, http.StatusBadRequest, response.Code)
	response = httptest.NewRecorder()
	controller.ListGlobalDestinations(response, httptest.NewRequest(http.MethodGet, "/api/notification-governance/destinations?count=3&offset=2", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, db.RetrieveQueryParams{Count: 3, Offset: 2}, service.listParams)
}

func TestNotificationGovernanceControllerEnforcesRouteScopeAndSafeErrors(t *testing.T) {
	projectID := 19
	service := &notificationGovernanceStub{err: pro_interfaces.ErrNotificationDestinationMissing}
	controller := NewNotificationGovernanceController(service)
	request := httptest.NewRequest(http.MethodGet, "/api/project/19/notification-governance/destinations/5", nil)
	request = mux.SetURLVars(request, map[string]string{"project_id": "19", "destination_id": "5"})
	request = helpers.SetContextValue(request, "project", db.Project{ID: projectID})
	response := httptest.NewRecorder()
	controller.GetProjectDestination(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code)
	require.NotNil(t, service.getDestinationScope)
	assert.Equal(t, projectID, *service.getDestinationScope)
	assert.Equal(t, 5, service.getDestinationID)
	assert.NotContains(t, response.Body.String(), "controller-write-only-secret")

	service.err = pro_interfaces.ErrNotificationRevisionConflict
	request = httptest.NewRequest(http.MethodPost, "/api/notification-governance/deliveries/9/retry", nil)
	request = mux.SetURLVars(request, map[string]string{"delivery_id": "9"})
	response = httptest.NewRecorder()
	controller.RetryGlobalDelivery(response, request)
	assert.Equal(t, http.StatusConflict, response.Code)
	service.err = pro_interfaces.ErrNotificationUnavailable
	response = httptest.NewRecorder()
	controller.RetryGlobalDelivery(response, request)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestNotificationGovernanceControllerDeletesScopedRevisionedResources(t *testing.T) {
	projectID := 19
	service := &notificationGovernanceStub{}
	controller := NewNotificationGovernanceController(service)
	request := httptest.NewRequest(http.MethodDelete, "/api/project/19/notification-governance/destinations/5", strings.NewReader(`{"revision":3}`))
	request = mux.SetURLVars(request, map[string]string{"project_id": "19", "destination_id": "5"})
	request = helpers.SetContextValue(request, "project", db.Project{ID: projectID})
	response := httptest.NewRecorder()
	controller.DeleteProjectDestination(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.NotNil(t, service.deleteDestinationScope)
	assert.Equal(t, projectID, *service.deleteDestinationScope)
	assert.Equal(t, 5, service.deleteDestinationID)
	assert.Equal(t, 3, service.deleteRevision)

	request = httptest.NewRequest(http.MethodDelete, "/api/notification-governance/rules/7", strings.NewReader(`{"revision":0}`))
	request = mux.SetURLVars(request, map[string]string{"rule_id": "7"})
	response = httptest.NewRecorder()
	controller.DeleteGlobalRule(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)

	service.err = pro_interfaces.ErrNotificationRevisionConflict
	request = httptest.NewRequest(http.MethodDelete, "/api/notification-governance/rules/7", strings.NewReader(`{"revision":2}`))
	request = mux.SetURLVars(request, map[string]string{"rule_id": "7"})
	response = httptest.NewRecorder()
	controller.DeleteGlobalRule(response, request)
	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Nil(t, service.deleteRuleScope)
	assert.Equal(t, 7, service.deleteRuleID)
}

func TestNotificationGovernanceHistoryReturnsOnlyAllowListedEventProjection(t *testing.T) {
	service := &notificationGovernanceStub{history: []pro_interfaces.NotificationDeliveryDTO{{
		ID: 3, EventID: "event-id", SourceKind: pro_interfaces.NotificationSourceTask, SourceID: "task:42",
		LifecycleAction: pro_interfaces.NotificationLifecycleTrigger, Severity: pro_interfaces.NotificationSeverityError,
		LastReason:        db.NotificationDeliveryReasonTransport,
		ProviderRequestID: "request_1",
		ProviderRecordID:  "0123456789abcdef0123456789abcdef",
		ProviderRecordURL: "https://example.service-now.com/incident.do?sys_id=0123456789abcdef0123456789abcdef",
	}}}
	controller := NewNotificationGovernanceController(service)
	response := httptest.NewRecorder()
	controller.GlobalHistory(response, httptest.NewRequest(http.MethodGet, "/api/notification-governance/deliveries", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"source_kind":"task"`)
	assert.Contains(t, response.Body.String(), `"source_id":"task:42"`)
	assert.Contains(t, response.Body.String(), `"lifecycle_action":"trigger"`)
	assert.Contains(t, response.Body.String(), `"severity":"error"`)
	assert.Contains(t, response.Body.String(), `"last_reason":"transport_error"`)
	assert.Contains(t, response.Body.String(), `"provider_request_id":"request_1"`)
	assert.Contains(t, response.Body.String(), `"provider_record_id":"0123456789abcdef0123456789abcdef"`)
	assert.Contains(t, response.Body.String(), `"provider_record_url":"https://example.service-now.com/incident.do?sys_id=0123456789abcdef0123456789abcdef"`)
	assert.NotContains(t, response.Body.String(), "details")
	assert.NotContains(t, response.Body.String(), "credential")
}

func TestNotificationGovernanceEventHistoryReturnsFilteredOutcomeWithoutDetails(t *testing.T) {
	service := &notificationGovernanceStub{events: []pro_interfaces.NotificationEventHistoryDTO{{
		EventID: "filtered-event", SourceKind: pro_interfaces.NotificationSourceSystem, SourceID: "system:node",
		LifecycleID: "system:node", Severity: pro_interfaces.NotificationSeverityWarning,
		LifecycleAction: pro_interfaces.NotificationLifecycleUpdate, IncidentKey: "incident", RoutingOutcome: db.NotificationRoutingFiltered,
	}}}
	controller := NewNotificationGovernanceController(service)
	response := httptest.NewRecorder()
	controller.GlobalEventHistory(response, httptest.NewRequest(http.MethodGet, "/api/notification-governance/events", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"routing_outcome":"filtered"`)
	assert.Contains(t, response.Body.String(), `"source_id":"system:node"`)
	assert.NotContains(t, response.Body.String(), "details")
	assert.NotContains(t, response.Body.String(), "credential")
}

func TestNotificationGovernanceAuditDescriptorHasOnlyScopeIdentifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/project/23/notification-governance/destinations/77/test", nil)
	request = mux.SetURLVars(request, map[string]string{"project_id": "23", "destination_id": "77"})
	descriptor, ok := enhancedAuditForRoute(request)
	require.True(t, ok)
	assert.Equal(t, pro_interfaces.AuditTargetNotification, descriptor.TargetType)
	assert.Equal(t, "project:23", descriptor.TargetID)
	assert.Equal(t, pro_interfaces.AuditActionCapabilityExecute, descriptor.Action)
	assert.NotContains(t, descriptor.TargetID, "77")
	projectID := 23
	assert.NoError(t, (pro_interfaces.AuditEvent{
		CorrelationID: "internal", Action: descriptor.Action, TargetType: descriptor.TargetType, TargetID: descriptor.TargetID,
		ProjectID: &projectID, Outcome: pro_interfaces.AuditOutcomeAllowed, Source: pro_interfaces.AuditSourceAPI,
		Reason: string(pro_interfaces.CapabilityReasonActive),
	}).Validate())
}

func TestNotificationGovernanceRouterEnforcesGlobalAndProjectPermissions(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config = &util.ConfigType{Debugging: &util.DebuggingConfig{}}
	admin, err := store.CreateUserWithoutPassword(db.User{Username: "notification-admin", Name: "Admin", Email: "admin@example.test", Admin: true})
	require.NoError(t, err)
	denied, err := store.CreateUserWithoutPassword(db.User{Username: "notification-denied", Name: "Denied", Email: "denied@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "notification router"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: denied.ID, Role: db.ProjectGuest})
	require.NoError(t, err)
	for _, token := range []db.APIToken{{ID: "notification-admin-token", UserID: admin.ID, Name: "admin"}, {ID: "notification-denied-token", UserID: denied.ID, Name: "denied"}} {
		_, err = store.CreateAPIToken(token)
		require.NoError(t, err)
	}
	router := Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, notificationGovernanceLogWriter{}, nil, metrics.NewMetrics(), &notificationGovernanceStub{err: pro_interfaces.ErrNotificationUnavailable})
	for _, test := range []struct {
		token, path string
		expected    int
	}{
		{"notification-admin-token", "/api/notification-governance/destinations", http.StatusServiceUnavailable},
		{"notification-denied-token", "/api/notification-governance/destinations", http.StatusForbidden},
		{"notification-admin-token", "/api/notification-governance/events", http.StatusServiceUnavailable},
		{"notification-denied-token", "/api/notification-governance/events", http.StatusForbidden},
		{"notification-admin-token", "/api/project/" + strconv.Itoa(project.ID) + "/notification-governance/destinations", http.StatusServiceUnavailable},
		{"notification-denied-token", "/api/project/" + strconv.Itoa(project.ID) + "/notification-governance/destinations", http.StatusForbidden},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Authorization", "Bearer "+test.token)
		request = helpers.SetContextValue(request, "store", store)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assert.Equal(t, test.expected, response.Code, test.path+" "+test.token)
	}
}

func TestWriteNotificationGovernanceErrorDoesNotExposeUnexpectedFailures(t *testing.T) {
	response := httptest.NewRecorder()
	writeNotificationGovernanceError(response, errors.New("provider token=never-return-this"))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.NotContains(t, response.Body.String(), "token=")

}
