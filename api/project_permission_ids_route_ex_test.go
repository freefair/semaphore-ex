package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectPermissionIDsRouterContracts(t *testing.T) {
	store := setupSessionTest(t)
	t.Cleanup(store.Close)
	util.Config.Debugging = &util.DebuggingConfig{}
	project, err := store.CreateProject(db.Project{Name: "permission IDs router"})
	require.NoError(t, err)
	admin, err := store.CreateUserWithoutPassword(db.User{Username: "permission-ids-admin", Name: "Permission IDs Admin", Email: "permission-ids-admin@example.test", Admin: true})
	require.NoError(t, err)
	member, err := store.CreateUserWithoutPassword(db.User{Username: "permission-ids-member", Name: "Permission IDs Member", Email: "permission-ids-member@example.test"})
	require.NoError(t, err)
	mappedRole, err := store.CreateProjectRole(db.Role{
		ID: "permission_ids_mapped", Name: "Permission IDs mapped", ProjectID: &project.ID,
		Permissions: db.CanDeleteViews, Revision: 1,
	})
	require.NoError(t, err)
	mapping, err := store.SaveOIDCGroupMapping(db.OIDCGroupMapping{
		ID: "permission-ids-mapping", ProviderID: "permission-ids", ClaimValue: "operators", TargetScope: "project",
		ProjectID: &project.ID, RoleID: string(mappedRole.ID), Enabled: true, Revision: 1,
		Created: time.Now().UTC(), Updated: time.Now().UTC(),
	}, 0)
	require.NoError(t, err)
	revision, err := store.GetOIDCGroupMappingRevision(mapping.ProviderID)
	require.NoError(t, err)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: mapping.ProviderID, UserID: member.ID, Source: "test", Status: "applied",
		Token: "permission-ids", MappingRevision: revision, ClaimRevision: "test", PreviewJSON: "{}", Created: time.Now().UTC(),
	}, []db.OIDCGroupAssignmentChange{{
		MappingID: mapping.ID, UserID: member.ID, TargetScope: "project", ProjectID: &project.ID, RoleID: string(mappedRole.ID),
	}}, nil)
	require.NoError(t, err)

	for token, user := range map[string]db.User{"permission-ids-admin-token": admin, "permission-ids-member-token": member} {
		_, err = store.CreateAPIToken(db.APIToken{ID: token, UserID: user.ID, Name: "permission IDs router"})
		require.NoError(t, err)
	}
	router := Route(store, nil, nil, nil, poolWithTasks(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, proServer.NewLogWriteService(), nil, metrics.NewMetrics(), nil, nil)
	request := func(method, target, token string, body any) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			encoded, err = json.Marshal(body)
			require.NoError(t, err)
		}
		req := httptest.NewRequest(method, target, bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req = helpers.SetContextValue(req, "store", store)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	assertPermissionIDs := func(response *httptest.ResponseRecorder, want []string) {
		var payload map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), response.Body.String())
		assert.Equal(t, stringsToAny(want), payload["permission_ids"])
	}
	assertListedRolePermissionIDs := func(response *httptest.ResponseRecorder, roleID string, want []string) {
		var payload []map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), response.Body.String())
		for _, role := range payload {
			if role["id"] == roleID {
				assert.Equal(t, stringsToAny(want), role["permission_ids"])
				return
			}
		}
		require.Failf(t, "role not listed", "role %s was absent from %s", roleID, response.Body.String())
	}

	memberRole := request(http.MethodGet, "/api/project/"+strconv.Itoa(project.ID)+"/role", "permission-ids-member-token", nil)
	require.Equal(t, http.StatusOK, memberRole.Code, memberRole.Body.String())
	assertPermissionIDs(memberRole, []string{"project.views.delete"})
	adminRole := request(http.MethodGet, "/api/project/"+strconv.Itoa(project.ID)+"/role", "permission-ids-admin-token", nil)
	require.Equal(t, http.StatusOK, adminRole.Code, adminRole.Body.String())
	allPermissionIDs := make([]string, 0)
	for _, definition := range pro_interfaces.ProjectPermissionCatalog() {
		allPermissionIDs = append(allPermissionIDs, string(definition.ID))
	}
	assertPermissionIDs(adminRole, allPermissionIDs)

	team := request(http.MethodGet, "/api/project/"+strconv.Itoa(project.ID)+"/users", "permission-ids-admin-token", nil)
	require.Equal(t, http.StatusOK, team.Code, team.Body.String())
	var members []map[string]any
	require.NoError(t, json.Unmarshal(team.Body.Bytes(), &members))
	for _, entry := range members {
		if entry["id"] == float64(member.ID) {
			assert.Equal(t, stringsToAny([]string{"project.views.delete"}), entry["permission_ids"])
		}
	}

	createdProject := request(http.MethodPost, "/api/project/"+strconv.Itoa(project.ID)+"/roles", "permission-ids-admin-token", map[string]any{
		"name": "ID project role", "permission_ids": []string{"project.views.delete", "project.repositories.view"},
	})
	require.Equal(t, http.StatusCreated, createdProject.Code, createdProject.Body.String())
	assertPermissionIDs(createdProject, []string{"project.repositories.view", "project.views.delete"})
	var projectRole map[string]any
	require.NoError(t, json.Unmarshal(createdProject.Body.Bytes(), &projectRole))
	projectRoleID := projectRole["id"].(string)

	createdGlobal := request(http.MethodPost, "/api/roles", "permission-ids-admin-token", map[string]any{
		"name": "ID global role", "permission_ids": []string{"project.views.delete"},
	})
	require.Equal(t, http.StatusCreated, createdGlobal.Code, createdGlobal.Body.String())
	assertPermissionIDs(createdGlobal, []string{"project.views.delete"})
	var globalRole map[string]any
	require.NoError(t, json.Unmarshal(createdGlobal.Body.Bytes(), &globalRole))
	globalRoleID := globalRole["id"].(string)

	for _, testCase := range []struct {
		name   string
		method string
		path   string
		body   any
		roleID string
		list   bool
		want   []string
	}{
		{name: "list project roles", method: http.MethodGet, path: "/api/project/" + strconv.Itoa(project.ID) + "/roles", roleID: projectRoleID, list: true, want: []string{"project.repositories.view", "project.views.delete"}},
		{name: "get project role", method: http.MethodGet, path: "/api/project/" + strconv.Itoa(project.ID) + "/roles/" + projectRoleID, roleID: projectRoleID, want: []string{"project.repositories.view", "project.views.delete"}},
		{name: "update project role", method: http.MethodPut, path: "/api/project/" + strconv.Itoa(project.ID) + "/roles/" + projectRoleID, body: map[string]any{"name": "Updated ID project role", "revision": 1, "permission_ids": []string{"project.tasks.run", "project.views.delete"}}, want: []string{"project.tasks.run", "project.views.delete"}},
		{name: "list global roles", method: http.MethodGet, path: "/api/roles", roleID: globalRoleID, list: true, want: []string{"project.views.delete"}},
		{name: "get global role", method: http.MethodGet, path: "/api/roles/" + globalRoleID, want: []string{"project.views.delete"}},
		{name: "update global role", method: http.MethodPut, path: "/api/roles/" + globalRoleID, body: map[string]any{"name": "Updated ID global role", "revision": 1, "permission_ids": []string{"project.tasks.run", "project.views.delete"}}, want: []string{"project.tasks.run", "project.views.delete"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := request(testCase.method, testCase.path, "permission-ids-admin-token", testCase.body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			if testCase.list {
				assertListedRolePermissionIDs(response, testCase.roleID, testCase.want)
				return
			}
			assertPermissionIDs(response, testCase.want)
		})
	}

	for _, testCase := range []struct {
		name string
		body map[string]any
		want string
	}{
		{name: "conflicting numeric mask", body: map[string]any{"name": "conflict", "permissions": db.CanUpdateProject, "permission_ids": []string{"project.tasks.run"}}, want: "conflicts with permission_ids"},
		{name: "unknown ID", body: map[string]any{"name": "unknown", "permission_ids": []string{"project.unknown"}}, want: "project.unknown"},
		{name: "retired ID", body: map[string]any{"name": "retired", "permission_ids": []string{"project.resources.view"}}, want: "project.resources.view"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := request(http.MethodPost, "/api/project/"+strconv.Itoa(project.ID)+"/roles", "permission-ids-admin-token", testCase.body)
			assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), testCase.want)
		})
	}
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
