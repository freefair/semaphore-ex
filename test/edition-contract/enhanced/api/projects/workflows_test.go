package projects

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	workflowServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowDefinitionServiceStub struct {
	workflows  []db.WorkflowTemplate
	validation db.WorkflowValidationResult
	create     db.WorkflowTemplate
	update     db.WorkflowTemplate
	updateErr  error
	deletedID  int
	calls      int
	getActors  []*db.User
}

type workflowServiceStub struct {
	pro_interfaces.WorkflowService
	run            db.WorkflowRun
	correlationIDs []string
	inputs         []db.WorkflowRunInput
	progressCalls  int
	stopCalls      int
	retryCalls     int
	artifacts      []db.WorkflowArtifactMetadata
	approval       db.WorkflowApproval
	approvalErr    error
	decision       db.WorkflowApprovalDecision
	inbox          []db.WorkflowApproval
}

type workflowExecutionPreflightServiceStub struct {
	*workflowServiceStub
	preview    pro_interfaces.ExecutionPreflightPlan
	previewErr error
	startErr   error
	reviews    []pro_interfaces.ExecutionPreflightReview
	overrides  []*pro_interfaces.DeploymentWindowOverrideInput
}

func (s *workflowExecutionPreflightServiceStub) PreviewWorkflowExecution(
	_ db.WorkflowTemplate,
	_ *db.User,
	_ ...db.WorkflowRunInput,
) (pro_interfaces.ExecutionPreflightPlan, error) {
	return s.preview, s.previewErr
}

func (s *workflowExecutionPreflightServiceStub) StartWorkflowWithExecutionPreflight(
	workflow db.WorkflowTemplate,
	user *db.User,
	correlationID string,
	review pro_interfaces.ExecutionPreflightReview,
	input ...db.WorkflowRunInput,
) (db.WorkflowRun, error) {
	s.reviews = append(s.reviews, review)
	if s.startErr != nil {
		return db.WorkflowRun{}, s.startErr
	}
	return s.StartWorkflow(workflow, user, correlationID, input...)
}

func (s *workflowExecutionPreflightServiceStub) StartWorkflowWithExecutionPreflightAndDeploymentWindowOverride(
	workflow db.WorkflowTemplate,
	user *db.User,
	correlationID string,
	review pro_interfaces.ExecutionPreflightReview,
	override *pro_interfaces.DeploymentWindowOverrideInput,
	input ...db.WorkflowRunInput,
) (db.WorkflowRun, error) {
	s.reviews = append(s.reviews, review)
	s.overrides = append(s.overrides, override)
	if s.startErr != nil {
		return db.WorkflowRun{}, s.startErr
	}
	return s.StartWorkflow(workflow, user, correlationID, input...)
}

func (s *workflowServiceStub) StartWorkflow(_ db.WorkflowTemplate, _ *db.User, correlationID string, input ...db.WorkflowRunInput) (db.WorkflowRun, error) {
	s.correlationIDs = append(s.correlationIDs, correlationID)
	if len(input) > 0 {
		s.inputs = append(s.inputs, input[0])
	}
	s.run.CorrelationID = correlationID
	return s.run, nil
}

func (s *workflowServiceStub) ProgressWorkflowRun(_ int, _ int, _ *db.User) error {
	s.progressCalls++
	return nil
}

func (s *workflowServiceStub) StopWorkflowRun(_ int, _ int, _ *db.User) (db.WorkflowRun, error) {
	s.stopCalls++
	return s.run, nil
}

func (s *workflowServiceStub) RetryWorkflowRunReconciliation(_ int, _ int, _ *db.User) (db.WorkflowRun, error) {
	s.retryCalls++
	return s.run, nil
}

func (s *workflowServiceStub) GetWorkflowApprovalInbox(_ int, _ *db.User) ([]db.WorkflowApproval, error) {
	return s.inbox, nil
}

func (s *workflowServiceStub) ResolveWorkflowApproval(_ int, _ int, _ int, _ int, decision db.WorkflowApprovalDecision, _ *db.User) (db.WorkflowApproval, error) {
	s.decision = decision
	return s.approval, s.approvalErr
}

func (s *workflowServiceStub) HandleWorkflowTaskOutputs(_ db.Task, _ map[string]json.RawMessage) error {
	return nil
}

func (s *workflowServiceStub) GetWorkflowRunArtifacts(_ int, _ int, _ *int) ([]db.WorkflowArtifactMetadata, error) {
	return s.artifacts, nil
}

type workflowManagerStub struct {
	db.WorkflowManager
	runs      []db.WorkflowRun
	run       db.WorkflowRun
	delays    []db.WorkflowDelay
	tasks     []db.TaskWithTpl
	approvals []db.WorkflowApproval
}

func (s *workflowManagerStub) GetWorkflowDelays(projectID int, runID int) ([]db.WorkflowDelay, error) {
	return s.delays, nil
}

func (s *workflowManagerStub) GetWorkflowRuns(_ int, _ int, _ db.RetrieveQueryParams) ([]db.WorkflowRun, error) {
	return s.runs, nil
}

func (s *workflowManagerStub) GetWorkflowRun(_ int, _ int, _ int) (db.WorkflowRun, error) {
	return s.run, nil
}

func (s *workflowManagerStub) GetWorkflowRunTasks(_ int, _ int, _ db.RetrieveQueryParams) ([]db.TaskWithTpl, error) {
	return s.tasks, nil
}

func (s *workflowManagerStub) GetWorkflowApprovals(_ int, _ int) ([]db.WorkflowApproval, error) {
	return s.approvals, nil
}

func (s *workflowDefinitionServiceStub) List(_ int, _ db.RetrieveQueryParams, _ ...*db.User) ([]db.WorkflowTemplate, error) {
	return s.workflows, nil
}

func (s *workflowDefinitionServiceStub) Get(_ int, workflowID int, actors ...*db.User) (db.WorkflowTemplate, error) {
	if len(actors) == 1 {
		s.getActors = append(s.getActors, actors[0])
	}
	for _, workflow := range s.workflows {
		if workflow.ID == workflowID {
			return workflow, nil
		}
	}
	return db.WorkflowTemplate{}, db.ErrNotFound
}

func (s *workflowDefinitionServiceStub) Validate(_ int, _ db.WorkflowTemplate, _ ...*db.User) (db.WorkflowValidationResult, error) {
	s.calls++
	return s.validation, nil
}

