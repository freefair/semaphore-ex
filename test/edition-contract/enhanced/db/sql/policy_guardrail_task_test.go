package sql

import (
	"testing"
	"time"

	coreDB "github.com/semaphoreui/semaphore/db"
	coreSQL "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyGuardrailEvaluationBindsTaskAtomically(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(coreDB.Project{Name: "policy task binding project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "policy-task-binding")
	evaluationID := seedPolicyGuardrailTaskEvaluation(t, store, project.ID, template.ID, coreDB.PolicyGuardrailDecisionAllow, nil, nil)

	created, err := store.CreateTask(coreDB.Task{
		ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus,
		PolicyGuardrailEvaluationID: &evaluationID,
	}, 0)
	require.NoError(t, err)
	require.Positive(t, created.ID)
	assertPolicyGuardrailEvaluationBoundToTask(t, store, evaluationID, created.ID)
}

func TestTaskCreationAtomicallyBindsPolicyGuardrailAndDeploymentWindow(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(coreDB.Project{Name: "combined task admission project"})
	require.NoError(t, err)
	template := deploymentWindowTemplate(t, store, project.ID, "combined-task-admission")

	admissions := NewDeploymentWindowStore(store.GetConnection())
	claim, err := admissions.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: project.ID, DecisionKey: "combined-task-binding", Source: pro_interfaces.DeploymentWindowSourceManual,
		Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: intPointer(template.ID),
	}, deploymentWindowEvaluator().Evaluate)
	require.NoError(t, err)
	decisionID := claim.Decision.ID
	evaluationID := seedPolicyGuardrailTaskEvaluation(t, store, project.ID, template.ID, coreDB.PolicyGuardrailDecisionAllow, nil, nil)

	created, err := store.CreateTask(coreDB.Task{
		ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus,
		DeploymentWindowDecisionID: &decisionID, PolicyGuardrailEvaluationID: &evaluationID,
	}, 0)
	require.NoError(t, err)
	assertPolicyGuardrailEvaluationBoundToTask(t, store, evaluationID, created.ID)
	var deploymentWindowTaskID int
	require.NoError(t, store.Sql().SelectOne(&deploymentWindowTaskID, "select task_id from project__deployment_window_decision where id=?", decisionID))
	assert.Equal(t, created.ID, deploymentWindowTaskID)
}

func TestPolicyGuardrailEvaluationRejectsInvalidOrReusedBindingsWithoutTask(t *testing.T) {
	for _, test := range []struct {
		name        string
		decision    coreDB.PolicyGuardrailDecision
		foreign     bool
		wrongTarget bool
		missing     bool
	}{
		{name: "missing", decision: coreDB.PolicyGuardrailDecisionAllow, missing: true},
		{name: "denied", decision: coreDB.PolicyGuardrailDecisionDeny},
		{name: "cross project", decision: coreDB.PolicyGuardrailDecisionAllow, foreign: true},
		{name: "wrong template", decision: coreDB.PolicyGuardrailDecisionAllow, wrongTarget: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := coreSQL.InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			project, err := store.CreateProject(coreDB.Project{Name: "invalid policy task binding project"})
			require.NoError(t, err)
			template := deploymentWindowTemplate(t, store, project.ID, "invalid-policy-task-binding")
			evaluationProjectID, evaluationTemplateID := project.ID, template.ID
			if test.foreign {
				foreignProject, foreignErr := store.CreateProject(coreDB.Project{Name: "foreign policy task binding project"})
				require.NoError(t, foreignErr)
				evaluationProjectID = foreignProject.ID
			}
			if test.wrongTarget {
				wrongTemplate := deploymentWindowTemplate(t, store, project.ID, "wrong-policy-task-template")
				evaluationTemplateID = wrongTemplate.ID
			}
			evaluationID := 999
			if !test.missing {
				evaluationID = seedPolicyGuardrailTaskEvaluation(t, store, evaluationProjectID, evaluationTemplateID, test.decision, nil, nil)
			}

			_, err = store.CreateTask(coreDB.Task{
				ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus,
				PolicyGuardrailEvaluationID: &evaluationID,
			}, 0)
			require.Error(t, err)
			assertTaskCount(t, store, project.ID, template.ID, 0)
			if !test.missing {
				assertPolicyGuardrailEvaluationUnbound(t, store, evaluationID)
			}
		})
	}

	t.Run("reused", func(t *testing.T) {
		store := coreSQL.InitConfigCreateTestStore()
		t.Cleanup(store.Close)
		project, err := store.CreateProject(coreDB.Project{Name: "reused policy task binding project"})
		require.NoError(t, err)
		template := deploymentWindowTemplate(t, store, project.ID, "reused-policy-task-binding")
		evaluationID := seedPolicyGuardrailTaskEvaluation(t, store, project.ID, template.ID, coreDB.PolicyGuardrailDecisionAllow, nil, nil)

		first, err := store.CreateTask(coreDB.Task{ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus, PolicyGuardrailEvaluationID: &evaluationID}, 0)
		require.NoError(t, err)
		assertPolicyGuardrailEvaluationBoundToTask(t, store, evaluationID, first.ID)

		_, err = store.CreateTask(coreDB.Task{ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus, PolicyGuardrailEvaluationID: &evaluationID}, 0)
		require.Error(t, err)
		assertTaskCount(t, store, project.ID, template.ID, 1)
		assertPolicyGuardrailEvaluationBoundToTask(t, store, evaluationID, first.ID)
	})
}

