package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLDAPGroupMappingControllerWritesStableTargetAndAudits(t *testing.T) {
	service := &ldapServiceStub{}
	audit := &auditRecorderStub{}
	controller := NewLDAPController(service, audit)
	request := httptest.NewRequest(http.MethodPut, "/api/capabilities/ldap/group-mappings/engineering",
		strings.NewReader(`{
			"provider_id":"corp",
			"group_external_id":"entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67",
			"target":{"scope":"global","role_id":"global_auditor"},
			"enabled":true,
			"expected_revision":0
		}`))
	request = mux.SetURLVars(request, map[string]string{"mapping_id": "engineering"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	controller.SaveGroupMapping(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 7, service.groupSaveRequest.ActorID)
	assert.Equal(t, "engineering", service.groupSaveRequest.Mapping.ID)
	assert.Equal(t, pro_interfaces.LDAPRoleScopeGlobal, service.groupSaveRequest.Mapping.Target.Scope)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionLDAPGroupMappingWrite, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditTargetLDAPGroupMapping, audit.events[0].TargetType)
	assert.Equal(t, "entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67", audit.events[0].TargetID)
	assert.NoError(t, audit.events[0].Validate())
}

func TestLDAPGroupMappingControllerMapsStaleApplyToConflict(t *testing.T) {
	service := &ldapServiceStub{err: pro_interfaces.ErrLDAPGroupPreviewStale}
	audit := &auditRecorderStub{}
	controller := NewLDAPController(service, audit)
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/ldap/group-mappings/apply",
		strings.NewReader(`{"provider_id":"corp","preview_token":"stale-preview"}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	controller.ApplyGroupPreview(recorder, request)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "LDAP_GROUP_PREVIEW_STALE")
	assert.Equal(t, "stale-preview", service.groupApplyRequest.Token)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
	assert.NoError(t, audit.events[0].Validate())
}

func TestLDAPGroupMappingControllerReturnsPreviewAndHistory(t *testing.T) {
	service := &ldapServiceStub{
		groupPreview: pro_interfaces.LDAPGroupPreview{ProviderID: "corp", DirectoryRevision: "directory"},
		groupHistory: []db.LDAPGroupReconciliation{{ID: 3, ProviderID: "corp", Status: "applied"}},
	}
	controller := NewLDAPController(service, nil)
	previewRequest := httptest.NewRequest(http.MethodPost, "/api/capabilities/ldap/group-mappings/preview",
		strings.NewReader(`{"provider_id":"corp"}`))
	previewRequest = helpers.SetContextValue(previewRequest, "user", &db.User{ID: 7, Admin: true})
	previewRecorder := httptest.NewRecorder()

	controller.PreviewGroupMappings(previewRecorder, previewRequest)

	assert.Equal(t, http.StatusOK, previewRecorder.Code)
	assert.Contains(t, previewRecorder.Body.String(), `"directory_revision":"directory"`)
	assert.Equal(t, "manual", service.groupPreviewRequest.Source)

	historyRequest := httptest.NewRequest(http.MethodGet,
		"/api/capabilities/ldap/group-mappings/history?provider_id=corp", nil)
	historyRecorder := httptest.NewRecorder()
	controller.GroupReconciliationHistory(historyRecorder, historyRequest)
	assert.Equal(t, http.StatusOK, historyRecorder.Code)
	assert.Contains(t, historyRecorder.Body.String(), `"status":"applied"`)
}

func TestLDAPGroupMappingControllerListsAndDeletesMappings(t *testing.T) {
	service := &ldapServiceStub{groupMappings: []pro_interfaces.LDAPGroupMapping{{
		ID: "engineering", ProviderID: "corp", Revision: 3,
		GroupExternalID: "entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67",
	}}}
	controller := NewLDAPController(service, nil)
	listRequest := httptest.NewRequest(http.MethodGet,
		"/api/capabilities/ldap/group-mappings?provider_id=corp", nil)
	listRecorder := httptest.NewRecorder()

	controller.GroupMappings(listRecorder, listRequest)

	assert.Equal(t, http.StatusOK, listRecorder.Code)
	assert.Contains(t, listRecorder.Body.String(), `"id":"engineering"`)

	deleteRequest := httptest.NewRequest(http.MethodDelete,
		"/api/capabilities/ldap/group-mappings/engineering?provider_id=corp&expected_revision=3", nil)
	deleteRequest = mux.SetURLVars(deleteRequest, map[string]string{"mapping_id": "engineering"})
	deleteRequest = helpers.SetContextValue(deleteRequest, "user", &db.User{ID: 7, Admin: true})
	deleteRecorder := httptest.NewRecorder()

	controller.DeleteGroupMapping(deleteRecorder, deleteRequest)

	assert.Equal(t, http.StatusNoContent, deleteRecorder.Code)
	assert.Equal(t, "corp", service.groupDeleteRequest.ProviderID)
	assert.Equal(t, "engineering", service.groupDeleteRequest.MappingID)
	assert.Equal(t, 3, service.groupDeleteRequest.ExpectedRevision)
}

func TestLDAPGroupMappingControllerMapsPolicyAndTargetErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode int
		code       string
	}{
		{name: "permission", err: pro_interfaces.ErrLDAPForbidden,
			statusCode: http.StatusForbidden, code: "LDAP_FORBIDDEN"},
		{name: "unresolved directory item", err: pro_interfaces.ErrLDAPGroupUnresolved,
			statusCode: http.StatusConflict, code: "LDAP_GROUP_UNRESOLVED"},
		{name: "protected administrator", err: pro_interfaces.ErrLDAPGroupProtectedAdministrator,
			statusCode: http.StatusConflict, code: "LDAP_GROUP_PROTECTED_ADMINISTRATOR"},
		{name: "unresolved role target", err: common_errors.NewValidationError("LDAP group mapping role target does not exist"),
			statusCode: http.StatusBadRequest, code: "LDAP group mapping role target does not exist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &ldapServiceStub{err: tt.err}
			controller := NewLDAPController(service, nil)
			request := httptest.NewRequest(http.MethodPost,
				"/api/capabilities/ldap/group-mappings/reconcile", strings.NewReader(`{"provider_id":"corp"}`))
			request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: tt.err != pro_interfaces.ErrLDAPForbidden})
			recorder := httptest.NewRecorder()

			controller.ReconcileGroupMappings(recorder, request)

			assert.Equal(t, tt.statusCode, recorder.Code)
			assert.Contains(t, recorder.Body.String(), tt.code)
		})
	}
}
