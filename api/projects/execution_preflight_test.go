package projects

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type executionPreflightAuditStub struct{ events []pro_interfaces.AuditEvent }

func (s *executionPreflightAuditStub) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestTaskExecutionPreflightControllerReturnsValueFreePlan(t *testing.T) {
	store, pool, project, actor, template, _ := createTaskExecutionPreflightControllerFixture(t)
	controller := NewTaskController(store, nil)
	request := taskExecutionPreflightRequest(http.MethodPost, project, actor, template, &pool)
	recorder := httptest.NewRecorder()

	controller.PreviewTask(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var plan pro_interfaces.ExecutionPreflightPlan
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &plan))
	assert.NotEmpty(t, plan.ReviewToken)
	assert.Equal(t, template.ID, plan.TemplateID)
	assert.NotEmpty(t, plan.Commands)
	for _, forbidden := range []string{"repository-secret", "private.example.invalid", "runner-secret", "runner.invalid/hook"} {
		assert.NotContains(t, recorder.Body.String(), forbidden)
	}
	stored, err := store.GetProjectTasks(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestTaskExecutionPreflightPreviewRecordsOnlyAllowlistedAuditProvenance(t *testing.T) {
	store, pool, project, actor, template, _ := createTaskExecutionPreflightControllerFixture(t)
	controller := NewTaskController(store, nil)
	audit := &executionPreflightAuditStub{}
	controller.ConfigureExecutionPreflightAudit(audit)
	request := taskExecutionPreflightRequest(http.MethodPost, project, actor, template, &pool)
	request = helpers.SetContextValue(request, "task", db.Task{TemplateID: template.ID, Secret: `{"secret":"must-not-audit"}`})
	response := httptest.NewRecorder()

	controller.PreviewTask(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Len(t, audit.events, 1)
	event := audit.events[0]
	assert.Equal(t, pro_interfaces.AuditActionExecutionPreflightPreview, event.Action)
	assert.Equal(t, "task-template:"+strconv.Itoa(template.ID), event.TargetID)
	require.NoError(t, event.Validate())
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "must-not-audit")
	assert.NotContains(t, string(payload), "repository-secret")
}

func TestTaskExecutionPreflightReadGateHidesPlanFromRunOnlyUser(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "preflight-run-only", Name: "Preflight Run Only", Email: "preflight-run-only@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID, RoleSlug: string(db.ProjectGuest),
		AllowedPermissions: db.CanRunTemplate, DeniedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)
	reached := false
	handler := NewTaskController(store, nil).GetTaskReadPermissionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/project/1/tasks/preflight", nil)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &user)
	request = helpers.SetContextValue(request, "task", db.Task{TemplateID: template.ID})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.False(t, reached)
}

func TestTaskExecutionPreflightControllerRejectsStalePlanBeforeInsert(t *testing.T) {
	store, pool, project, actor, template, repository := createTaskExecutionPreflightControllerFixture(t)
	controller := NewTaskController(store, nil)
	previewRecorder := httptest.NewRecorder()
	controller.PreviewTask(previewRecorder, taskExecutionPreflightRequest(http.MethodPost, project, actor, template, &pool))
	require.Equal(t, http.StatusOK, previewRecorder.Code)
	var plan pro_interfaces.ExecutionPreflightPlan
	require.NoError(t, json.Unmarshal(previewRecorder.Body.Bytes(), &plan))

	repository.GitBranch = "changed-after-review"
	require.NoError(t, store.UpdateRepository(repository))
	start := taskExecutionPreflightRequest(http.MethodPost, project, actor, template, &pool)
	start.Header.Set(taskPreflightFingerprintHeader, plan.Fingerprint)
	start.Header.Set(taskPreflightTokenHeader, plan.ReviewToken)
	startRecorder := httptest.NewRecorder()

	controller.AddTask(startRecorder, start)

	assert.Equal(t, http.StatusConflict, startRecorder.Code, startRecorder.Body.String())
	assert.Contains(t, startRecorder.Body.String(), `"code":"stale_execution_preflight"`)
	assert.Contains(t, startRecorder.Body.String(), `"reference_changed"`)
	stored, err := store.GetProjectTasks(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestTaskExecutionPreflightControllerReturnsCoarseDeploymentWindowConflict(t *testing.T) {
	controller := NewTaskController(nil, nil)
	response := httptest.NewRecorder()
	nextEligible := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	handled := controller.writeTaskExecutionPreflightError(
		response,
		httptest.NewRequest(http.MethodPost, "/api/project/7/tasks", nil),
		db.Task{},
		pro_interfaces.ExecutionPreflightPlan{},
		&pro_interfaces.DeploymentWindowBlockedError{
			DecisionID: 42, Reason: pro_interfaces.DeploymentWindowReasonFreezeActive,
			NextEligibleAt: &nextEligible, NextEligibleKnown: true,
		},
	)

	assert.True(t, handled)
	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), `"state":"blocked"`)
	assert.Contains(t, response.Body.String(), `"reason":"freeze_active"`)
	assert.NotContains(t, response.Body.String(), "decision_id")
	assert.NotContains(t, response.Body.String(), "42")
}