func TestFencedWorkflowTaskBindsOnlyItsExactPolicyGuardrailEvaluation(t *testing.T) {
	t.Run("exact binding", func(t *testing.T) {
		database, repository, projectID, run := workflowReconciliationFixture(t, "policy-guardrail-workflow-binding")
		t.Cleanup(database.Close)
		node := run.Nodes[0]
		ownership := NewWorkflowReconciliationStore(database.GetConnection())
		lease, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "policy-guardrail-workflow", time.Minute)
		require.NoError(t, err)
		require.True(t, claimed)
		claimed, err = repository.ClaimWorkflowRunNodeFenced(lease, node.WorkflowNodeID, time.Now().UTC())
		require.NoError(t, err)
		require.True(t, claimed)
		evaluationID := seedPolicyGuardrailTaskEvaluation(t, database, projectID, node.TemplateID, coreDB.PolicyGuardrailDecisionAllow, &run.ID, &node.ID)

		created, err := database.CreateWorkflowTaskFenced(coreDB.Task{
			ProjectID: projectID, TemplateID: node.TemplateID, Status: task_logger.TaskWaitingStatus,
			WorkflowRunID: &run.ID, WorkflowNodeID: &node.WorkflowNodeID,
			WorkflowTemplateSnapshot: &node.TemplateSnapshotJSON, PolicyGuardrailEvaluationID: &evaluationID,
		}, 0, lease)
		require.NoError(t, err)
		assertPolicyGuardrailEvaluationBoundToTask(t, database, evaluationID, created.ID)
	})

	t.Run("mismatched workflow node", func(t *testing.T) {
		database, repository, projectID, run := workflowReconciliationFixture(t, "policy-guardrail-workflow-mismatch")
		t.Cleanup(database.Close)
		node := run.Nodes[0]
		ownership := NewWorkflowReconciliationStore(database.GetConnection())
		lease, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "policy-guardrail-workflow-mismatch", time.Minute)
		require.NoError(t, err)
		require.True(t, claimed)
		claimed, err = repository.ClaimWorkflowRunNodeFenced(lease, node.WorkflowNodeID, time.Now().UTC())
		require.NoError(t, err)
		require.True(t, claimed)
		otherNode := run.Nodes[1]
		evaluationID := seedPolicyGuardrailTaskEvaluation(t, database, projectID, node.TemplateID, coreDB.PolicyGuardrailDecisionAllow, &run.ID, &otherNode.ID)

		_, err = database.CreateWorkflowTaskFenced(coreDB.Task{
			ProjectID: projectID, TemplateID: node.TemplateID, Status: task_logger.TaskWaitingStatus,
			WorkflowRunID: &run.ID, WorkflowNodeID: &node.WorkflowNodeID,
			WorkflowTemplateSnapshot: &node.TemplateSnapshotJSON, PolicyGuardrailEvaluationID: &evaluationID,
		}, 0, lease)
		require.Error(t, err)
		assertTaskCount(t, database, projectID, node.TemplateID, 0)
		assertPolicyGuardrailEvaluationUnbound(t, database, evaluationID)
	})

	t.Run("direct task evaluation", func(t *testing.T) {
		database, repository, projectID, run := workflowReconciliationFixture(t, "policy-guardrail-workflow-direct")
		t.Cleanup(database.Close)
		node := run.Nodes[0]
		ownership := NewWorkflowReconciliationStore(database.GetConnection())
		lease, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "policy-guardrail-workflow-direct", time.Minute)
		require.NoError(t, err)
		require.True(t, claimed)
		claimed, err = repository.ClaimWorkflowRunNodeFenced(lease, node.WorkflowNodeID, time.Now().UTC())
		require.NoError(t, err)
		require.True(t, claimed)
		evaluationID := seedPolicyGuardrailTaskEvaluation(t, database, projectID, node.TemplateID, coreDB.PolicyGuardrailDecisionAllow, nil, nil)

		_, err = database.CreateWorkflowTaskFenced(coreDB.Task{
			ProjectID: projectID, TemplateID: node.TemplateID, Status: task_logger.TaskWaitingStatus,
			WorkflowRunID: &run.ID, WorkflowNodeID: &node.WorkflowNodeID,
			WorkflowTemplateSnapshot: &node.TemplateSnapshotJSON, PolicyGuardrailEvaluationID: &evaluationID,
		}, 0, lease)
		require.Error(t, err)
		assertTaskCount(t, database, projectID, node.TemplateID, 0)
		assertPolicyGuardrailEvaluationUnbound(t, database, evaluationID)
	})
}

