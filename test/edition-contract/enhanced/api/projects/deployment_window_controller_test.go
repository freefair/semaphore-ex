package projects

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deploymentWindowGovernanceStub struct {
	policy        db.DeploymentWindowPolicy
	saveErr       error
	resetErr      error
	statusErr     error
	savedProject  int
	savedRevision int
	resetProject  int
	resetRevision int
	statusRequest pro_interfaces.DeploymentWindowStatusRequest
}

type deploymentWindowAuditRecorder struct {
	events []pro_interfaces.AuditEvent
	err    error
}

func (r *deploymentWindowAuditRecorder) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	r.events = append(r.events, event)
	return r.err
}

func (s *deploymentWindowGovernanceStub) GetPolicy(context.Context, int) (db.DeploymentWindowPolicy, error) {
	return s.policy, nil
}
func (s *deploymentWindowGovernanceStub) SavePolicy(_ context.Context, policy db.DeploymentWindowPolicy, expected int) (db.DeploymentWindowPolicy, error) {
	s.savedProject, s.savedRevision = policy.ProjectID, expected
	if s.saveErr != nil {
		return db.DeploymentWindowPolicy{}, s.saveErr
	}
	return policy, nil
}
func (s *deploymentWindowGovernanceStub) ResetPolicy(_ context.Context, projectID, expected int) error {
	s.resetProject, s.resetRevision = projectID, expected
	return s.resetErr
}
func (s *deploymentWindowGovernanceStub) CurrentStatus(_ context.Context, request pro_interfaces.DeploymentWindowStatusRequest) (pro_interfaces.DeploymentWindowAdminDecision, error) {
	s.statusRequest = request
	if s.statusErr != nil {
		return pro_interfaces.DeploymentWindowAdminDecision{}, s.statusErr
	}
	return pro_interfaces.DeploymentWindowAdminDecision{
		State: pro_interfaces.DeploymentWindowDecisionBlocked, Reason: pro_interfaces.DeploymentWindowReasonFreezeActive,
		Provenance: pro_interfaces.DeploymentWindowDecisionProvenance{PolicyRevision: 99, EffectiveTimezone: "Europe/Berlin", MatchedRuleIDs: []int{5}},
	}, nil
}
func (s *deploymentWindowGovernanceStub) Preview(context.Context, db.DeploymentWindowPolicy, pro_interfaces.DeploymentWindowStatusRequest) (pro_interfaces.DeploymentWindowAdminDecision, error) {
	return pro_interfaces.DeploymentWindowAdminDecision{}, nil
}
func (s *deploymentWindowGovernanceStub) DecisionHistory(context.Context, int, db.RetrieveQueryParams) ([]pro_interfaces.DeploymentWindowDecisionHistoryDTO, error) {
	return []pro_interfaces.DeploymentWindowDecisionHistoryDTO{}, nil
}

func deploymentWindowRequest(method, target, body string, user db.User, permissions db.ProjectUserPermission) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	request = helpers.SetContextValue(request, "user", &user)
	request = helpers.SetContextValue(request, "basePermissions", permissions)
	return request
}

func TestDeploymentWindowControllerUsesRouteProjectAndCAS(t *testing.T) {
	service := &deploymentWindowGovernanceStub{}
	controller := NewDeploymentWindowController(service, nil)
	response := httptest.NewRecorder()
	request := deploymentWindowRequest(http.MethodPut, "/api/project/7/deployment-windows", `{"revision":4,"timezone":"UTC","default":"allow","rules":[]}`, db.User{ID: 1}, db.CanManageProjectResources)
	controller.SavePolicy(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 7, service.savedProject)
	assert.Equal(t, 4, service.savedRevision)

	service.saveErr = db.ErrDeploymentWindowRevisionConflict
	response = httptest.NewRecorder()
	controller.SavePolicy(response, deploymentWindowRequest(http.MethodPut, "/api/project/7/deployment-windows", `{"revision":4,"timezone":"UTC","default":"allow","rules":[]}`, db.User{ID: 1}, db.CanManageProjectResources))
	assert.Equal(t, http.StatusConflict, response.Code)

	response = httptest.NewRecorder()
	controller.ResetPolicy(response, deploymentWindowRequest(http.MethodDelete, "/api/project/7/deployment-windows?expected_revision=4", "", db.User{ID: 1}, db.CanManageProjectResources))
	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, 7, service.resetProject)
	assert.Equal(t, 4, service.resetRevision)
}

