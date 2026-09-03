package api_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	rootapi "github.com/semaphoreui/semaphore/api"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type policyGuardrailPermissionLogWriter struct{}

func (policyGuardrailPermissionLogWriter) WriteEventLog(pro_interfaces.EventLogRecord) error {
	return nil
}
func (policyGuardrailPermissionLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error {
	return nil
}
func (policyGuardrailPermissionLogWriter) WriteResult(any) error { return nil }

func TestPolicyGuardrailRoutesKeepManageAndRollbackPermissionsIndependent(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config.Debugging = &util.DebuggingConfig{}
	project, err := store.CreateProject(db.Project{Name: "policy guardrail permissions"})
	require.NoError(t, err)

	globalManage := createPolicyGuardrailGlobalUser(t, store, "manage", db.CanManageGlobalPolicyGuardrails)
	globalRollback := createPolicyGuardrailGlobalUser(t, store, "rollback", db.CanRollbackGlobalPolicyGuardrails)
	projectManage := createPolicyGuardrailProjectUser(t, store, project.ID, "manage", db.CanManagePolicyGuardrails)
	projectRollback := createPolicyGuardrailProjectUser(t, store, project.ID, "rollback", db.CanRollbackPolicyGuardrails)
	admin, err := store.CreateUserWithoutPassword(db.User{Username: "policy-guardrail-admin", Name: "Admin", Email: "policy-guardrail-admin@example.test", Admin: true})
	require.NoError(t, err)

	router := rootapi.Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, policyGuardrailPermissionLogWriter{}, nil, metrics.NewMetrics(), nil, nil)
	tokens := map[int]string{}
	for _, user := range []db.User{globalManage, globalRollback, projectManage, projectRollback, admin} {
		token := "policy-token-" + strconv.Itoa(user.ID)
		_, tokenErr := store.CreateAPIToken(db.APIToken{ID: token, UserID: user.ID, Name: token})
		require.NoError(t, tokenErr)
		tokens[user.ID] = token
	}
	serve := func(user db.User, method, target string) int {
		request := httptest.NewRequest(method, target, nil)
		request.Header.Set("Authorization", "Bearer "+tokens[user.ID])
		request = helpers.SetContextValue(request, "store", store)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response.Code
	}

	// The service is nil in this transport test: GET reaches its 503 boundary;
	// the body-free rollback request reaches controller validation (400). A 403
	// proves the independent permission stopped the request before either.
	assert.Equal(t, http.StatusServiceUnavailable, serve(globalManage, http.MethodGet, "/api/policy-guardrails"))
	assert.Equal(t, http.StatusForbidden, serve(globalManage, http.MethodPost, "/api/policy-guardrails/rollback"))
	assert.Equal(t, http.StatusForbidden, serve(globalRollback, http.MethodGet, "/api/policy-guardrails"))
	assert.Equal(t, http.StatusBadRequest, serve(globalRollback, http.MethodPost, "/api/policy-guardrails/rollback"))
	assert.Equal(t, http.StatusServiceUnavailable, serve(admin, http.MethodGet, "/api/policy-guardrails"))

	projectBase := "/api/project/" + strconv.Itoa(project.ID) + "/policy-guardrails"
	assert.Equal(t, http.StatusServiceUnavailable, serve(projectManage, http.MethodGet, projectBase))
	assert.Equal(t, http.StatusForbidden, serve(projectManage, http.MethodPost, projectBase+"/rollback"))
	assert.Equal(t, http.StatusForbidden, serve(projectRollback, http.MethodGet, projectBase))
	assert.Equal(t, http.StatusBadRequest, serve(projectRollback, http.MethodPost, projectBase+"/rollback"))
}

func createPolicyGuardrailGlobalUser(t *testing.T, store *coresql.SqlDb, suffix string, permissions db.GlobalPermission) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{Username: "policy-global-" + suffix, Name: suffix, Email: "policy-global-" + suffix + "@example.test"})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{ID: db.ProjectRoleID("policy_global_" + suffix), Slug: "policy_global_" + suffix, Name: suffix, GlobalPermissions: permissions, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{UserID: user.ID, RoleID: role.ID, Revision: 1})
	require.NoError(t, err)
	return user
}

func createPolicyGuardrailProjectUser(t *testing.T, store *coresql.SqlDb, projectID int, suffix string, permissions db.ProjectUserPermission) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{Username: "policy-project-" + suffix, Name: suffix, Email: "policy-project-" + suffix + "@example.test"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: db.ProjectRoleID("policy_project_" + suffix), Name: suffix, ProjectID: &projectID, Permissions: permissions, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	return user
}