func seedPolicyGuardrailTaskEvaluation(t *testing.T, store *coreSQL.SqlDb, projectID, templateID int, decision coreDB.PolicyGuardrailDecision, workflowRunID, workflowRunNodeID *int) int {
	t.Helper()
	result, err := store.Sql().Exec(
		"insert into policy_guardrail_evaluation(project_id, decision_key, intent, source, template_id, workflow_template_id, workflow_run_id, workflow_run_node_id, task_id, actor_user_id, input_fingerprint, revisions_json, findings_json, decision, evaluated_at, created) values (?, ?, ?, ?, ?, ?, ?, ?, null, null, ?, ?, ?, ?, ?, ?)",
		projectID, "task-binding-"+time.Now().UTC().Format(time.RFC3339Nano), "task", "task-binding", templateID, nil, workflowRunID, workflowRunNodeID,
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "[]", "[]", decision, time.Now().UTC(), time.Now().UTC(),
	)
	require.NoError(t, err)
	id, err := result.LastInsertId()
	require.NoError(t, err)
	return int(id)
}

func assertPolicyGuardrailEvaluationBoundToTask(t *testing.T, store *coreSQL.SqlDb, evaluationID, expectedTaskID int) {
	t.Helper()
	var taskID int
	require.NoError(t, store.Sql().SelectOne(&taskID, "select task_id from policy_guardrail_evaluation where id=?", evaluationID))
	assert.Equal(t, expectedTaskID, taskID)
}

func assertPolicyGuardrailEvaluationUnbound(t *testing.T, store *coreSQL.SqlDb, evaluationID int) {
	t.Helper()
	var count int
	require.NoError(t, store.Sql().SelectOne(&count, "select count(1) from policy_guardrail_evaluation where id=? and task_id is null", evaluationID))
	assert.Equal(t, 1, count)
}

func assertTaskCount(t *testing.T, store *coreSQL.SqlDb, projectID, templateID, expected int) {
	t.Helper()
	var count int
	require.NoError(t, store.Sql().SelectOne(&count, "select count(1) from task where project_id=? and template_id=?", projectID, templateID))
	assert.Equal(t, expected, count)
}
