package projects

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskStartByTemplateNameUsesResolvedTemplatePermission(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	allowed := createTemplateNamePermissionUser(t, store, project.ID, db.ProjectGuest, "template-name-allowed")
	denied := createTemplateNamePermissionUser(t, store, project.ID, db.ProjectManager, "template-name-denied")
	_, err := store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID, RoleSlug: string(db.ProjectGuest),
		AllowedPermissions: db.CanRunTemplate, DeniedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID, RoleSlug: string(db.ProjectManager),
		DeniedPermissions: db.CanRunTemplate, Revision: 1,
	})
	require.NoError(t, err)

	controller := NewTaskController(store, nil)
	for _, tc := range []struct {
		name string
		user db.User
		want int
	}{
		{name: "allowed", user: allowed, want: http.StatusNoContent},
		{name: "denied", user: denied, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			handler := ProjectMiddleware(controller.NewTaskMiddleware(controller.GetTaskPermissionsMiddleware(
				GetMustCanMiddleware(db.CanRunProjectTasks)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reached = true
					resolved := helpers.GetFromContext(r, "task").(db.Task)
					assert.Equal(t, template.ID, resolved.TemplateID)
					assert.Empty(t, resolved.TemplateName)
					w.WriteHeader(http.StatusNoContent)
				})),
			)))
			req := httptest.NewRequest(http.MethodPost, "/api/project/1/tasks", bytes.NewBufferString(`{"template_name":"Protected template"}`))
			req = mux.SetURLVars(req, map[string]string{"project_id": strconv.Itoa(project.ID)})
			req = helpers.SetContextValue(req, "store", store)
			req = helpers.SetContextValue(req, "user", &tc.user)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			assert.Equal(t, tc.want, response.Code, response.Body.String())
			assert.Equal(t, tc.want == http.StatusNoContent, reached)
		})
	}
}

func createTemplateNamePermissionUser(t *testing.T, store *coresql.SqlDb, projectID int, role db.ProjectUserRole, username string) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{Username: username, Name: username, Email: username + "@example.test"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, Role: role})
	require.NoError(t, err)
	return user
}
