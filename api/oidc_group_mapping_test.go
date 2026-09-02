package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type oidcGroupMappingServiceStub struct {
	availableErr      error
	err               error
	mappings          []pro_interfaces.OIDCGroupMapping
	preview           pro_interfaces.OIDCGroupPreview
	history           []db.OIDCGroupReconciliation
	assignments       []pro_interfaces.OIDCRoleAssignment
	saveRequest       pro_interfaces.OIDCGroupMappingRequest
	deleteRequest     pro_interfaces.OIDCGroupMappingDeleteRequest
	previewRequest    pro_interfaces.OIDCGroupPreviewRequest
	reconcileRequests []pro_interfaces.OIDCGroupPreviewRequest
}

func (s *oidcGroupMappingServiceStub) Available(context.Context) error { return s.availableErr }
func (s *oidcGroupMappingServiceStub) GroupMappings(context.Context, string) ([]pro_interfaces.OIDCGroupMapping, error) {
	return s.mappings, s.err
}
func (s *oidcGroupMappingServiceStub) SaveGroupMapping(_ context.Context, request pro_interfaces.OIDCGroupMappingRequest) (pro_interfaces.OIDCGroupMapping, error) {
	s.saveRequest = request
	if s.err != nil {
		return pro_interfaces.OIDCGroupMapping{}, s.err
	}
	return request.Mapping, nil
}
func (s *oidcGroupMappingServiceStub) DeleteGroupMapping(_ context.Context, request pro_interfaces.OIDCGroupMappingDeleteRequest) error {
	s.deleteRequest = request
	return s.err
}
func (s *oidcGroupMappingServiceStub) PreviewGroupMappings(_ context.Context, request pro_interfaces.OIDCGroupPreviewRequest) (pro_interfaces.OIDCGroupPreview, error) {
	s.previewRequest = request
	return s.preview, s.err
}
func (s *oidcGroupMappingServiceStub) ReconcileGroupMappings(_ context.Context, request pro_interfaces.OIDCGroupPreviewRequest) (pro_interfaces.OIDCGroupPreview, error) {
	s.reconcileRequests = append(s.reconcileRequests, request)
	return s.preview, s.err
}
func (s *oidcGroupMappingServiceStub) GroupReconciliationHistory(context.Context, string, int) ([]db.OIDCGroupReconciliation, error) {
	return s.history, s.err
}
func (s *oidcGroupMappingServiceStub) EffectiveGroupAssignments(context.Context, string) ([]pro_interfaces.OIDCRoleAssignment, error) {
	return s.assignments, s.err
}

func withOIDCGroupProvider(t *testing.T) {
	t.Helper()
	original := util.Config
	t.Cleanup(func() { util.Config = original })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{
		"corp": {
			DisplayName: "Corporate", ClientSecret: "must-not-leak",
			GroupClaimPath: "realm.groups", GroupClaimCaseInsensitive: true,
			GroupClaimMissingPolicy: "preserve",
		},
	}}
}