func (s *workflowDefinitionServiceStub) Create(_ int, _ db.WorkflowTemplate, _ ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	s.calls++
	return s.create, s.validation, nil
}

func (s *workflowDefinitionServiceStub) Update(_ int, _ int, _ db.WorkflowTemplate, _ ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	s.calls++
	return s.update, s.validation, s.updateErr
}

func (s *workflowDefinitionServiceStub) Delete(_ int, workflowID int, _ ...*db.User) error {
	s.deletedID = workflowID
	return nil
}

func (s *workflowDefinitionServiceStub) ListVersions(_ int, _ int, _ db.RetrieveQueryParams, _ ...*db.User) ([]db.WorkflowVersion, error) {
	return []db.WorkflowVersion{}, nil
}

func (s *workflowDefinitionServiceStub) GetVersion(_ int, _ int, _ int, _ ...*db.User) (db.WorkflowVersion, error) {
	return db.WorkflowVersion{}, db.ErrNotFound
}

func (s *workflowDefinitionServiceStub) DiffVersions(_ int, _ int, _ int, _ int, _ ...*db.User) (pro_interfaces.WorkflowDefinitionDiff, error) {
	return pro_interfaces.WorkflowDefinitionDiff{}, db.ErrNotFound
}

func (s *workflowDefinitionServiceStub) RestoreVersion(_ int, _ int, _ int, _ string, _ ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, db.ErrNotFound
}

func TestWorkflowControllerCRUDAndValidation(t *testing.T) {
	valid := db.WorkflowValidationResult{Valid: true, Issues: []db.WorkflowValidationIssue{}}
	created := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Deploy", DefinitionVersion: 1, Revision: 1}
	service := &workflowDefinitionServiceStub{workflows: []db.WorkflowTemplate{created}, validation: valid, create: created, update: created}
	controller := NewWorkflowController(nil, nil, service)

	listRecorder := httptest.NewRecorder()
	controller.GetWorkflows(listRecorder, workflowRequest(http.MethodGet, "/api/project/7/workflows", nil, nil))
	assert.Equal(t, http.StatusOK, listRecorder.Code)
	assert.Contains(t, listRecorder.Body.String(), `"revision":1`)

	createRecorder := httptest.NewRecorder()
	controller.AddWorkflow(createRecorder, workflowRequest(http.MethodPost, "/api/project/7/workflows", created, nil))
	assert.Equal(t, http.StatusCreated, createRecorder.Code, createRecorder.Body.String())

	getRecorder := httptest.NewRecorder()
	controller.GetWorkflow(getRecorder, workflowRequest(http.MethodGet, "/api/project/7/workflows/41", nil, &created))
	assert.Equal(t, http.StatusOK, getRecorder.Code)

	updateRecorder := httptest.NewRecorder()
	controller.UpdateWorkflow(updateRecorder, workflowRequest(http.MethodPut, "/api/project/7/workflows/41", created, &created))
	assert.Equal(t, http.StatusOK, updateRecorder.Code, updateRecorder.Body.String())

	deleteRecorder := httptest.NewRecorder()
	controller.RemoveWorkflow(deleteRecorder, workflowRequest(http.MethodDelete, "/api/project/7/workflows/41", nil, &created))
	assert.Equal(t, http.StatusNoContent, deleteRecorder.Code)
	assert.Equal(t, 41, service.deletedID)

	validateRecorder := httptest.NewRecorder()
	controller.ValidateWorkflow(validateRecorder, workflowRequest(http.MethodPost, "/api/project/7/workflows/validate", created, nil))
	assert.Equal(t, http.StatusOK, validateRecorder.Code)
	assert.JSONEq(t, `{"valid":true,"issues":[]}`, validateRecorder.Body.String())
}

func TestWorkflowControllerReturnsLocatedValidationErrors(t *testing.T) {
	nodeID := -2
	service := &workflowDefinitionServiceStub{validation: db.WorkflowValidationResult{
		Valid: false,
		Issues: []db.WorkflowValidationIssue{{
			Code: "WORKFLOW_TEMPLATE_NOT_IN_PROJECT", Message: "Template is not available in this project.",
			Path: "nodes[1].template_id", NodeID: &nodeID,
		}},
	}}
	controller := NewWorkflowController(nil, nil, service)
	recorder := httptest.NewRecorder()

	controller.AddWorkflow(recorder, workflowRequest(http.MethodPost, "/api/project/7/workflows", db.WorkflowTemplate{Name: "Invalid"}, nil))

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "WORKFLOW_VALIDATION_FAILED", response["code"])
	issues := response["issues"].([]any)
	issue := issues[0].(map[string]any)
	assert.Equal(t, "WORKFLOW_TEMPLATE_NOT_IN_PROJECT", issue["code"])
	assert.Equal(t, "nodes[1].template_id", issue["path"])
	assert.Equal(t, float64(-2), issue["node_id"])
}

