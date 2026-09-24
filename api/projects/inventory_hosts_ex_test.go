package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	prosql "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostAPIHonorsTemplateVisibility(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	actor, err := store.CreateUserWithoutPassword(db.User{Username: "host-reader", Name: "Host reader", Email: "host-reader@example.invalid"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: actor.ID, Role: db.ProjectGuest})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{ProjectID: project.ID, Name: "hosts", Type: db.InventoryStatic, Inventory: "web"})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{ProjectID: project.ID, TemplateID: template.ID, UserID: &actor.ID, Created: time.Now(), Status: task_logger.TaskSuccessStatus}, 0)
	require.NoError(t, err)
	repository := prosql.NewAnsibleTask(store.GetConnection())
	hosts := repository.(db.InventoryHostRepository)
	require.NoError(t, hosts.IngestInventoryHostEvent(task, inventory, db.InventoryHostEvent{Version: 1, Kind: "host", Host: "web", Groups: []string{}}))
	require.NoError(t, hosts.IngestInventoryHostEvent(task, inventory, db.InventoryHostEvent{Version: 1, Kind: "complete", Count: 1}))
	controller := NewTaskController(store, repository)
	request := httptest.NewRequest(http.MethodGet, "/hosts", nil)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &actor)
	response := httptest.NewRecorder()
	controller.GetInventoryHosts(response, request)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"host":"web"`)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: project.ID, TemplateID: template.ID, RoleSlug: string(db.ProjectGuest), DeniedPermissions: db.CanReadTemplate, Revision: 1})
	require.NoError(t, err)
	response = httptest.NewRecorder()
	controller.GetInventoryHosts(response, request)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), `"host":"web"`)
}