func TestDeploymentWindowPolicyMutationsRecordBoundedBestEffortAudit(t *testing.T) {
	service := &deploymentWindowGovernanceStub{}
	controller := NewDeploymentWindowController(service, nil)
	audit := &deploymentWindowAuditRecorder{}
	controller.(pro_interfaces.DeploymentWindowAuditConfigurer).ConfigureDeploymentWindowAudit(audit)
	user := db.User{ID: 1}
	response := httptest.NewRecorder()
	controller.SavePolicy(response, deploymentWindowRequest(http.MethodPut, "/api/project/7/deployment-windows", `{"revision":4,"timezone":"UTC","default":"allow","rules":[]}`, user, db.CanManageProjectResources))
	require.Equal(t, http.StatusOK, response.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionDeploymentWindowPolicyUpdate, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditReasonDeploymentWindowPolicyUpdated, audit.events[0].Reason)
	require.NoError(t, audit.events[0].Validate())

	service.resetErr = errors.New("write failed")
	response = httptest.NewRecorder()
	controller.ResetPolicy(response, deploymentWindowRequest(http.MethodDelete, "/api/project/7/deployment-windows?expected_revision=4", "", user, db.CanManageProjectResources))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Len(t, audit.events, 2)
	assert.Equal(t, pro_interfaces.AuditOutcomeFailure, audit.events[1].Outcome)
	assert.Equal(t, pro_interfaces.AuditReasonOperationError, audit.events[1].Reason)

	audit.err = errors.New("audit offline")
	response = httptest.NewRecorder()
	controller.SavePolicy(response, deploymentWindowRequest(http.MethodPut, "/api/project/7/deployment-windows", `{"revision":4,"timezone":"UTC","default":"allow","rules":[]}`, user, db.CanManageProjectResources))
	assert.Equal(t, http.StatusOK, response.Code, "audit writer failures must not undo governance changes")
}

func TestDeploymentWindowControllerRejectsUntrustedBodiesAndManagerBoundary(t *testing.T) {
	controller := NewDeploymentWindowController(&deploymentWindowGovernanceStub{}, nil)
	manager := db.User{ID: 1}
	for _, body := range []string{
		`{"revision":1,"timezone":"UTC","default":"allow","rules":[],"project_id":9}`,
		`{"revision":1,"timezone":"UTC","default":"allow","rules":[],"unexpected":true}`,
		`{"revision":0,"timezone":"UTC","default":"allow","rules":[]}`,
		`{"revision":1`,
	} {
		response := httptest.NewRecorder()
		controller.SavePolicy(response, deploymentWindowRequest(http.MethodPut, "/api/project/7/deployment-windows", body, manager, db.CanManageProjectResources))
		assert.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	response := httptest.NewRecorder()
	controller.GetPolicy(response, deploymentWindowRequest(http.MethodGet, "/api/project/7/deployment-windows", "", db.User{ID: 2}, db.CanRunProjectTasks))
	assert.Equal(t, http.StatusForbidden, response.Code)

	response = httptest.NewRecorder()
	controller.GetPolicy(response, deploymentWindowRequest(http.MethodGet, "/api/project/7/deployment-windows", "", manager, db.CanManageProjectResources))
	assert.Equal(t, http.StatusOK, response.Code)
}

func TestDeploymentWindowStatusIsCoarseAndHidesCrossTenantTarget(t *testing.T) {
	service := &deploymentWindowGovernanceStub{}
	controller := NewDeploymentWindowController(service, nil)
	admin := db.User{ID: 1, Admin: true}
	response := httptest.NewRecorder()
	controller.CurrentStatus(response, deploymentWindowRequest(http.MethodGet, "/api/project/7/deployment-windows/status?template_id=11", "", admin, 0))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 7, service.statusRequest.ProjectID)
	assert.NotContains(t, response.Body.String(), "provenance")
	assert.NotContains(t, response.Body.String(), "policy_revision")

	service.statusErr = db.ErrDeploymentWindowTenantMismatch
	response = httptest.NewRecorder()
	controller.CurrentStatus(response, deploymentWindowRequest(http.MethodGet, "/api/project/7/deployment-windows/status?template_id=999", "", admin, 0))
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestDeploymentWindowStatusRequiresEffectiveTemplateReadAndRun(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "deployment-window-status"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "deployment-window-repository", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, Name: "deployment-window-target", Playbook: "site.yml", RepositoryID: repository.ID})
	require.NoError(t, err)
	manager, err := store.CreateUserWithoutPassword(db.User{Username: "dw-manager", Name: "Manager", Email: "manager@example.test"})
	require.NoError(t, err)
	runner, err := store.CreateUserWithoutPassword(db.User{Username: "dw-runner", Name: "Runner", Email: "runner@example.test"})
	require.NoError(t, err)
	guest, err := store.CreateUserWithoutPassword(db.User{Username: "dw-guest", Name: "Guest", Email: "guest@example.test"})
	require.NoError(t, err)
	for _, member := range []db.ProjectUser{
		{ProjectID: project.ID, UserID: manager.ID, Role: db.ProjectManager},
		{ProjectID: project.ID, UserID: runner.ID, Role: db.ProjectTaskRunner},
		{ProjectID: project.ID, UserID: guest.ID, Role: db.ProjectGuest},
	} {
		_, createErr := store.CreateProjectUser(member)
		require.NoError(t, createErr)
	}
	controller := NewDeploymentWindowController(&deploymentWindowGovernanceStub{}, nil)
	serve := func(user db.User) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := deploymentWindowRequest(http.MethodGet, "/api/project/"+strconv.Itoa(project.ID)+"/deployment-windows/status?template_id="+strconv.Itoa(template.ID), "", user, 0)
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "project", project)
		controller.CurrentStatus(response, request)
		return response
	}
	assert.Equal(t, http.StatusOK, serve(manager).Code)
	assert.Equal(t, http.StatusOK, serve(runner).Code)
	assert.Equal(t, http.StatusForbidden, serve(guest).Code)
}