func TestWorkflowControllerReturnsCurrentDefinitionOnRevisionConflict(t *testing.T) {
	current := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Newer", Revision: 3}
	service := &workflowDefinitionServiceStub{
		workflows: []db.WorkflowTemplate{current}, validation: db.WorkflowValidationResult{Valid: true},
		updateErr: pro_interfaces.ErrWorkflowRevisionConflict,
	}
	controller := NewWorkflowController(nil, nil, service)
	recorder := httptest.NewRecorder()

	controller.UpdateWorkflow(recorder, workflowRequest(http.MethodPut, "/api/project/7/workflows/41", db.WorkflowTemplate{Revision: 2}, &current))

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"WORKFLOW_REVISION_CONFLICT"`)
	assert.Contains(t, recorder.Body.String(), `"name":"Newer"`)
	assert.Contains(t, recorder.Body.String(), `"revision":3`)
	require.Len(t, service.getActors, 1)
	assert.Equal(t, 12, service.getActors[0].ID)
}

func TestWorkflowControllerAcceptsBrowserPolicyRevisionWithoutStorageOnlyMirror(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "browser policy revision"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "browser-policy-repository",
		GitURL: "https://example.test/browser-policy.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "browser policy", Playbook: "deploy.yml",
	})
	require.NoError(t, err)
	manager := workflowSQL.NewWorkflowStore(store.GetConnection())
	definitionService := workflowServer.NewWorkflowDefinitionService(manager, store)
	actor := &db.User{ID: 1, Admin: true}
	created, validation, err := definitionService.Create(project.ID, db.WorkflowTemplate{
		Name: "before browser save", Nodes: []db.WorkflowNode{{ID: -1, TemplateID: template.ID}},
	}, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	payload, err := json.Marshal(created)
	require.NoError(t, err)
	var browser db.WorkflowTemplate
	require.NoError(t, json.Unmarshal(payload, &browser))
	assert.Zero(t, browser.AccessPolicyRevision)
	require.Equal(t, created.AccessPolicy.Revision, browser.AccessPolicy.Revision)
	browser.Name = "saved through browser JSON"
	payload, err = json.Marshal(browser)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPut, "/api/project/1/workflows/1", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "workflow", created)
	request = helpers.SetContextValue(request, "user", actor)
	controller := NewWorkflowController(nil, manager, definitionService)
	recorder := httptest.NewRecorder()
	controller.UpdateWorkflow(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var updated db.WorkflowTemplate
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &updated))
	assert.Equal(t, "saved through browser JSON", updated.Name)
	assert.Equal(t, 1, updated.AccessPolicy.Revision)
	persisted, err := definitionService.Get(project.ID, created.ID, actor)
	require.NoError(t, err)
	assert.Equal(t, 1, persisted.AccessPolicyRevision)
	assert.Equal(t, 1, persisted.AccessPolicy.Revision)

	var invalid map[string]any
	require.NoError(t, json.Unmarshal(payload, &invalid))
	invalid["access_policy_revision"] = 999
	invalidPayload, err := json.Marshal(invalid)
	require.NoError(t, err)
	invalidRequest := httptest.NewRequest(http.MethodPut, "/api/project/1/workflows/1", bytes.NewReader(invalidPayload))
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidRequest = helpers.SetContextValue(invalidRequest, "project", project)
	invalidRequest = helpers.SetContextValue(invalidRequest, "workflow", persisted)
	invalidRequest = helpers.SetContextValue(invalidRequest, "user", actor)
	invalidRecorder := httptest.NewRecorder()
	controller.UpdateWorkflow(invalidRecorder, invalidRequest)
	assert.Equal(t, http.StatusBadRequest, invalidRecorder.Code, invalidRecorder.Body.String())
}

func TestWorkflowControllerResponseShapesUsePersistedContributions(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "workflow response shape"})
	require.NoError(t, err)
	actor, err := store.CreateUserWithoutPassword(db.User{
		Username: "workflow-response-admin", Name: "Workflow Response Admin", Email: "workflow-response-admin@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: actor.ID, Role: db.ProjectOwner})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "workflow-response", GitURL: "https://example.test/repo.git", GitBranch: "main"})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "Deploy", Playbook: "deploy.yml"})
	require.NoError(t, err)
	manager := workflowSQL.NewWorkflowStore(store.GetConnection())
	workflow, err := manager.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "restricted", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: template.ID, DisplayName: "deploy"}},
	})
	require.NoError(t, err)
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	workflow.CurrentVersionID = 73
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{template.ID: template}, actor.ID, "response-shape", now)
	require.NoError(t, err)
	assert.Equal(t, 73, run.WorkflowVersionID)
	run, err = manager.CreateWorkflowRun(run)
	require.NoError(t, err)
	policy := db.WorkflowApprovalRolePolicy{
		Revision: 3, Mode: db.WorkflowApprovalRoleModeAllOf,
		RoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}, MinimumDistinctApprovers: 2,
	}
	policySnapshot := db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: 3, Policy: policy}
	policyJSON, err := json.Marshal(policySnapshot)
	require.NoError(t, err)
	approval, opened, err := manager.OpenWorkflowApproval(db.WorkflowApproval{
		ProjectID: project.ID, WorkflowTemplateID: workflow.ID, WorkflowName: workflow.Name,
		WorkflowRunID: run.ID, WorkflowNodeID: workflow.Nodes[0].ID, Status: db.WorkflowApprovalPending,
		Created: now, Prompt: "Approve deployment", EligiblePermission: db.CanRunProjectTasks, RolePolicySnapshotJSON: string(policyJSON),
		RolePolicySnapshotRevision: 3, RolePolicySnapshot: policySnapshot, RequestActorUserID: actor.ID + 1,
		TimeoutOutcome: db.WorkflowApprovalTimeoutReject, CorrelationID: "response-shape",
	})
	require.NoError(t, err)
	require.True(t, opened)
	require.NoError(t, manager.CreateWorkflowApprovalContribution(db.WorkflowApprovalContribution{
		ApprovalID: approval.ID, ActorUserID: actor.ID, RoleID: db.BuiltinProjectRoleReferenceOwner,
		RoleRevision: 1, RoleOrigin: db.WorkflowApprovalRoleOriginBuiltIn, Decision: db.WorkflowApprovalApproved,
		Created: now, PolicyRevision: 3, CorrelationID: "response-shape-contribution",
	}))

	service := &workflowServiceStub{inbox: []db.WorkflowApproval{approval}}
	definition := &workflowDefinitionServiceStub{workflows: []db.WorkflowTemplate{workflow}}
	controller := NewWorkflowController(service, manager, definition)
	request := func(method, target string, selected *db.WorkflowTemplate) *http.Request {
		r := workflowRequest(method, target, nil, selected)
		r = helpers.SetContextValue(r, "project", project)
		r = helpers.SetContextValue(r, "user", &db.User{ID: actor.ID, Admin: true})
		return helpers.SetContextValue(r, "store", store)
	}

	list := httptest.NewRecorder()
	controller.GetWorkflows(list, request(http.MethodGet, "/api/project/7/workflows", nil))
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"effective_access":{"view":true,"edit":true,"start":true,"stop":true,"administer":true}`)

	detail := httptest.NewRecorder()
	controller.GetWorkflow(detail, request(http.MethodGet, "/api/project/7/workflows/41", &workflow))
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	assert.Contains(t, detail.Body.String(), `"effective_access":{"view":true,"edit":true,"start":true,"stop":true,"administer":true}`)

	runDetail := httptest.NewRecorder()
	runRequest := helpers.SetContextValue(request(http.MethodGet, "/api/project/7/workflows/41/runs/91", &workflow), "workflow_run", run)
	controller.GetWorkflowRun(runDetail, runRequest)
	require.Equal(t, http.StatusOK, runDetail.Code, runDetail.Body.String())
	assert.Contains(t, runDetail.Body.String(), `"workflow_version_id":73`)
	assert.Contains(t, runDetail.Body.String(), `"effective_access":{"view":true,"edit":true,"start":true,"stop":true,"administer":true}`)
	assert.Contains(t, runDetail.Body.String(), `"eligible":true`)
	assert.Contains(t, runDetail.Body.String(), `"policy_revision":3`)
	assert.Contains(t, runDetail.Body.String(), `"contribution_count":1`)
	assert.Contains(t, runDetail.Body.String(), `"minimum_distinct_approvers":2`)
	assert.Contains(t, runDetail.Body.String(), `"mode":"all_of"`)

	inbox := httptest.NewRecorder()
	controller.GetWorkflowApprovalInbox(inbox, request(http.MethodGet, "/api/project/7/workflow-approvals", nil))
	require.Equal(t, http.StatusOK, inbox.Code, inbox.Body.String())
	assert.Contains(t, inbox.Body.String(), `"eligible":true`)
	assert.Contains(t, inbox.Body.String(), `"policy_revision":3`)
	assert.Contains(t, inbox.Body.String(), `"contribution_count":1`)
	assert.Contains(t, inbox.Body.String(), `"minimum_distinct_approvers":2`)
	assert.Contains(t, inbox.Body.String(), `"mode":"all_of"`)
	for _, response := range []string{runDetail.Body.String(), inbox.Body.String()} {
		assert.NotContains(t, response, "directory_provider_id")
		assert.NotContains(t, response, "directory_mapping_id")
		assert.NotContains(t, response, "directory_revision_fingerprint")
	}
}