func TestOIDCGroupMappingControllerListsSecretFreeProviders(t *testing.T) {
	withOIDCGroupProvider(t)
	controller := NewOIDCGroupMappingController(&oidcGroupMappingServiceStub{}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/capabilities/oidc/group-mapping/providers", nil)
	recorder := httptest.NewRecorder()

	controller.Providers(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"id":"corp"`)
	assert.Contains(t, recorder.Body.String(), `"path":"realm.groups"`)
	assert.NotContains(t, recorder.Body.String(), "must-not-leak")
	assert.NotContains(t, recorder.Body.String(), "client_secret")
}

func TestOIDCGroupMappingControllerResolvesExistingMixedCaseProviderID(t *testing.T) {
	withOIDCGroupProvider(t)
	provider := util.Config.OidcProviders["corp"]
	delete(util.Config.OidcProviders, "corp")
	util.Config.OidcProviders["Corporate"] = provider
	service := &oidcGroupMappingServiceStub{}
	controller := NewOIDCGroupMappingController(service, nil)

	request := httptest.NewRequest(http.MethodGet,
		"/api/capabilities/oidc/group-mappings?provider_id=corporate", nil)
	recorder := httptest.NewRecorder()
	controller.GroupMappings(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestOIDCGroupMappingControllerRejectsAmbiguousProviderIDs(t *testing.T) {
	withOIDCGroupProvider(t)
	util.Config.OidcProviders["Corp"] = util.Config.OidcProviders["corp"]
	controller := NewOIDCGroupMappingController(&oidcGroupMappingServiceStub{}, nil)

	request := httptest.NewRequest(http.MethodGet,
		"/api/capabilities/oidc/group-mapping/providers", nil)
	recorder := httptest.NewRecorder()
	controller.Providers(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "ambiguous")
}

func TestOIDCGroupMappingControllerCRUDAndAuditContract(t *testing.T) {
	withOIDCGroupProvider(t)
	service := &oidcGroupMappingServiceStub{}
	audit := &auditRecorderStub{}
	controller := NewOIDCGroupMappingController(service, audit)
	request := httptest.NewRequest(http.MethodPut, "/api/capabilities/oidc/group-mappings/engineering",
		strings.NewReader(`{
			"provider_id":"corp","claim_value":"Engineering",
			"target":{"scope":"global","role_id":"auditor"},
			"enabled":true,"expected_revision":0
		}`))
	request = mux.SetURLVars(request, map[string]string{"mapping_id": "engineering"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	controller.SaveGroupMapping(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "engineering", service.saveRequest.Mapping.ID)
	assert.Equal(t, "corp", service.saveRequest.Mapping.ProviderID)
	assert.Equal(t, "Engineering", service.saveRequest.Mapping.ClaimValue)
	assert.True(t, service.saveRequest.Configuration.CaseInsensitive)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionOIDCGroupMappingWrite, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditTargetOIDCGroupMapping, audit.events[0].TargetType)
	assert.Equal(t, "provider:corp", audit.events[0].TargetID)
	assert.NoError(t, audit.events[0].Validate())

	deleteRequest := httptest.NewRequest(http.MethodDelete,
		"/api/capabilities/oidc/group-mappings/engineering?provider_id=corp&expected_revision=3", nil)
	deleteRequest = mux.SetURLVars(deleteRequest, map[string]string{"mapping_id": "engineering"})
	deleteRequest = helpers.SetContextValue(deleteRequest, "user", &db.User{ID: 7, Admin: true})
	deleteRecorder := httptest.NewRecorder()
	controller.DeleteGroupMapping(deleteRecorder, deleteRequest)
	assert.Equal(t, http.StatusNoContent, deleteRecorder.Code)
	assert.Equal(t, 3, service.deleteRequest.ExpectedRevision)
}

func TestOIDCGroupMappingControllerPreviewAcceptsOnlySelectedClaimFixture(t *testing.T) {
	withOIDCGroupProvider(t)
	service := &oidcGroupMappingServiceStub{preview: pro_interfaces.OIDCGroupPreview{
		ProviderID: "corp", UnknownValues: []string{"unknown"},
	}}
	controller := NewOIDCGroupMappingController(service, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/oidc/group-mappings/preview",
		strings.NewReader(`{"provider_id":"corp","user_id":9,"claim":["Engineering","unknown"]}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	controller.PreviewGroupMappings(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, []string{"engineering", "unknown"}, service.previewRequest.Claim.Values)
	assert.Equal(t, "manual", service.previewRequest.Source)
	assert.NotContains(t, recorder.Body.String(), "access_token")

	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/capabilities/oidc/group-mappings/preview",
		strings.NewReader(`{"provider_id":"corp","user_id":9,"claim":{"token":"forbidden"}}`))
	invalidRequest = helpers.SetContextValue(invalidRequest, "user", &db.User{ID: 7, Admin: true})
	invalidRecorder := httptest.NewRecorder()
	controller.PreviewGroupMappings(invalidRecorder, invalidRequest)
	assert.Equal(t, http.StatusBadRequest, invalidRecorder.Code)
	assert.Contains(t, invalidRecorder.Body.String(), "string or string array")
}

func TestOIDCGroupMappingControllerMapsStaleAndExposesHistoryAndAssignments(t *testing.T) {
	withOIDCGroupProvider(t)
	stale := NewOIDCGroupMappingController(&oidcGroupMappingServiceStub{
		err: pro_interfaces.ErrOIDCGroupMappingPreviewStale,
	}, nil)
	request := httptest.NewRequest(http.MethodPut, "/api/capabilities/oidc/group-mappings/engineering",
		strings.NewReader(`{
			"provider_id":"corp","claim_value":"engineering",
			"target":{"scope":"global","role_id":"auditor"},
			"enabled":true,"expected_revision":2
		}`))
	request = mux.SetURLVars(request, map[string]string{"mapping_id": "engineering"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()
	stale.SaveGroupMapping(recorder, request)
	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "OIDC_GROUP_MAPPING_STALE")

	service := &oidcGroupMappingServiceStub{
		history: []db.OIDCGroupReconciliation{{ID: 3, ProviderID: "corp", UserID: 9, Status: "applied"}},
		assignments: []pro_interfaces.OIDCRoleAssignment{{
			UserID: 9, OwnerKind: "oidc", ManagedByProviderID: "corp", ManagedByMappingID: "engineering",
		}},
	}
	controller := NewOIDCGroupMappingController(service, nil)
	historyRequest := httptest.NewRequest(http.MethodGet,
		"/api/capabilities/oidc/group-mappings/history?provider_id=corp", nil)
	historyRecorder := httptest.NewRecorder()
	controller.GroupReconciliationHistory(historyRecorder, historyRequest)
	assert.Equal(t, http.StatusOK, historyRecorder.Code)
	assert.Contains(t, historyRecorder.Body.String(), `"status":"applied"`)
	assert.NotContains(t, historyRecorder.Body.String(), "preview_json")

	assignmentRequest := httptest.NewRequest(http.MethodGet,
		"/api/capabilities/oidc/group-mappings/assignments?provider_id=corp", nil)
	assignmentRecorder := httptest.NewRecorder()
	controller.EffectiveGroupAssignments(assignmentRecorder, assignmentRequest)
	assert.Equal(t, http.StatusOK, assignmentRecorder.Code)
	assert.Contains(t, assignmentRecorder.Body.String(), `"managed_by_mapping_id":"engineering"`)
}

func TestOIDCGroupMappingRoutesDenyNonAdministrators(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config = &util.ConfigType{
		Debugging: &util.DebuggingConfig{},
		OidcProviders: map[string]util.OidcProvider{
			"corp": {GroupClaimPath: "realm.groups"},
		},
	}
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "oidc-route-non-admin", Name: "OIDC route non-admin", Email: "oidc-route-non-admin@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "oidc-route-token", UserID: user.ID, Name: "OIDC route security test"})
	require.NoError(t, err)
	router := Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	for _, endpoint := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/capabilities/oidc/group-mapping/providers", ""},
		{http.MethodGet, "/api/capabilities/oidc/group-mappings?provider_id=corp", ""},
		{http.MethodPut, "/api/capabilities/oidc/group-mappings/engineering", `{}`},
		{http.MethodDelete, "/api/capabilities/oidc/group-mappings/engineering?provider_id=corp&expected_revision=1", ""},
		{http.MethodPost, "/api/capabilities/oidc/group-mappings/preview", `{}`},
		{http.MethodGet, "/api/capabilities/oidc/group-mappings/history?provider_id=corp", ""},
		{http.MethodGet, "/api/capabilities/oidc/group-mappings/assignments?provider_id=corp", ""},
	} {
		t.Run(endpoint.method+" "+endpoint.path, func(t *testing.T) {
			request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
			request.Header.Set("Authorization", "Bearer oidc-route-token")
			request = helpers.SetContextValue(request, "store", store)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, http.StatusForbidden, response.Code)
		})
	}
}

var _ pro_interfaces.OIDCGroupMappingService = (*oidcGroupMappingServiceStub)(nil)