func TestDeploymentWindowPreviewHidesTemplateWithoutReadAccess(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "deployment-window-preview-access"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "deployment-window-preview-repository", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, Name: "deployment-window-preview-target", Playbook: "site.yml", RepositoryID: repository.ID})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "dw-preview-hidden", Name: "Preview Hidden", Email: "preview-hidden@example.test"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{
		ID: "deployment-window-preview-manager", Name: "Deployment Window Preview Manager",
		Permissions: db.CanManageProjectResources, ProjectID: &project.ID, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)

	controller := NewDeploymentWindowController(&deploymentWindowGovernanceStub{}, nil)
	response := httptest.NewRecorder()
	request := deploymentWindowRequest(
		http.MethodPost,
		"/api/project/"+strconv.Itoa(project.ID)+"/deployment-windows/preview",
		`{"revision":1,"timezone":"UTC","default":"allow","rules":[],"template_id":`+strconv.Itoa(template.ID)+`}`,
		user,
		db.CanManageProjectResources,
	)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	controller.Preview(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)

	readOnlyUser, err := store.CreateUserWithoutPassword(db.User{Username: "dw-preview-no-run", Name: "Preview No Run", Email: "preview-no-run@example.test"})
	require.NoError(t, err)
	readOnlyRole, err := store.CreateProjectRole(db.Role{
		ID: "deployment-window-preview-read-only", Name: "Deployment Window Preview Read Only",
		Permissions: db.CanManageProjectResources, ProjectID: &project.ID, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: readOnlyUser.ID, RoleID: &readOnlyRole.ID, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID, RoleID: &readOnlyRole.ID,
		AllowedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)
	response = httptest.NewRecorder()
	request = deploymentWindowRequest(
		http.MethodPost,
		"/api/project/"+strconv.Itoa(project.ID)+"/deployment-windows/preview",
		`{"revision":1,"timezone":"UTC","default":"allow","rules":[],"template_id":`+strconv.Itoa(template.ID)+`}`,
		readOnlyUser,
		db.CanManageProjectResources,
	)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	controller.Preview(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestDeploymentWindowControllerRejectsOversizedHistoryAndUnavailableService(t *testing.T) {
	manager := db.User{ID: 1}
	controller := NewDeploymentWindowController(&deploymentWindowGovernanceStub{}, nil)
	response := httptest.NewRecorder()
	controller.DecisionHistory(response, deploymentWindowRequest(http.MethodGet, "/api/project/7/deployment-windows/history?count=101", "", manager, db.CanManageProjectResources))
	assert.Equal(t, http.StatusBadRequest, response.Code)

	controller = NewDeploymentWindowController(nil, nil)
	response = httptest.NewRecorder()
	controller.GetPolicy(response, deploymentWindowRequest(http.MethodGet, "/api/project/7/deployment-windows", "", manager, db.CanManageProjectResources))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)

	large := `{"revision":1,"timezone":"UTC","default":"allow","rules":[]}` + strings.Repeat(" ", int(deploymentWindowBodyLimit))
	controller = NewDeploymentWindowController(&deploymentWindowGovernanceStub{}, nil)
	response = httptest.NewRecorder()
	controller.SavePolicy(response, deploymentWindowRequest(http.MethodPut, "/api/project/7/deployment-windows", large, manager, db.CanManageProjectResources))
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestDeploymentWindowControllerMapsUnavailableServiceErrors(t *testing.T) {
	service := &deploymentWindowGovernanceStub{saveErr: errors.New("database unavailable")}
	response := httptest.NewRecorder()
	NewDeploymentWindowController(service, nil).SavePolicy(response, deploymentWindowRequest(http.MethodPut, "/api/project/7/deployment-windows", `{"revision":1,"timezone":"UTC","default":"allow","rules":[]}`, db.User{ID: 1}, db.CanManageProjectResources))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}
