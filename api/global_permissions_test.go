package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func withActiveProjectRolesSnapshot(request *http.Request, user db.User) *http.Request {
	snapshot := pro_interfaces.NewCapabilitySnapshot(pro_interfaces.CapabilityRequest{
		UserID: user.ID, IsAdmin: user.Admin, At: time.Unix(1_700_000_000, 0).UTC(),
	}, []pro_interfaces.CapabilityDecision{pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityProjectRoles,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
		},
		nil,
	)})
	return request.WithContext(context.WithValue(request.Context(), capabilitySnapshotContextKey{}, snapshot))
}

func TestGlobalPermissionMiddlewareRejectsProjectAdminAndAllowsExplicitGlobalGrant(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectAdmin, err := store.CreateUserWithoutPassword(db.User{
		Username: "project-only-admin", Name: "Project-only admin",
		Email: "project-only-admin@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "user_manager", Slug: "user_manager", Name: "User manager",
		GlobalPermissions: db.CanManageGlobalUsers, Revision: 1,
	})
	require.NoError(t, err)

	reached := false
	handler := globalPermissionMiddleware(db.CanManageGlobalUsers)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusNoContent)
		},
	))

	request := httptest.NewRequest(http.MethodPost, "/api/users", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &projectAdmin)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.False(t, reached)

	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: projectAdmin.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)
	unavailable := pro_interfaces.NewCapabilitySnapshot(pro_interfaces.CapabilityRequest{
		UserID: projectAdmin.ID, At: time.Unix(1_700_000_000, 0).UTC(),
	}, []pro_interfaces.CapabilityDecision{pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityProjectRoles,
		pro_interfaces.CapabilityStateUnavailable,
		pro_interfaces.CapabilityReasonProviderUnavailable,
		nil,
		nil,
	)})
	request = request.WithContext(context.WithValue(request.Context(), capabilitySnapshotContextKey{}, unavailable))
	recorder = httptest.NewRecorder()
	reached = false
	handler.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.False(t, reached)

	request = withActiveProjectRolesSnapshot(request, projectAdmin)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.True(t, reached)
}

