package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTriggerChangesRecordTheActingUserAsCredentialPrincipal(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "principal project"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "none", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "deploy", Playbook: "site.yml"})
	require.NoError(t, err)
	schedule, err := store.CreateSchedule(db.Schedule{ProjectID: project.ID, TemplateID: template.ID, Name: "nightly", CronFormat: "* * * * *", Type: db.ScheduleTypeCron, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	integration, err := store.CreateIntegration(db.Integration{ProjectID: project.ID, TemplateID: &template.ID, Name: "hook", AuthMethod: db.IntegrationAuthNone, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "trigger-editor", Name: "Editor", Email: "trigger-editor@example.test"})
	require.NoError(t, err)
	request := helpers.SetContextValue(helpers.SetContextValue(httptest.NewRequest(http.MethodPut, "/", nil), "store", store), "user", &user)

	recordScheduleCredentialPrincipal(request, schedule.ID)
	recordIntegrationCredentialPrincipal(request, integration.ID)

	for name, task := range map[string]db.Task{
		"schedule":    {ProjectID: project.ID, ScheduleID: &schedule.ID},
		"integration": {ProjectID: project.ID, IntegrationID: &integration.ID},
	} {
		t.Run(name, func(t *testing.T) {
			principal, ok, err := store.GetTaskCredentialPrincipal(task)
			require.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, user.ID, principal)
		})
	}
}
