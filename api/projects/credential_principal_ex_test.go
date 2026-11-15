package projects

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestCredentialPrincipalMiddlewares(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "middleware project"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "none", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "deploy", Playbook: "site.yml"})
	require.NoError(t, err)
	schedule, err := store.CreateSchedule(db.Schedule{ProjectID: project.ID, TemplateID: template.ID, Name: "nightly", CronFormat: "* * * * *", Type: db.ScheduleTypeCron, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	first, err := store.CreateIntegration(db.Integration{ProjectID: project.ID, TemplateID: &template.ID, Name: "first", AuthMethod: db.IntegrationAuthNone, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	second, err := store.CreateIntegration(db.Integration{ProjectID: project.ID, TemplateID: &template.ID, Name: "second", AuthMethod: db.IntegrationAuthNone, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "principal-owner", Name: "Owner", Email: "principal-owner@example.test"})
	require.NoError(t, err)
	editor, err := store.CreateUserWithoutPassword(db.User{Username: "principal-editor", Name: "Editor", Email: "principal-editor@example.test"})
	require.NoError(t, err)
	principal := func(task db.Task) (int, bool) {
		userID, ok, err := store.GetTaskCredentialPrincipal(task)
		require.NoError(t, err)
		return userID, ok
	}
	request := func(method string, values map[string]any) *http.Request {
		r := httptest.NewRequest(method, "/", nil)
		r = helpers.SetContextValue(r, "store", store)
		r = helpers.SetContextValue(r, "user", &editor)
		r = helpers.SetContextValue(r, "permissions", db.CanUpdateSchedules|db.CanUpdateIntegrations)
		for key, value := range values {
			r = helpers.SetContextValue(r, key, value)
		}
		return r
	}
	answer := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	}
	scheduleTask := db.Task{ProjectID: project.ID, ScheduleID: &schedule.ID}
	// seed records user as the author of the last change.
	seed := func(kind db.CredentialPrincipalKind, triggerID int, user db.User) {
		token, ok, err := store.BeginCredentialPrincipalChange(kind, triggerID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, store.FinishCredentialPrincipalChange(kind, triggerID, token, user.ID))
	}

	t.Run("a successful schedule change moves the responsibility", func(t *testing.T) {
		seed(db.CredentialPrincipalSchedule, schedule.ID, owner)
		ScheduleCredentialPrincipalMiddleware(answer(http.StatusNoContent)).ServeHTTP(httptest.NewRecorder(), request(http.MethodPut, map[string]any{"schedule": schedule}))
		userID, ok := principal(scheduleTask)
		assert.True(t, ok)
		assert.Equal(t, editor.ID, userID)
	})

	t.Run("a failed schedule change leaves no principal", func(t *testing.T) {
		seed(db.CredentialPrincipalSchedule, schedule.ID, owner)
		ScheduleCredentialPrincipalMiddleware(answer(http.StatusBadRequest)).ServeHTTP(httptest.NewRecorder(), request(http.MethodPut, map[string]any{"schedule": schedule}))
		_, ok := principal(scheduleTask)
		assert.False(t, ok)
	})

	t.Run("reading changes nothing", func(t *testing.T) {
		seed(db.CredentialPrincipalSchedule, schedule.ID, owner)
		ScheduleCredentialPrincipalMiddleware(answer(http.StatusOK)).ServeHTTP(httptest.NewRecorder(), request(http.MethodGet, map[string]any{"schedule": schedule}))
		userID, _ := principal(scheduleTask)
		assert.Equal(t, owner.ID, userID)
	})

	t.Run("an integration change moves only that integration", func(t *testing.T) {
		seed(db.CredentialPrincipalIntegration, first.ID, owner)
		seed(db.CredentialPrincipalIntegration, second.ID, owner)
		IntegrationCredentialPrincipalMiddleware(answer(http.StatusOK)).ServeHTTP(httptest.NewRecorder(), request(http.MethodPost, map[string]any{"project": project, "integration": first}))
		userID, _ := principal(db.Task{ProjectID: project.ID, IntegrationID: &first.ID})
		assert.Equal(t, editor.ID, userID)
		userID, _ = principal(db.Task{ProjectID: project.ID, IntegrationID: &second.ID})
		assert.Equal(t, owner.ID, userID)
	})

	t.Run("a request the route refuses touches no principal", func(t *testing.T) {
		seed(db.CredentialPrincipalSchedule, schedule.ID, owner)
		r := request(http.MethodPut, map[string]any{"schedule": schedule})
		r = helpers.SetContextValue(r, "permissions", db.ProjectUserPermission(0))
		ScheduleCredentialPrincipalMiddleware(answer(http.StatusForbidden)).ServeHTTP(httptest.NewRecorder(), r)
		userID, ok := principal(scheduleTask)
		assert.True(t, ok)
		assert.Equal(t, owner.ID, userID)
	})

	t.Run("a concurrent change is refused", func(t *testing.T) {
		seed(db.CredentialPrincipalSchedule, schedule.ID, owner)
		token, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		called := false
		recorder := httptest.NewRecorder()
		ScheduleCredentialPrincipalMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).
			ServeHTTP(recorder, request(http.MethodPut, map[string]any{"schedule": schedule}))
		assert.Equal(t, http.StatusConflict, recorder.Code)
		assert.False(t, called, "the change must not run while another one holds the trigger")
		require.NoError(t, store.AbortCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, token))
	})

	t.Run("a project alias change moves every integration of the project", func(t *testing.T) {
		seed(db.CredentialPrincipalIntegration, first.ID, owner)
		seed(db.CredentialPrincipalIntegration, second.ID, owner)
		IntegrationCredentialPrincipalMiddleware(answer(http.StatusOK)).ServeHTTP(httptest.NewRecorder(), request(http.MethodPost, map[string]any{"project": project}))
		for _, integration := range []db.Integration{first, second} {
			userID, _ := principal(db.Task{ProjectID: project.ID, IntegrationID: &integration.ID})
			assert.Equal(t, editor.ID, userID, integration.Name)
		}
	})
}

func TestCredentialPrincipalRenewalStops(t *testing.T) {
	repo := &countingPrincipalRepository{}
	stop := renewCredentialPrincipalClaims(repo, db.CredentialPrincipalSchedule, map[int]string{1: "token"})
	stop()
	stop()
	assert.Zero(t, repo.renewals.Load(), "no renewal is due before a fifth of the lifetime")
}

type countingPrincipalRepository struct {
	db.TaskCredentialPrincipalRepository
	renewals atomic.Int32
}

func (r *countingPrincipalRepository) RenewCredentialPrincipalChange(db.CredentialPrincipalKind, int, string) error {
	r.renewals.Add(1)
	return nil
}

func TestStatusRecorderKeepsTheFirstStatus(t *testing.T) {
	recorder := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	recorder.WriteHeader(http.StatusInternalServerError)
	recorder.WriteHeader(http.StatusNoContent)
	assert.Equal(t, http.StatusInternalServerError, recorder.status)

	implicit := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	_, err := implicit.Write([]byte("ok"))
	require.NoError(t, err)
	implicit.WriteHeader(http.StatusBadRequest)
	assert.Equal(t, http.StatusOK, implicit.status, "a body without header is a 200")
}