func TestWorkflowControllerRejectsOversizedDefinitionsBeforeServiceCalls(t *testing.T) {
	current := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Current", Revision: 1}
	body := []byte(`{"name":"` + strings.Repeat("a", int(workflowDefinitionBodyLimit)) + `"}`)
	tests := []struct {
		name   string
		method string
		target string
		call   func(pro_interfaces.WorkflowController, http.ResponseWriter, *http.Request)
	}{
		{name: "create", method: http.MethodPost, target: "/api/project/7/workflows", call: func(controller pro_interfaces.WorkflowController, w http.ResponseWriter, r *http.Request) {
			controller.AddWorkflow(w, r)
		}},
		{name: "validate", method: http.MethodPost, target: "/api/project/7/workflows/validate", call: func(controller pro_interfaces.WorkflowController, w http.ResponseWriter, r *http.Request) {
			controller.ValidateWorkflow(w, r)
		}},
		{name: "update", method: http.MethodPut, target: "/api/project/7/workflows/41", call: func(controller pro_interfaces.WorkflowController, w http.ResponseWriter, r *http.Request) {
			controller.UpdateWorkflow(w, r)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &workflowDefinitionServiceStub{}
			controller := NewWorkflowController(nil, nil, service)
			recorder := httptest.NewRecorder()
			request := workflowRawRequest(test.method, test.target, body, &current)

			test.call(controller, recorder, request)

			assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
			assert.JSONEq(t, `{"code":"WORKFLOW_DEFINITION_TOO_LARGE","message":"Workflow definition exceeds the 8 MiB request limit."}`, recorder.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestWorkflowControllerAcceptsMaximumShapeWithinBodyLimit(t *testing.T) {
	nodes := make([]db.WorkflowNode, 200)
	for index := range nodes {
		nodes[index] = db.WorkflowNode{ID: index + 1, TemplateID: index + 1}
	}
	edges := make([]db.WorkflowEdge, 0, 1000)
	for source := 1; source < len(nodes) && len(edges) < cap(edges); source++ {
		for destination := source + 1; destination <= len(nodes) && len(edges) < cap(edges); destination++ {
			edges = append(edges, db.WorkflowEdge{ID: len(edges) + 1, SourceNodeID: source, DestinationNodeID: destination})
		}
	}
	workflow := db.WorkflowTemplate{Name: "Maximum shape", DefinitionVersion: 1, Nodes: nodes, Edges: edges}
	data, err := json.Marshal(workflow)
	require.NoError(t, err)
	require.Less(t, int64(len(data)), workflowDefinitionBodyLimit)
	service := &workflowDefinitionServiceStub{validation: db.WorkflowValidationResult{Valid: true}}
	controller := NewWorkflowController(nil, nil, service)
	recorder := httptest.NewRecorder()

	controller.AddWorkflow(recorder, workflowRawRequest(http.MethodPost, "/api/project/7/workflows", data, nil))

	assert.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.Equal(t, 1, service.calls)
}

func TestWorkflowRunControllerStartStatusListStopAndArtifacts(t *testing.T) {
	taskID := 301
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Edited live definition", Revision: 4}
	run := db.WorkflowRun{
		ID: 91, ProjectID: 7, WorkflowTemplateID: workflow.ID, ActorUserID: 12,
		Status: db.WorkflowRunQueued, DefinitionRevision: 3,
		DefinitionSnapshot: db.WorkflowTemplate{
			ID: 41, ProjectID: 7, Name: "Original snapshot", Revision: 3,
			Nodes: []db.WorkflowNode{{ID: 201, TemplateID: 51, DisplayName: "Deploy"}},
		},
		Nodes: []db.WorkflowRunNode{{
			ID: 101, ProjectID: 7, WorkflowRunID: 91, WorkflowNodeID: 201,
			TemplateID: 51, Status: db.WorkflowRunNodeQueued, TaskID: &taskID,
			TemplateSnapshot: db.Template{ID: 51, Name: "Deploy template"},
		}},
	}
	service := &workflowServiceStub{run: run, artifacts: []db.WorkflowArtifactMetadata{}}
	workflowNodeID := 201
	manager := &workflowManagerStub{
		runs: []db.WorkflowRun{run}, run: run,
		tasks: []db.TaskWithTpl{{Task: db.Task{ID: taskID, ProjectID: 7, WorkflowNodeID: &workflowNodeID}}},
	}
	controller := NewWorkflowController(service, manager, &workflowDefinitionServiceStub{})

	start := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)
	start.Header.Set("Idempotency-Key", "deploy-once")
	startRecorder := httptest.NewRecorder()
	controller.RunWorkflow(startRecorder, start)
	assert.Equal(t, http.StatusCreated, startRecorder.Code, startRecorder.Body.String())
	assert.Equal(t, "deploy-once", startRecorder.Header().Get("Idempotency-Key"))
	assert.Contains(t, startRecorder.Body.String(), `"correlation_id":"deploy-once"`)

	duplicateRecorder := httptest.NewRecorder()
	controller.RunWorkflow(duplicateRecorder, start)
	assert.Equal(t, http.StatusCreated, duplicateRecorder.Code)
	assert.Equal(t, []string{"deploy-once", "deploy-once"}, service.correlationIDs)

	listRecorder := httptest.NewRecorder()
	controller.GetWorkflowRuns(listRecorder, workflowRequest(http.MethodGet, "/api/project/7/workflows/41/runs", nil, &workflow))
	assert.Equal(t, http.StatusOK, listRecorder.Code)
	assert.Contains(t, listRecorder.Body.String(), `"id":91`)

	statusRecorder := httptest.NewRecorder()
	controller.GetWorkflowRun(statusRecorder, workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91", workflow, run))
	assert.Equal(t, http.StatusOK, statusRecorder.Code)
	assert.Equal(t, 1, service.progressCalls)
	assert.Contains(t, statusRecorder.Body.String(), `"workflow":{"id":41`, "the status API must return the frozen definition")
	assert.Contains(t, statusRecorder.Body.String(), `"name":"Original snapshot"`)
	assert.Contains(t, statusRecorder.Body.String(), `"task":{"id":301`)

	stopRecorder := httptest.NewRecorder()
	controller.StopWorkflowRun(stopRecorder, workflowRunRequest(http.MethodPost, "/api/project/7/workflows/41/runs/91/stop", workflow, run))
	assert.Equal(t, http.StatusOK, stopRecorder.Code)
	assert.Equal(t, 1, service.stopCalls)

	artifactsRecorder := httptest.NewRecorder()
	controller.GetWorkflowRunArtifacts(artifactsRecorder, workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91/artifacts", workflow, run))
	assert.Equal(t, http.StatusOK, artifactsRecorder.Code)
	assert.JSONEq(t, `[]`, artifactsRecorder.Body.String())
}

func TestWorkflowExecutionPreflightControllerReturnsPlanAndForwardsReview(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	plan := pro_interfaces.ExecutionPreflightPlan{
		ContractVersion: pro_interfaces.ExecutionPreflightContractVersion,
		Intent:          pro_interfaces.ExecutionPreflightWorkflow, ProjectID: 7, ActorID: 12, WorkflowID: 41,
		Definition: pro_interfaces.ExecutionPreflightDefinition{Kind: pro_interfaces.ExecutionReferenceWorkflow, ID: 41},
		Inputs:     []pro_interfaces.ExecutionPreflightInput{{Name: "token", Type: "secret_reference", Present: true, Sensitive: true}},
		References: []pro_interfaces.ExecutionPreflightReference{{Kind: pro_interfaces.ExecutionReferenceCredential, ID: 9, Visible: true}},
		Commands:   []pro_interfaces.ExecutionPreflightCommand{}, Placements: []pro_interfaces.ExecutionPreflightPlacement{},
		Findings: []pro_interfaces.ExecutionPreflightFinding{}, Fingerprint: "sha256:preview", ReviewToken: "review-token",
	}
	service := &workflowExecutionPreflightServiceStub{
		workflowServiceStub: &workflowServiceStub{run: db.WorkflowRun{ID: 91}}, preview: plan,
	}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	previewRequest := workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/preflight", []byte(`{"parameters":{"token":{"access_key_id":9}}}`), &workflow)
	previewRecorder := httptest.NewRecorder()

	controller.PreviewWorkflow(previewRecorder, previewRequest)

	assert.Equal(t, http.StatusOK, previewRecorder.Code, previewRecorder.Body.String())
	assert.Contains(t, previewRecorder.Body.String(), `"review_token":"review-token"`)
	assert.NotContains(t, previewRecorder.Body.String(), "credential-value")

	startRequest := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)
	startRequest.Header.Set(workflowPreflightFingerprintHeader, "sha256:preview")
	startRequest.Header.Set(workflowPreflightTokenHeader, "review-token")
	startRecorder := httptest.NewRecorder()
	controller.RunWorkflow(startRecorder, startRequest)

	assert.Equal(t, http.StatusCreated, startRecorder.Code, startRecorder.Body.String())
	require.Len(t, service.reviews, 1)
	assert.Equal(t, "sha256:preview", service.reviews[0].Fingerprint)
	assert.Equal(t, "review-token", service.reviews[0].ReviewToken)
}

func TestWorkflowExecutionPreflightControllerReturnsBoundedStaleDiff(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	service := &workflowExecutionPreflightServiceStub{
		workflowServiceStub: &workflowServiceStub{},
		startErr: &pro_interfaces.ExecutionPreflightStaleError{
			Changes:   []pro_interfaces.ExecutionPreflightChangeCode{pro_interfaces.ExecutionChangeReference},
			Preflight: pro_interfaces.ExecutionPreflightPlan{ReviewToken: "fresh-token"},
		},
	}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	recorder := httptest.NewRecorder()
	request := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)

	controller.RunWorkflow(recorder, request)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"stale_execution_preflight"`)
	assert.Contains(t, recorder.Body.String(), `"reference_changed"`)
	assert.Contains(t, recorder.Body.String(), `"review_token":"fresh-token"`)
}

func TestWorkflowRunControllerRejectsOversizedIdempotencyKey(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	service := &workflowServiceStub{}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	request := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)
	request.Header.Set("Idempotency-Key", strings.Repeat("a", workflowRunCorrelationIDLimit+1))
	recorder := httptest.NewRecorder()

	controller.RunWorkflow(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Empty(t, service.correlationIDs)
}

func TestWorkflowRunControllerBindsOnlyAllowListedStartInput(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	service := &workflowServiceStub{run: db.WorkflowRun{ID: 91}}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	request := workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/run", []byte(`{
		"parameters":{"region":"eu"},
		"node_overrides":{"201":{"inventory_id":11,"arguments":"[\"--check\"]"}}
	}`), &workflow)
	recorder := httptest.NewRecorder()

	controller.RunWorkflow(recorder, request)

	assert.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.Len(t, service.inputs, 1)
	assert.JSONEq(t, `"eu"`, string(service.inputs[0].UserValues["region"]))
	require.NotNil(t, service.inputs[0].NodeOverrides[201].InventoryID)
	assert.Equal(t, 11, *service.inputs[0].NodeOverrides[201].InventoryID)
	assert.Empty(t, service.inputs[0].TriggerValues, "direct API callers cannot spoof trigger values")
}

func TestWorkflowRunControllerRejectsUnknownOrTriggerOverrideFields(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	for _, body := range []string{
		`{"trigger_values":{"region":"eu"}}`,
		`{"node_overrides":{"201":{"playbook":"unsafe.yml"}}}`,
		`{"unknown":true}`,
	} {
		service := &workflowServiceStub{}
		controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
		recorder := httptest.NewRecorder()
		controller.RunWorkflow(recorder, workflowRawRequest(
			http.MethodPost, "/api/project/7/workflows/41/run", []byte(body), &workflow,
		))
		assert.Equal(t, http.StatusBadRequest, recorder.Code, body)
		assert.Empty(t, service.inputs)
	}
}

func TestWorkflowRunControllerPassesOnlyStrictManualOverrideEnvelope(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	service := &workflowExecutionPreflightServiceStub{
		workflowServiceStub: &workflowServiceStub{run: db.WorkflowRun{ID: 91}},
	}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	response := httptest.NewRecorder()
	controller.RunWorkflow(response, workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/run", []byte(`{
		"deployment_window_override":{"category":"incident","reference":"INC-41"}
	}`), &workflow))

	assert.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Len(t, service.overrides, 1)
	require.NotNil(t, service.overrides[0])
	assert.Equal(t, pro_interfaces.DeploymentWindowOverrideIncident, service.overrides[0].Category)
	assert.Equal(t, "INC-41", service.overrides[0].Reference)
	assert.NotContains(t, response.Body.String(), "INC-41")

	reviewed := httptest.NewRecorder()
	reviewedRequest := workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/run", []byte(`{
		"deployment_window_override":{"category":"security","reference":"SEC-9"}
	}`), &workflow)
	reviewedRequest.Header.Set(workflowPreflightFingerprintHeader, "sha256:reviewed")
	reviewedRequest.Header.Set(workflowPreflightTokenHeader, "review-token")
	controller.RunWorkflow(reviewed, reviewedRequest)
	assert.Equal(t, http.StatusCreated, reviewed.Code, reviewed.Body.String())
	require.Len(t, service.overrides, 2)
	assert.Equal(t, "SEC-9", service.overrides[1].Reference)
	assert.Equal(t, "sha256:reviewed", service.reviews[1].Fingerprint)
	assert.NotContains(t, reviewed.Body.String(), "SEC-9")

	for _, body := range []string{
		`{"deployment_window_override":{"category":"incident","reference":"INC-41","actor_id":99}}`,
		`{"deployment_window_override":{"category":"incident","reference":"INC-41","authorized":true}}`,
		`{"deployment_window_override":{"category":"incident","reference":"INC-41"}} {}`,
	} {
		invalid := httptest.NewRecorder()
		controller.RunWorkflow(invalid, workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/run", []byte(body), &workflow))
		assert.Equal(t, http.StatusBadRequest, invalid.Code, body)
	}
}

func TestWorkflowPreviewRejectsDeploymentWindowOverride(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	service := &workflowExecutionPreflightServiceStub{
		workflowServiceStub: &workflowServiceStub{},
		preview:             pro_interfaces.ExecutionPreflightPlan{},
	}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	response := httptest.NewRecorder()
	controller.PreviewWorkflow(response, workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/preflight", []byte(`{
		"deployment_window_override":{"category":"incident","reference":"INC-41"}
	}`), &workflow))

	assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), "INC-41")
}

func TestWorkflowRunControllerMapsDeploymentWindowOverrideFailures(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"invalid", pro_interfaces.ErrDeploymentWindowOverrideInvalid, http.StatusBadRequest},
		{"forbidden", pro_interfaces.ErrDeploymentWindowOverrideForbidden, http.StatusForbidden},
		{"existing start", pro_interfaces.ErrDeploymentWindowOverrideConflict, http.StatusConflict},
		{"blocked", &pro_interfaces.DeploymentWindowBlockedError{DecisionID: 19, Reason: pro_interfaces.DeploymentWindowReasonFreezeActive}, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &workflowExecutionPreflightServiceStub{
				workflowServiceStub: &workflowServiceStub{}, startErr: tc.err,
			}
			controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
			response := httptest.NewRecorder()
			controller.RunWorkflow(response, workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/run", []byte(`{
				"deployment_window_override":{"category":"incident","reference":"INC-41"}
			}`), &workflow))
			assert.Equal(t, tc.code, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "INC-41")
			assert.NotContains(t, response.Body.String(), "19")
		})
	}
}

func TestWorkflowRunArtifactsEndpointReturnsValueFreeMetadata(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	run := db.WorkflowRun{ID: 91, ProjectID: 7, WorkflowTemplateID: 41}
	taskID := 301
	attempt := 2
	service := &workflowServiceStub{artifacts: []db.WorkflowArtifactMetadata{{
		WorkflowNodeID: 11, Name: "deployment_token",
		Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString}, Sensitive: true,
		Availability: db.WorkflowArtifactAvailable, SizeBytes: 12,
		Fingerprint: "sha256:metadata-only", ProducerTaskID: &taskID, ProducerAttempt: &attempt,
	}}}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	recorder := httptest.NewRecorder()

	controller.GetWorkflowRunArtifacts(
		recorder,
		workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91/artifacts", workflow, run),
	)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"name":"deployment_token"`)
	assert.Contains(t, recorder.Body.String(), `"sensitive":true`)
	assert.Contains(t, recorder.Body.String(), `"availability":"available"`)
	assert.NotContains(t, recorder.Body.String(), "value_json")
	assert.NotContains(t, recorder.Body.String(), "encrypted_value")
	assert.NotContains(t, recorder.Body.String(), "ciphertext")
}

func TestWorkflowRunResponseExposesEffectiveValuesAndValueFreeSecretAudit(t *testing.T) {
	reference := db.WorkflowSecretReference{AccessKeyID: 41}
	run := db.WorkflowRun{
		ID: 91, ProjectID: 7, WorkflowTemplateID: 41,
		ParameterSnapshot: map[string]db.WorkflowParameterSnapshot{
			"region": {Name: "region", Type: db.WorkflowParameterString, Source: db.WorkflowParameterSourceUser, Value: json.RawMessage(`"eu"`)},
			"token": {Name: "token", Type: db.WorkflowParameterSecretReference, Source: db.WorkflowParameterSourceUser,
				SecretReference: &reference, ReferenceFingerprint: reference.Fingerprint()},
		},
	}
	encoded, err := json.Marshal(newWorkflowRunView(run))
	require.NoError(t, err)
	response := string(encoded)
	assert.Contains(t, response, `"value":"eu"`)
	assert.Contains(t, response, `"access_key_id":41`)
	assert.Contains(t, response, `"reference_fingerprint":"sha256:`)
	assert.NotContains(t, response, "deployment-secret")
	assert.NotContains(t, response, "ciphertext")
}

func TestWorkflowRunResponseExposesValueFreeHAReconciliationDiagnostics(t *testing.T) {
	transferredAt := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	run := db.WorkflowRun{
		ID: 91, ProjectID: 7, WorkflowTemplateID: 41,
		ReconciliationOwnership: &db.WorkflowReconciliationDiagnostics{
			Owned: true, OwnerBootID: "boot-b", PreviousOwnerBootID: "boot-a", FencingToken: 4,
			LeaseExpiresAt: transferredAt.Add(time.Minute), AcquiredAt: transferredAt,
			OwnershipTransferredAt: &transferredAt, TransferCount: 2,
			LeaseAgeSeconds: 7, ReconciliationLagSeconds: 1, Recovered: true,
		},
	}
	encoded, err := json.Marshal(newWorkflowRunView(run))
	require.NoError(t, err)
	response := string(encoded)
	assert.Contains(t, response, `"owner_boot_id":"boot-b"`)
	assert.Contains(t, response, `"previous_owner_boot_id":"boot-a"`)
	assert.Contains(t, response, `"fencing_token":4`)
	assert.Contains(t, response, `"reconciliation_lag_seconds":1`)
	assert.Contains(t, response, `"recovered":true`)
	assert.NotContains(t, response, "parameter_snapshot")
	assert.NotContains(t, response, "credential")
	assert.NotContains(t, response, "secret")
}

func TestWorkflowRunControllerExposesConditionalPresentationFields(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	run := db.WorkflowRun{
		ID: 91, ProjectID: 7, WorkflowTemplateID: 41, Status: db.WorkflowRunSucceeded,
		DefinitionSnapshot: db.WorkflowTemplate{
			ID: 41, Name: "Conditional", MaxParallelTasks: 2,
			Nodes: []db.WorkflowNode{
				{ID: 201, TemplateID: 51, DisplayName: "Root"},
				{ID: 202, TemplateID: 52, DisplayName: "Optional", JoinMode: db.WorkflowJoinAnySuccessful},
			},
			Edges: []db.WorkflowEdge{{
				ID: 301, SourceNodeID: 201, DestinationNodeID: 202,
				Condition: db.WorkflowEdgeExpression, Expression: `result.summary.failed_hosts == 0`,
				ConditionProgram: db.WorkflowConditionProgram{Version: 1, Instructions: []db.WorkflowConditionInstruction{{Operation: "field", Field: "result.secret"}}},
			}},
		},
		Nodes: []db.WorkflowRunNode{
			{WorkflowNodeID: 201, Status: db.WorkflowRunNodeSucceeded, TemplateSnapshot: db.Template{ID: 51, Name: "Root"}},
			{
				WorkflowNodeID: 202, Status: db.WorkflowRunNodeSkipped, Reason: "condition did not match",
				ResultJSON:       `{"status":"skipped","successful":false}`,
				Result:           db.WorkflowNodeResult{Status: db.WorkflowRunNodeSkipped},
				TemplateSnapshot: db.Template{ID: 52, Name: "Optional"},
			},
		},
	}
	service := &workflowServiceStub{run: run}
	manager := &workflowManagerStub{run: run}
	controller := NewWorkflowController(service, manager, &workflowDefinitionServiceStub{})
	recorder := httptest.NewRecorder()

	controller.GetWorkflowRun(recorder, workflowRunRequest(
		http.MethodGet, "/api/project/7/workflows/41/runs/91", workflow, run,
	))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	body := recorder.Body.String()
	assert.Contains(t, body, `"max_parallel_tasks":2`)
	assert.Contains(t, body, `"join_mode":"any-successful"`)
	assert.Contains(t, body, `"condition_expression":"result.summary.failed_hosts == 0"`)
	assert.Contains(t, body, `"status":"skipped"`)
	assert.Contains(t, body, `"result":{"status":"skipped","successful":false}`)
	assert.NotContains(t, body, "condition_program")
	assert.NotContains(t, body, "result.secret")
}

func TestWorkflowRunResponsesExposeOnlyRunPresentationData(t *testing.T) {
	taskArguments := "sensitive-task-arguments"
	vaultScript := "sensitive-vault-script"
	usedRunnerID := 88
	usedRunnerName := "runner-east"
	workflowNodeID := 201
	taskID := 301
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Edited live definition", Revision: 4}
	run := db.WorkflowRun{
		ID: 91, ProjectID: 7, WorkflowTemplateID: workflow.ID, ActorUserID: 12,
		Status: db.WorkflowRunRunning, DefinitionVersion: 1, DefinitionRevision: 3,
		CorrelationID: "deploy-once",
		DefinitionSnapshot: db.WorkflowTemplate{
			ID: 41, ProjectID: 7, Name: "Original snapshot", DefinitionVersion: 1, Revision: 3,
			Nodes: []db.WorkflowNode{{
				ID: workflowNodeID, WorkflowTemplateID: 41, TemplateID: 51,
				DisplayName: "Deploy application", Kind: db.WorkflowNodeTaskKind,
				PositionX: 120, PositionY: 240,
				TaskParams: &db.TaskParams{
					Environment: "sensitive-workflow-environment",
					Arguments:   &taskArguments,
				},
			}},
			Edges: []db.WorkflowEdge{},
		},
		Nodes: []db.WorkflowRunNode{{
			ID: 101, ProjectID: 7, WorkflowRunID: 91, WorkflowNodeID: workflowNodeID,
			TemplateID: 51, Status: db.WorkflowRunNodeRunning, TaskID: &taskID,
			TemplateSnapshot: db.Template{
				ID: 51, ProjectID: 7, Name: "Deploy template", Playbook: "sensitive-playbook.yml",
				Vaults: []db.TemplateVault{{Type: db.TemplateVaultScript, Script: &vaultScript}},
			},
		}},
	}
	service := &workflowServiceStub{run: run}
	manager := &workflowManagerStub{
		runs: []db.WorkflowRun{run}, run: run,
		tasks: []db.TaskWithTpl{{
			Task: db.Task{
				ID: taskID, ProjectID: 7, WorkflowNodeID: &workflowNodeID,
				Status:      task_logger.TaskRunningStatus,
				Environment: "sensitive-task-environment", Arguments: &taskArguments,
			},
			UsedRunnerID: &usedRunnerID, UsedRunnerName: &usedRunnerName,
		}},
	}
	controller := NewWorkflowController(service, manager, &workflowDefinitionServiceStub{})

	responses := map[string]*httptest.ResponseRecorder{}

	startRequest := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)
	startRequest.Header.Set("Idempotency-Key", "deploy-once")
	responses["start"] = httptest.NewRecorder()
	controller.RunWorkflow(responses["start"], startRequest)

	responses["list"] = httptest.NewRecorder()
	controller.GetWorkflowRuns(responses["list"], workflowRequest(http.MethodGet, "/api/project/7/workflows/41/runs", nil, &workflow))

	responses["details"] = httptest.NewRecorder()
	controller.GetWorkflowRun(responses["details"], workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91", workflow, run))

	responses["stop"] = httptest.NewRecorder()
	controller.StopWorkflowRun(responses["stop"], workflowRunRequest(http.MethodPost, "/api/project/7/workflows/41/runs/91/stop", workflow, run))

	for name, recorder := range responses {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, []int{http.StatusOK, http.StatusCreated}, recorder.Code, recorder.Body.String())
			body := recorder.Body.String()
			assert.NotContains(t, body, "sensitive-workflow-environment")
			assert.NotContains(t, body, "sensitive-task-arguments")
			assert.NotContains(t, body, "sensitive-vault-script")
			assert.NotContains(t, body, "sensitive-playbook.yml")
			assert.NotContains(t, body, "sensitive-task-environment")
		})
	}

	details := responses["details"].Body.String()
	assert.Contains(t, details, `"name":"Original snapshot"`)
	assert.Contains(t, details, `"display_name":"Deploy application"`)
	assert.Contains(t, details, `"name":"Deploy template"`)
	assert.Contains(t, details, `"id":301`)
	assert.Contains(t, details, `"status":"running"`)
	assert.Contains(t, details, `"used_runner_id":88`)
}

