package projects

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
	communityserver "github.com/semaphoreui/semaphore/community-pro/services/server"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerAuditRecorder struct {
	events []pro_interfaces.AuditEvent
}

type deniedRunnerProvider struct{}

func (deniedRunnerProvider) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityProjectRunners,
			pro_interfaces.CapabilityStateDisabled,
			pro_interfaces.CapabilityReasonDisabledByAdmin,
			nil,
			nil,
		),
	}), nil
}

func (deniedRunnerProvider) Configure(
	context.Context,
	pro_interfaces.CapabilityRequest,
	pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	panic("not used")
}

func (r *runnerAuditRecorder) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func TestProjectRunnerCreateReturnsRegistrationTokenOnceAndListsOnlyOriginProject(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "origin"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	audit := &runnerAuditRecorder{}
	controller := NewProjectRunnerController(
		communityserver.NewSubscriptionService(nil, nil, nil, nil),
		server.NewRunnerService(store),
		features.NewCapabilityProvider(store),
		audit,
	)

	create := httptest.NewRequest(http.MethodPost, "/api/project/1/runners", bytes.NewBufferString(`{"name":"isolated"}`))
	create = runnerContractRequest(create, store, project)
	created := httptest.NewRecorder()
	controller.AddRunner(created, create)

	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		db.Runner
		RegistrationToken string `json:"registration_token"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	assert.NotEmpty(t, response.RegistrationToken)
	assert.Equal(t, project.ID, *response.ProjectID)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionProjectRunnerCreate, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditOutcomeAllowed, audit.events[0].Outcome)
	require.NotNil(t, audit.events[0].ProjectID)
	assert.Equal(t, project.ID, *audit.events[0].ProjectID)

	originList := httptest.NewRecorder()
	originRequest := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/1/runners", nil), store, project)
	controller.GetRunners(originList, originRequest)
	require.Equal(t, http.StatusOK, originList.Code)
	assert.Contains(t, originList.Body.String(), "isolated")
	assert.NotContains(t, originList.Body.String(), response.RegistrationToken)

	otherList := httptest.NewRecorder()
	otherRequest := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/2/runners", nil), store, other)
	controller.GetRunners(otherList, otherRequest)
	require.Equal(t, http.StatusOK, otherList.Code)
	assert.JSONEq(t, `[]`, otherList.Body.String())
}

func TestProjectRunnerMiddlewareRejectsCrossProjectLookup(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	origin, err := store.CreateProject(db.Project{Name: "origin"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	runner, _, err := server.NewRunnerService(store).CreateProjectRunner(db.Runner{
		Name: "isolated", ProjectID: &origin.ID,
	})
	require.NoError(t, err)
	audit := &runnerAuditRecorder{}
	controller := NewProjectRunnerController(nil, server.NewRunnerService(store), features.NewCapabilityProvider(store), audit)
	handler := controller.RunnerMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cross-project runner must not reach the handler")
	}))
	request := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/2/runners/1", nil), store, other)
	request = mux.SetURLVars(request, map[string]string{"project_id": "2", "runner_id": "1"})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
	assert.Equal(t, "runner:"+strconv.Itoa(runner.ID), audit.events[0].TargetID)
	require.NotNil(t, audit.events[0].ProjectID)
	assert.Equal(t, other.ID, *audit.events[0].ProjectID)
}

func TestProjectRunnerCapabilityIsRequiredByBackend(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "guarded"})
	require.NoError(t, err)
	controller := NewProjectRunnerController(nil, server.NewRunnerService(store), nil, nil)
	request := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/1/runners", nil), store, project)
	response := httptest.NewRecorder()

	controller.GetRunners(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestProjectRunnerCapabilityDenialIsAudited(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "denied"})
	require.NoError(t, err)
	audit := &runnerAuditRecorder{}
	controller := NewProjectRunnerController(nil, server.NewRunnerService(store), deniedRunnerProvider{}, audit)
	request := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/1/runners", nil), store, project)
	response := httptest.NewRecorder()

	controller.GetRunners(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionProjectRunnerList, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
	assert.Equal(t, string(pro_interfaces.CapabilityReasonDisabledByAdmin), audit.events[0].Reason)
}

func TestProjectRunnerLifecycleMutationsPersistAndAreAudited(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "lifecycle"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name: "before", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	audit := &runnerAuditRecorder{}
	controller := NewProjectRunnerController(nil, server.NewRunnerService(store), features.NewCapabilityProvider(store), audit)

	update := runnerLifecycleRequest(httptest.NewRequest(http.MethodPut, "/api/project/1/runners/1",
		bytes.NewBufferString(`{"name":"after","tags":["linux"],"is_default":true,"max_parallel_tasks":3}`)), store, project, runner)
	updated := httptest.NewRecorder()
	controller.UpdateRunner(updated, update)
	require.Equal(t, http.StatusNoContent, updated.Code, updated.Body.String())
	stored, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.Equal(t, "after", stored.Name)
	assert.Equal(t, []string{"linux"}, stored.Tags)
	assert.True(t, stored.Active)

	deactivate := runnerLifecycleRequest(httptest.NewRequest(http.MethodPost, "/api/project/1/runners/1/active",
		bytes.NewBufferString(`{"active":false}`)), store, project, stored)
	deactivated := httptest.NewRecorder()
	controller.SetRunnerActive(deactivated, deactivate)
	require.Equal(t, http.StatusNoContent, deactivated.Code, deactivated.Body.String())
	stored, err = store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.False(t, stored.Active)

	for range 2 {
		clear := runnerLifecycleRequest(httptest.NewRequest(http.MethodDelete, "/api/project/1/runners/1/cache", nil), store, project, stored)
		cleared := httptest.NewRecorder()
		controller.ClearRunnerCache(cleared, clear)
		require.Equal(t, http.StatusNoContent, cleared.Code, cleared.Body.String())
	}
	stored, err = store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.NotNil(t, stored.CleaningRequested)

	issue := runnerLifecycleRequest(httptest.NewRequest(http.MethodPost, "/api/project/1/runners/1/registration-token", nil), store, project, stored)
	issued := httptest.NewRecorder()
	controller.RegenerateRegistrationToken(issued, issue)
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	assert.Contains(t, issued.Body.String(), server.RunnerRegistrationTokenPrefix)
	stored, err = store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.Token)
	assert.False(t, stored.Active)

	remove := runnerLifecycleRequest(httptest.NewRequest(http.MethodDelete, "/api/project/1/runners/1", nil), store, project, stored)
	removed := httptest.NewRecorder()
	controller.DeleteRunner(removed, remove)
	require.Equal(t, http.StatusNoContent, removed.Code, removed.Body.String())
	_, err = store.GetRunner(project.ID, runner.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	actions := make([]pro_interfaces.AuditAction, len(audit.events))
	for index := range audit.events {
		actions[index] = audit.events[index].Action
		assert.Equal(t, pro_interfaces.AuditOutcomeAllowed, audit.events[index].Outcome)
	}
	assert.Equal(t, []pro_interfaces.AuditAction{
		pro_interfaces.AuditActionProjectRunnerUpdate,
		pro_interfaces.AuditActionProjectRunnerActive,
		pro_interfaces.AuditActionProjectRunnerCache,
		pro_interfaces.AuditActionProjectRunnerCache,
		pro_interfaces.AuditActionProjectRunnerIssue,
		pro_interfaces.AuditActionProjectRunnerDelete,
	}, actions)
}

func TestProjectRunnerDestructiveMutationsReturnAssignmentConflict(t *testing.T) {
	for name, invoke := range map[string]func(pro_interfaces.ProjectRunnerController, http.ResponseWriter, *http.Request){
		"deactivate": func(controller pro_interfaces.ProjectRunnerController, response http.ResponseWriter, request *http.Request) {
			controller.SetRunnerActive(response, request)
		},
		"re-register": func(controller pro_interfaces.ProjectRunnerController, response http.ResponseWriter, request *http.Request) {
			controller.RegenerateRegistrationToken(response, request)
		},
		"delete": func(controller pro_interfaces.ProjectRunnerController, response http.ResponseWriter, request *http.Request) {
			controller.DeleteRunner(response, request)
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, project, runner, task := createBusyProjectRunnerControllerFixture(t)
			audit := &runnerAuditRecorder{}
			controller := NewProjectRunnerController(nil, server.NewRunnerService(store), features.NewCapabilityProvider(store), audit)
			method, path, body := http.MethodDelete, "/api/project/1/runners/1", ""
			switch name {
			case "deactivate":
				method, path, body = http.MethodPost, "/api/project/1/runners/1/active", `{"active":false}`
			case "re-register":
				method, path = http.MethodPost, "/api/project/1/runners/1/registration-token"
			}
			request := runnerLifecycleRequest(httptest.NewRequest(method, path, bytes.NewBufferString(body)), store, project, runner)
			response := httptest.NewRecorder()

			invoke(controller, response, request)

			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			var payload struct {
				Error       string                    `json:"error"`
				RunnerID    int                       `json:"runner_id"`
				Assignments []db.RunnerTaskAssignment `json:"assignments"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
			assert.Equal(t, "PROJECT_RUNNER_ASSIGNMENTS_ACTIVE", payload.Error)
			assert.Equal(t, runner.ID, payload.RunnerID)
			require.Len(t, payload.Assignments, 1)
			assert.Equal(t, task.ID, payload.Assignments[0].TaskID)
			stored, err := store.GetRunner(project.ID, runner.ID)
			require.NoError(t, err)
			assert.True(t, stored.Active)
			assert.NotEmpty(t, stored.Token)
			require.Len(t, audit.events, 1)
			assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
			assert.Equal(t, pro_interfaces.AuditReasonActiveAssignments, audit.events[0].Reason)
		})
	}
}

func createBusyProjectRunnerControllerFixture(t *testing.T) (*sql.SqlDb, db.Project, db.Runner, db.Task) {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "busy runner"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, Name: "repo", GitURL: "https://example.com/repo.git", GitBranch: "main", SSHKeyID: key.ID,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "runner lifecycle", Playbook: "site.yml",
	})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name: "busy", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		TemplateID: template.ID, ProjectID: project.ID, Status: task_logger.TaskRunningStatus,
		Playbook: "site.yml", RunnerID: &runner.ID, Created: time.Now(),
	}, 0)
	require.NoError(t, err)
	return store, project, runner, task
}

func runnerLifecycleRequest(request *http.Request, store db.Store, project db.Project, runner db.Runner) *http.Request {
	request = runnerContractRequest(request, store, project)
	return helpers.SetContextValue(request, "runner", &runner)
}

func runnerContractRequest(request *http.Request, store db.Store, project db.Project) *http.Request {
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 1})
	return request
}