func TestTaskExecutionPreflightControllerMapsDeploymentWindowOverrideFailures(t *testing.T) {
	controller := NewTaskController(nil, nil)
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"invalid", pro_interfaces.ErrDeploymentWindowOverrideInvalid, http.StatusBadRequest},
		{"forbidden", pro_interfaces.ErrDeploymentWindowOverrideForbidden, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handled := controller.writeTaskExecutionPreflightError(response, httptest.NewRequest(http.MethodPost, "/api/project/7/tasks", nil), db.Task{}, pro_interfaces.ExecutionPreflightPlan{}, tc.err)
			assert.True(t, handled)
			assert.Equal(t, tc.code, response.Code)
			assert.NotContains(t, response.Body.String(), "reference")
		})
	}
}

func TestTaskOverrideCannotBeSilentlyIgnoredWithoutAdmissionService(t *testing.T) {
	_, pool, project, actor, template, _ := createTaskExecutionPreflightControllerFixture(t)
	_, _, err := pool.AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride(
		db.Task{TemplateID: template.ID}, &actor, project.ID, template.App.NeedTaskAlias(),
		pro_interfaces.ExecutionPreflightReview{},
		&pro_interfaces.DeploymentWindowOverrideInput{
			Category: pro_interfaces.DeploymentWindowOverrideIncident, Reference: "INC-41",
		},
	)
	assert.ErrorIs(t, err, pro_interfaces.ErrDeploymentWindowOverrideForbidden)
}

func createTaskExecutionPreflightControllerFixture(t *testing.T) (*sql.SqlDb, tasks.TaskPool, db.Project, db.User, db.Template, db.Repository) {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "preflight api"})
	require.NoError(t, err)
	actor, err := store.CreateUserWithoutPassword(db.User{
		Username: "preflight-api", Name: "Preflight API", Email: "preflight-api@example.invalid",
	})
	require.NoError(t, err)
	keySecret := "repository-secret"
	key, err := store.CreateAccessKey(db.AccessKey{
		ProjectID: &project.ID, Name: "repository key", Type: db.AccessKeyString, Secret: &keySecret,
	})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repository",
		GitURL: "https://private.example.invalid/repository.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "deploy",
		App: db.AppBash, Playbook: "deploy.sh", RunnerTags: []string{"linux"},
	})
	require.NoError(t, err)
	_, err = store.CreateRunner(db.Runner{
		ProjectID: &project.ID, Name: "runner", Active: true, Token: "runner-secret",
		Tags: []string{"linux"}, Webhook: "https://runner.invalid/hook",
	})
	require.NoError(t, err)
	pool := tasks.CreateTaskPool(store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)
	issuer, err := tasks.NewExecutionPreflightReviewTokenIssuer([]byte(strings.Repeat("k", 32)), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	return store, pool, project, actor, template, repository
}

func taskExecutionPreflightRequest(
	method string,
	project db.Project,
	actor db.User,
	template db.Template,
	pool *tasks.TaskPool,
) *http.Request {
	request := httptest.NewRequest(method, "/api/project/1/tasks/preflight", nil)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &actor)
	request = helpers.SetContextValue(request, "task", db.Task{TemplateID: template.ID})
	request = helpers.SetContextValue(request, "task_pool", pool)
	return request
}