func TestWorkflowApprovalControllerListsAndResolvesStrictDecision(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	run := db.WorkflowRun{ID: 91, ProjectID: 7, WorkflowTemplateID: 41}
	pending := db.WorkflowApproval{ID: 12, ProjectID: 7, WorkflowRunID: 91, WorkflowNodeID: 201, Status: db.WorkflowApprovalPending, Prompt: "Deploy?"}
	resolved := pending
	resolved.Status = db.WorkflowApprovalApproved
	service := &workflowServiceStub{approval: resolved}
	manager := &workflowManagerStub{approvals: []db.WorkflowApproval{pending}}
	controller := NewWorkflowController(service, manager, &workflowDefinitionServiceStub{})

	list := httptest.NewRecorder()
	controller.GetWorkflowApprovals(list, workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91/approvals", workflow, run))
	assert.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), `"workflow_node_id":201`)
	assert.Contains(t, list.Body.String(), `"status":"pending"`)

	resolveRequest := workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/runs/91/approvals/201", []byte(`{"status":"approved","comment":"Reviewed","source":"user"}`), &workflow)
	resolveRequest = helpers.SetContextValue(resolveRequest, "workflow_run", run)
	resolveRequest = mux.SetURLVars(resolveRequest, map[string]string{"node_id": "201"})
	resolve := httptest.NewRecorder()
	controller.ResolveWorkflowApproval(resolve, resolveRequest)
	assert.Equal(t, http.StatusOK, resolve.Code, resolve.Body.String())
	assert.Equal(t, db.WorkflowApprovalApproved, service.decision.Status)
	assert.Equal(t, "Reviewed", service.decision.Comment)

	invalidRequest := workflowRawRequest(http.MethodPost, "/api/project/7/workflows/41/runs/91/approvals/201", []byte(`{"status":"approved","unexpected":true}`), &workflow)
	invalidRequest = helpers.SetContextValue(invalidRequest, "workflow_run", run)
	invalidRequest = mux.SetURLVars(invalidRequest, map[string]string{"node_id": "201"})
	invalid := httptest.NewRecorder()
	controller.ResolveWorkflowApproval(invalid, invalidRequest)
	assert.Equal(t, http.StatusBadRequest, invalid.Code)
}