func TestDelegatedUserManagerCanCreateUsersButCannotGrantBreakGlassAdmin(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	manager, err := store.CreateUserWithoutPassword(db.User{
		Username: "delegated-user-manager", Name: "Delegated user manager",
		Email: "delegated-user-manager@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "delegated_user_management", Slug: "delegated_user_management",
		Name: "Delegated user management", GlobalPermissions: db.CanManageGlobalUsers,
		Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: manager.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)

	controller := NewUsersController(nil)
	request := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(
		`{"username":"managed-user","name":"Managed user","email":"managed@example.test","password":"strongpassword1"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &manager)
	request = withActiveProjectRolesSnapshot(request, manager)
	recorder := httptest.NewRecorder()
	controller.AddUser(recorder, request)
	assert.Equal(t, http.StatusCreated, recorder.Code)

	escalation := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(
		`{"username":"forbidden-admin","name":"Forbidden admin","email":"forbidden-admin@example.test","password":"strongpassword1","admin":true}`,
	))
	escalation.Header.Set("Content-Type", "application/json")
	escalation = helpers.SetContextValue(escalation, "store", store)
	escalation = helpers.SetContextValue(escalation, "user", &manager)
	escalation = withActiveProjectRolesSnapshot(escalation, manager)
	recorder = httptest.NewRecorder()
	controller.AddUser(recorder, escalation)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestGlobalPermissionMiddlewareKeepsBuiltInAdminAsBreakGlass(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "break-glass-admin", Name: "Break-glass admin",
		Email: "break-glass-admin@example.test", Admin: true,
	})
	require.NoError(t, err)

	handler := globalPermissionMiddleware(db.CanManageGlobalRoles)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	request := httptest.NewRequest(http.MethodGet, "/api/roles", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &admin)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestDelegatedSystemManagerCanUseSystemOptionsWithoutBuiltInAdmin(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	manager, err := store.CreateUserWithoutPassword(db.User{
		Username: "delegated-system-manager", Name: "Delegated system manager",
		Email: "delegated-system-manager@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "delegated_system_management", Slug: "delegated_system_management",
		Name: "Delegated system management", GlobalPermissions: db.CanManageGlobalSystem,
		Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: manager.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)

	setRequest := httptest.NewRequest(http.MethodPost, "/api/options", bytes.NewBufferString(
		`{"key":"delegated_system_option","value":"enabled"}`,
	))
	setRequest.Header.Set("Content-Type", "application/json")
	setRequest = helpers.SetContextValue(setRequest, "store", store)
	setRequest = helpers.SetContextValue(setRequest, "user", &manager)
	setRequest = withActiveProjectRolesSnapshot(setRequest, manager)
	setResponse := httptest.NewRecorder()
	setOption(setResponse, setRequest)
	assert.Equal(t, http.StatusOK, setResponse.Code)

	getRequest := httptest.NewRequest(http.MethodGet, "/api/options", nil)
	getRequest = helpers.SetContextValue(getRequest, "store", store)
	getRequest = helpers.SetContextValue(getRequest, "user", &manager)
	getRequest = withActiveProjectRolesSnapshot(getRequest, manager)
	getResponse := httptest.NewRecorder()
	getOptions(getResponse, getRequest)
	assert.Equal(t, http.StatusOK, getResponse.Code)
	var options map[string]string
	require.NoError(t, json.Unmarshal(getResponse.Body.Bytes(), &options))
	assert.Equal(t, "enabled", options["delegated_system_option"])
}

func TestDelegatedSystemManagerCannotUseBuiltInAdminOnlyRoute(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	manager, err := store.CreateUserWithoutPassword(db.User{
		Username: "delegated-system-boundary", Name: "Delegated system boundary",
		Email: "delegated-system-boundary@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "delegated_system_boundary", Slug: "delegated_system_boundary", Name: "System manager",
		GlobalPermissions: db.CanManageGlobalSystem, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: manager.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)

	reached := false
	handler := adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/cluster", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &manager)
	request = withActiveProjectRolesSnapshot(request, manager)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.False(t, reached)
}

func TestGlobalAuditReaderCanReadGlobalAuditWithoutSystemManagement(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	reader, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-audit-reader", Name: "Global audit reader",
		Email: "global-audit-reader@example.test",
	})
	require.NoError(t, err)
	other, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-audit-other", Name: "Global audit other",
		Email: "global-audit-other@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "global_audit_reader", Slug: "global_audit_reader", Name: "Global audit reader",
		GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: reader.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)
	description := "global audit entry"
	_, err = store.CreateEvent(db.Event{UserID: &other.ID, Description: &description})
	require.NoError(t, err)

	handler := globalPermissionMiddleware(db.CanReadGlobalAudit)(http.HandlerFunc(getGlobalAuditEvents))
	request := httptest.NewRequest(http.MethodGet, "/api/audit/events", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &reader)
	request = withActiveProjectRolesSnapshot(request, reader)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusOK, response.Code)
	var events []db.Event
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &events))
	require.Len(t, events, 1)
	assert.Equal(t, other.ID, *events[0].UserID)

	request = helpers.SetContextValue(request, "user", &other)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestDelegatedUserManagerCannotModifyBuiltInAdministrator(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	manager, err := store.CreateUserWithoutPassword(db.User{
		Username: "delegated-admin-target-manager", Name: "Delegated manager",
		Email: "delegated-admin-target-manager@example.test",
	})
	require.NoError(t, err)
	administrator, err := store.CreateUser(db.UserWithPwd{
		Pwd: "original-password",
		User: db.User{
			Username: "protected-built-in-admin", Name: "Protected administrator",
			Email: "protected-built-in-admin@example.test", Admin: true,
		},
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "admin_target_user_manager", Slug: "admin_target_user_manager", Name: "User manager",
		GlobalPermissions: db.CanManageGlobalUsers, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: manager.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)

	controller := NewUsersController(nil)
	update := httptest.NewRequest(http.MethodPut, "/api/users/2", bytes.NewBufferString(
		`{"name":"Changed","username":"protected-built-in-admin","email":"protected-built-in-admin@example.test","admin":true,"password":"attacker-password"}`,
	))
	update.Header.Set("Content-Type", "application/json")
	update = helpers.SetContextValue(update, "store", store)
	update = helpers.SetContextValue(update, "user", &manager)
	update = helpers.SetContextValue(update, "_user", administrator)
	update = withActiveProjectRolesSnapshot(update, manager)
	updateResponse := httptest.NewRecorder()
	controller.UpdateUser(updateResponse, update)
	assert.Equal(t, http.StatusForbidden, updateResponse.Code)

	reset := httptest.NewRequest(http.MethodPost, "/api/users/2/password", bytes.NewBufferString(
		`{"password":"attacker-password"}`,
	))
	reset.Header.Set("Content-Type", "application/json")
	reset = helpers.SetContextValue(reset, "store", store)
	reset = helpers.SetContextValue(reset, "user", &manager)
	reset = helpers.SetContextValue(reset, "_user", administrator)
	reset = withActiveProjectRolesSnapshot(reset, manager)
	resetResponse := httptest.NewRecorder()
	controller.UpdateUserPassword(resetResponse, reset)
	assert.Equal(t, http.StatusForbidden, resetResponse.Code)

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/users/2", nil)
	deleteRequest = helpers.SetContextValue(deleteRequest, "store", store)
	deleteRequest = helpers.SetContextValue(deleteRequest, "user", &manager)
	deleteRequest = helpers.SetContextValue(deleteRequest, "_user", administrator)
	deleteRequest = withActiveProjectRolesSnapshot(deleteRequest, manager)
	deleteResponse := httptest.NewRecorder()
	controller.DeleteUser(deleteResponse, deleteRequest)
	assert.Equal(t, http.StatusForbidden, deleteResponse.Code)

	unchanged, err := store.GetUser(administrator.ID)
	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(unchanged.Password), []byte("original-password")))
}

func TestDelegatedAdministratorPasswordResetDenialIsAudited(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	manager, err := store.CreateUserWithoutPassword(db.User{
		Username: "delegated-password-audit", Name: "Delegated password audit",
		Email: "delegated-password-audit@example.test",
	})
	require.NoError(t, err)
	administrator, err := store.CreateUser(db.UserWithPwd{
		Pwd: "original-password",
		User: db.User{
			Username: "password-audit-admin", Name: "Password audit administrator",
			Email: "password-audit-admin@example.test", Admin: true,
		},
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "password_audit_manager", Slug: "password_audit_manager", Name: "User manager",
		GlobalPermissions: db.CanManageGlobalUsers, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: manager.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)

	audit := &auditRecorderStub{}
	controller := NewUsersController(nil)
	handler := controller.GetUserMiddleware(EnhancedGlobalPermissionAuditMiddleware(audit)(
		http.HandlerFunc(controller.UpdateUserPassword),
	))
	request := httptest.NewRequest(http.MethodPost, "/api/users/2/password", bytes.NewBufferString(
		`{"password":"attacker-password"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request = mux.SetURLVars(request, map[string]string{"user_id": strconv.Itoa(administrator.ID)})
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &manager)
	request = withActiveProjectRolesSnapshot(request, manager)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionGlobalUserPassword, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
	assert.Equal(t, "user:"+strconv.Itoa(administrator.ID), audit.events[0].TargetID)
	require.NoError(t, audit.events[0].Validate())
}
