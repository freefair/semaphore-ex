package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharedTaskGroupMutationsRequireOwnerProjectRoutePermission(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	owner, err := store.CreateProject(db.Project{Name: "group owner"})
	require.NoError(t, err)
	consumer, err := store.CreateProject(db.Project{Name: "group consumer"})
	require.NoError(t, err)
	group, err := store.CreateTaskGroup(db.TaskGroup{
		ProjectID: owner.ID, Name: "shared deployment", Description: "original", MaxParallelTasks: 1,
		SharedProjectIDs: db.TaskGroupBindings{consumer.ID},
	})
	require.NoError(t, err)
	controller := NewTaskGroupController(store)
	updateHandler := GetMustHavePermissionMiddleware(db.CanUpdateTaskGroups)(http.HandlerFunc(controller.UpdateTaskGroup))

	update := func(project db.Project, permissions db.ProjectUserPermission, description string) *httptest.ResponseRecorder {
		visible, getErr := store.GetTaskGroup(project.ID, group.ID)
		require.NoError(t, getErr)
		visible.Description = description
		body, marshalErr := json.Marshal(visible)
		require.NoError(t, marshalErr)
		req := httptest.NewRequest(http.MethodPut, "/api/project/"+strconv.Itoa(project.ID)+"/task_groups/"+strconv.Itoa(group.ID), strings.NewReader(string(body)))
		req = mux.SetURLVars(req, map[string]string{"group_id": strconv.Itoa(group.ID)})
		req = helpers.SetContextValue(req, "project", project)
		req = helpers.SetContextValue(req, "permissions", permissions)
		req = helpers.SetContextValue(req, "user", &db.User{ID: 1})
		response := httptest.NewRecorder()
		updateHandler.ServeHTTP(response, req)
		return response
	}

	// A consumer can resolve the share but the consumer project is never an
	// owner mutation scope, even when its role has update permission there.
	consumerResponse := update(consumer, db.CanUpdateTaskGroups, "consumer attempt")
	assert.NotEqual(t, http.StatusNoContent, consumerResponse.Code)
	persisted, err := store.GetTaskGroup(owner.ID, group.ID)
	require.NoError(t, err)
	assert.Equal(t, "original", persisted.Description)

	deleteHandler := GetMustHavePermissionMiddleware(db.CanDeleteTaskGroups)(http.HandlerFunc(controller.DeleteTaskGroup))
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/project/"+strconv.Itoa(consumer.ID)+"/task_groups/"+strconv.Itoa(group.ID), strings.NewReader(`{"revision":`+strconv.Itoa(group.Revision)+`}`))
	deleteRequest = mux.SetURLVars(deleteRequest, map[string]string{"group_id": strconv.Itoa(group.ID)})
	deleteRequest = helpers.SetContextValue(deleteRequest, "project", consumer)
	deleteRequest = helpers.SetContextValue(deleteRequest, "permissions", db.CanDeleteTaskGroups)
	deleteRequest = helpers.SetContextValue(deleteRequest, "user", &db.User{ID: 1})
	deleteResponse := httptest.NewRecorder()
	deleteHandler.ServeHTTP(deleteResponse, deleteRequest)
	assert.NotEqual(t, http.StatusNoContent, deleteResponse.Code)
	_, err = store.GetTaskGroup(owner.ID, group.ID)
	require.NoError(t, err)

	// Routing to the owner project does not borrow a consumer's permission.
	ownerDeniedResponse := update(owner, 0, "unprivileged owner route")
	assert.Equal(t, http.StatusForbidden, ownerDeniedResponse.Code)
	persisted, err = store.GetTaskGroup(owner.ID, group.ID)
	require.NoError(t, err)
	assert.Equal(t, "original", persisted.Description)

	ownerResponse := update(owner, db.CanUpdateTaskGroups, "owner update")
	require.Equal(t, http.StatusNoContent, ownerResponse.Code, ownerResponse.Body.String())
	persisted, err = store.GetTaskGroup(owner.ID, group.ID)
	require.NoError(t, err)
	assert.Equal(t, "owner update", persisted.Description)
}