func TestWorkflowApprovalControllerListsEligibilityFilteredInbox(t *testing.T) {
	pending := db.WorkflowApproval{
		ID: 12, ProjectID: 7, WorkflowTemplateID: 41, WorkflowName: "Deploy",
		WorkflowRunID: 91, WorkflowNodeID: 201, Status: db.WorkflowApprovalPending, Prompt: "Deploy?",
	}
	service := &workflowServiceStub{inbox: []db.WorkflowApproval{pending}}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})

	recorder := httptest.NewRecorder()
	controller.GetWorkflowApprovalInbox(recorder, workflowRequest(http.MethodGet, "/api/project/7/workflow-approvals", nil, nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"workflow_template_id":41`)
	assert.Contains(t, recorder.Body.String(), `"workflow_name":"Deploy"`)
}

func TestWorkflowControllerRetriesQuarantinedReconciliation(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	run := db.WorkflowRun{ID: 91, ProjectID: 7, WorkflowTemplateID: 41, ReconciliationState: db.WorkflowRunReconciliationQuarantined}
	service := &workflowServiceStub{run: run}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	recorder := httptest.NewRecorder()
	controller.RetryWorkflowRunReconciliation(recorder, workflowRunRequest(http.MethodPost, "/api/project/7/workflows/41/runs/91/retry-reconcile", workflow, run))
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, 1, service.retryCalls)
}

func workflowRequest(method, target string, body any, workflow *db.WorkflowTemplate) *http.Request {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7, Name: "Project"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 12, Username: "workflow-user"})
	if workflow != nil {
		request = helpers.SetContextValue(request, "workflow", *workflow)
	}
	return request
}

func workflowRawRequest(method, target string, body []byte, workflow *db.WorkflowTemplate) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7, Name: "Project"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 12, Username: "workflow-user"})
	if workflow != nil {
		request = helpers.SetContextValue(request, "workflow", *workflow)
	}
	return request
}

func workflowRunRequest(method, target string, workflow db.WorkflowTemplate, run db.WorkflowRun) *http.Request {
	request := workflowRequest(method, target, nil, &workflow)
	return helpers.SetContextValue(request, "workflow_run", run)
}
