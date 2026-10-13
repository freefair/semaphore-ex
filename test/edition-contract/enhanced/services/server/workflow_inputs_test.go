package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoerceWorkflowSurveyInput(t *testing.T) {
	tests := []struct {
		name     string
		variable db.SurveyVar
		raw      string
		expected string
		present  bool
	}{
		{name: "integer number", variable: db.SurveyVar{Type: db.SurveyVarInt}, raw: `42`, expected: `"42"`, present: true},
		{name: "large integer preserved", variable: db.SurveyVar{Type: db.SurveyVarInt}, raw: `9007199254740993`, expected: `"9007199254740993"`, present: true},
		{name: "enum numeric", variable: db.SurveyVar{Type: db.SurveyVarEnum, Values: []db.SurveyVarEnumValue{{Value: "42"}}}, raw: `42`, expected: `"42"`, present: true},
		{name: "select scalar", variable: db.SurveyVar{Type: db.SurveyVarSelect, Values: []db.SurveyVarEnumValue{{Value: "true"}}}, raw: `true`, expected: `["true"]`, present: true},
		{name: "select array", variable: db.SurveyVar{Type: db.SurveyVarSelect, Values: []db.SurveyVarEnumValue{{Value: "42"}, {Value: "blue"}}}, raw: `[42,"blue"]`, expected: `["42","blue"]`, present: true},
		{name: "select unknown", variable: db.SurveyVar{Type: db.SurveyVarSelect, Values: []db.SurveyVarEnumValue{{Value: "blue"}}}, raw: `["red"]`},
		{name: "optional empty", variable: db.SurveyVar{Type: db.SurveyVarStr}, raw: `""`, expected: `""`, present: true},
		{name: "required empty", variable: db.SurveyVar{Type: db.SurveyVarStr, Required: true}, raw: `""`},
		{name: "compact object", variable: db.SurveyVar{Type: db.SurveyVarText}, raw: `{ "x": 1 }`, expected: `"{\"x\":1}"`, present: true},
		{name: "integer string", variable: db.SurveyVar{Type: db.SurveyVarInt}, raw: `"-42"`, expected: `"-42"`, present: true},
		{name: "integer fraction rejected", variable: db.SurveyVar{Type: db.SurveyVarInt}, raw: `4.2`},
		{name: "enum allowed", variable: db.SurveyVar{Type: db.SurveyVarEnum, Values: []db.SurveyVarEnumValue{{Value: "blue"}}}, raw: `"blue"`, expected: `"blue"`, present: true},
		{name: "enum rejected", variable: db.SurveyVar{Type: db.SurveyVarEnum, Values: []db.SurveyVarEnumValue{{Value: "blue"}}}, raw: `"red"`},
		{name: "secret cannot receive public value", variable: db.SurveyVar{Type: db.SurveyVarSecret}, raw: `"secret"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, present := coerceWorkflowSurveyInput(test.variable, []byte(test.raw))
			assert.Equal(t, test.present, present)
			if present {
				assert.JSONEq(t, test.expected, string(value))
			}
		})
	}
}

func TestWorkflowEdgeCanonicalLessUsesFrozenNodeOrder(t *testing.T) {
	workflow := db.WorkflowTemplate{Nodes: []db.WorkflowNode{{ID: 90}, {ID: 10}}}
	assert.True(t, workflowEdgeCanonicalLess(workflow, db.WorkflowEdge{SourceNodeID: 90}, db.WorkflowEdge{SourceNodeID: 10}))
}

func TestWorkflowServiceResolvesFrozenExplicitEdgeInput(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "release", Type: db.SurveyVarInt, Required: true}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	workflow := edgeInputWorkflow(t, &fixture, db.WorkflowEdgeInputExplicit, []db.WorkflowInputMapping{{Var: "release", Key: "build"}}, nil)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "edge-frozen-input")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	task, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	document := `{"values":{"build":42}}`
	require.NoError(t, fixture.store.UpdateTaskArtifacts(fixture.projectID, task.ID, &document))
	// The post-start edit must not change the frozen task snapshot used below.
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "release", Type: db.SurveyVarStr, Required: true}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(task))
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(task))
	require.Len(t, fixture.enqueuer.tasks, 2)
	plain, err := decodeWorkflowArtifactObject(fixture.enqueuer.inputTasks[1].Environment)
	require.NoError(t, err)
	assert.JSONEq(t, `"42"`, string(plain["release"]))
}

func TestWorkflowServiceFailsMissingRequiredEdgeInputWithoutTaskAndProgressesFailure(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "release", Type: db.SurveyVarStr, Required: true}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	workflow := edgeInputWorkflow(t, &fixture, db.WorkflowEdgeInputExplicit, []db.WorkflowInputMapping{{Var: "release", Key: "missing"}}, nil)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "edge-missing-input")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	task := finishWorkflowTask(t, fixture.store, producer, task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(task))
	run = loadWorkflowRun(t, &fixture, run.ID)
	consumer := workflowRunNodeNamed(t, run, "Consumer")
	assert.Equal(t, db.WorkflowRunNodeFailed, consumer.Status)
	assert.Nil(t, consumer.TaskID)
	assert.Len(t, fixture.enqueuer.tasks, 1)
	require.NoError(t, fixture.service.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	assert.Len(t, fixture.enqueuer.tasks, 1)
}

func TestWorkflowServiceMissingRequiredEdgeInputQueuesOnFailureBranchOnce(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "release", Type: db.SurveyVarStr, Required: true}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	failureTemplate, err := fixture.store.CreateTemplate(db.Template{ProjectID: fixture.projectID, RepositoryID: fixture.first.RepositoryID, Name: "Failure handler", Playbook: "failure.yml"})
	require.NoError(t, err)
	raw := db.WorkflowTemplate{ProjectID: fixture.projectID, Name: "Missing input failure branch", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: fixture.first.ID, DisplayName: "Producer"}, {ID: -2, TemplateID: fixture.second.ID, DisplayName: "Consumer"}, {ID: -3, TemplateID: failureTemplate.ID, DisplayName: "Failure handler", JoinMode: db.WorkflowJoinAllComplete}},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess, InputMode: db.WorkflowEdgeInputExplicit, InputMappings: []db.WorkflowInputMapping{{Var: "release", Key: "missing"}}},
			{ID: -2, SourceNodeID: -2, DestinationNodeID: -3, Condition: db.WorkflowEdgeOnFailure},
		}}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "missing-input-failure-branch")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	task := finishWorkflowTask(t, fixture.store, producer, task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(task))
	run = loadWorkflowRun(t, &fixture, run.ID)
	consumer := workflowRunNodeNamed(t, run, "Consumer")
	handler := workflowRunNodeNamed(t, run, "Failure handler")
	assert.Equal(t, db.WorkflowRunNodeFailed, consumer.Status)
	assert.Nil(t, consumer.TaskID)
	assert.Equal(t, db.WorkflowRunNodeQueued, handler.Status)
	require.NotNil(t, handler.TaskID)
	assert.Len(t, fixture.enqueuer.tasks, 2)
	require.NoError(t, fixture.service.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	assert.Len(t, fixture.enqueuer.tasks, 2)
}

func TestWorkflowServiceFailsEdgeInputOver64KiBWithoutTask(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "release", Type: db.SurveyVarStr, Required: true}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	workflow := edgeInputWorkflow(t, &fixture, db.WorkflowEdgeInputExplicit, []db.WorkflowInputMapping{{Var: "release", Key: "build"}}, nil)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "edge-oversize-input")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	task, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	document := `{"values":{"build":"` + strings.Repeat("x", maxWorkflowEdgeInputBytes) + `"}}`
	require.NoError(t, fixture.store.UpdateTaskArtifacts(fixture.projectID, task.ID, &document))
	task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(task))
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(task))
	run = loadWorkflowRun(t, &fixture, run.ID)
	consumer := workflowRunNodeNamed(t, run, "Consumer")
	assert.Equal(t, db.WorkflowRunNodeFailed, consumer.Status)
	assert.Nil(t, consumer.TaskID)
	assert.Contains(t, consumer.Reason, "64 KB")
}

func TestWorkflowEdgeInputsDoNotOverridePrivateTaskValuesOrLogTheirSources(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "token", Type: db.SurveyVarStr, Required: true}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	workflow := edgeInputWorkflow(t, &fixture, db.WorkflowEdgeInputExplicit, []db.WorkflowInputMapping{{Var: "token", Key: "public_token"}}, nil)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "private-input-precedence")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	producerTask, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	document := `{"values":{"public_token":"public-edge-value"}}`
	require.NoError(t, fixture.store.UpdateTaskArtifacts(fixture.projectID, producerTask.ID, &document))
	producerTask.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(producerTask))
	for index := range run.Nodes {
		if run.Nodes[index].WorkflowNodeID == producer.WorkflowNodeID {
			run.Nodes[index].Status = db.WorkflowRunNodeSucceeded
			run.Nodes[index].Result = db.WorkflowNodeResult{Status: db.WorkflowRunNodeSucceeded}
		}
	}
	consumer := workflowRunNodeNamed(t, run, "Consumer")
	definition, err := workflowDefinitionNode(run.DefinitionSnapshot, consumer.WorkflowNodeID)
	require.NoError(t, err)

	for _, test := range []struct {
		name     string
		secret   string
		bindings map[string]int
	}{
		{name: "task secret", secret: `{"token":"private-secret-value"}`},
		{name: "global credential binding", bindings: map[string]int{"token": 41}},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := db.Task{
				Environment:              `{"token":"established-value"}`,
				Secret:                   test.secret,
				GlobalCredentialBindings: test.bindings,
			}

			failed, reason, lines, resolveErr := fixture.service.(*workflowService).resolveWorkflowEdgeInputs(run, consumer, definition, &task)

			require.NoError(t, resolveErr)
			assert.False(t, failed)
			assert.Empty(t, reason)
			assert.Empty(t, lines)
			plain, decodeErr := decodeWorkflowArtifactObject(task.Environment)
			require.NoError(t, decodeErr)
			assert.JSONEq(t, `"established-value"`, string(plain["token"]))
			assert.NotContains(t, strings.Join(lines, "\n"), "public-edge-value")
			assert.NotContains(t, strings.Join(lines, "\n"), "private-secret-value")
		})
	}
}

func TestWorkflowEdgeInputLimitExcludesEstablishedAndDefaultValues(t *testing.T) {
	value := strings.Repeat("x", maxWorkflowEdgeInputBytes+1)
	tests := []struct {
		name        string
		environment string
		variable    db.SurveyVar
	}{
		{
			name:        "established environment value",
			environment: `{"release":"` + value + `"}`,
			variable:    db.SurveyVar{Name: "release", Type: db.SurveyVarText, Required: true},
		},
		{
			name:     "survey default",
			variable: db.SurveyVar{Name: "release", Type: db.SurveyVarText, Required: true, DefaultValue: &db.SurveyVarDefaultValue{Values: []string{value}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &workflowService{}
			task := db.Task{Environment: test.environment}
			node := db.WorkflowRunNode{WorkflowNodeID: 1, TemplateSnapshot: db.Template{SurveyVars: []db.SurveyVar{test.variable}}}
			run := db.WorkflowRun{DefinitionSnapshot: db.WorkflowTemplate{}}

			failed, reason, lines, err := s.resolveWorkflowEdgeInputs(run, node, db.WorkflowNode{ID: 1}, &task)

			require.NoError(t, err)
			assert.False(t, failed)
			assert.Empty(t, reason)
			assert.Equal(t, []string{`Workflow input "release" came from ` + map[bool]string{true: "survey default", false: "static value"}[test.environment == ""] + "."}, lines)
		})
	}
}

func TestWorkflowServiceInvalidEdgeInputFallsBackToStaticBeforeDefault(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "release", Type: db.SurveyVarEnum, Required: true, Values: []db.SurveyVarEnumValue{{Value: "static"}, {Value: "default"}}, DefaultValue: &db.SurveyVarDefaultValue{Values: []string{"default"}}}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	raw := db.WorkflowTemplate{ProjectID: fixture.projectID, Name: "Static fallback", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: fixture.first.ID, DisplayName: "Producer"}, {ID: -2, TemplateID: fixture.second.ID, DisplayName: "Consumer", TaskParams: &db.TaskParams{ProjectID: fixture.projectID, Environment: `{"release":"static"}`}}},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess, InputMode: db.WorkflowEdgeInputExplicit, InputMappings: []db.WorkflowInputMapping{{Var: "release", Key: "build"}}}}}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "edge-static-fallback")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	task, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	document := `{"values":{"build":"invalid"}}`
	require.NoError(t, fixture.store.UpdateTaskArtifacts(fixture.projectID, task.ID, &document))
	task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(task))
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(task))
	plain, err := decodeWorkflowArtifactObject(fixture.enqueuer.inputTasks[1].Environment)
	require.NoError(t, err)
	assert.JSONEq(t, `"static"`, string(plain["release"]))
}

func TestWorkflowServiceDeclaredSensitiveArtifactAliasRejectsPublicOverwrite(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	configureWorkflowArtifactEncryption(t)
	fixture.second.SurveyVars = []db.SurveyVar{{Name: "deployment_token", Type: db.SurveyVarStr, Required: true}}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))
	workflow := createArtifactWorkflow(t, &fixture)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "edge-declared-secret")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	task, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	document := `{"values":{"deployment_token":"public"}}`
	require.NoError(t, fixture.store.UpdateTaskArtifacts(fixture.projectID, task.ID, &document))
	require.NoError(t, fixture.service.HandleWorkflowTaskOutputs(task, map[string]json.RawMessage{"release": json.RawMessage(`"release"`), "token": json.RawMessage(`"secret"`)}))
	task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(task))
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(task))
	plain, err := decodeWorkflowArtifactObject(fixture.enqueuer.inputTasks[1].Environment)
	require.NoError(t, err)
	secret, err := decodeWorkflowArtifactObject(fixture.enqueuer.inputTasks[1].Secret)
	require.NoError(t, err)
	assert.NotContains(t, plain, "deployment_token")
	assert.JSONEq(t, `"secret"`, string(secret["deployment_token"]))
}

func TestWorkflowEdgeSourceValuesNeverExposeReservedEXOutput(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "reserved-output-source")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "First")
	task, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	document := `{"values":{"safe":"visible","semaphore_workflow_outputs":{"token":"must-not-leak"}}}`
	require.NoError(t, fixture.store.UpdateTaskArtifacts(fixture.projectID, task.ID, &document))
	task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(task))
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(task))
	run = loadWorkflowRun(t, &fixture, run.ID)
	values, err := fixture.service.(*workflowService).workflowEdgeSourceValues(run, producer.WorkflowNodeID, map[int]bool{})
	require.NoError(t, err)
	assert.JSONEq(t, `"visible"`, string(values["safe"]))
	assert.NotContains(t, values, "semaphore_workflow_outputs")
	encoded, err := json.Marshal(values)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "must-not-leak")
}

func edgeInputWorkflow(t *testing.T, fixture *workflowServiceFixture, mode db.WorkflowEdgeInputMode, mappings []db.WorkflowInputMapping, declared []db.WorkflowArtifactReference) db.WorkflowTemplate {
	t.Helper()
	raw := db.WorkflowTemplate{ProjectID: fixture.projectID, Name: "Edge inputs", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: fixture.first.ID, DisplayName: "Producer"}, {ID: -2, TemplateID: fixture.second.ID, DisplayName: "Consumer", ArtifactInputs: declared}},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess, InputMode: mode, InputMappings: mappings}}}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	created, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	return created
}
