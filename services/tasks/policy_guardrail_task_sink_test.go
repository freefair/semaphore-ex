package tasks

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPoolPolicyGuardrailAdmissionRejectsUnadmittedDirectTaskSinks(t *testing.T) {
	policy := &executionPreflightPolicyGuardrailStub{}
	evaluationID, decisionID := 1, 1
	task := db.Task{
		TemplateID: 1, ProjectID: 1, WorkflowRunID: intPtr(2), WorkflowNodeID: intPtr(3),
		DeploymentWindowDecisionID: &decisionID,
	}
	template := db.Template{ID: 1, ProjectID: 1}
	lease := pro_interfaces.WorkflowReconciliationLease{ProjectID: 1, WorkflowRunID: 2, OwnerBootID: "owner", FencingToken: 1}

	newPool := func() *TaskPool {
		return &TaskPool{
			policyGuardrailAdmission:  policy,
			deploymentWindowAdmission: deploymentWindowAdmissionStub{},
		}
	}

	tests := []struct {
		name string
		call func(*TaskPool, db.Task) error
	}{
		{
			name: "generic AddTask rejects even a caller-supplied evaluation",
			call: func(pool *TaskPool, task db.Task) error {
				taskWithEvaluation := task
				taskWithEvaluation.PolicyGuardrailEvaluationID = &evaluationID
				_, err := pool.AddTask(taskWithEvaluation, nil, "", 1, false)
				return err
			},
		},
		{
			name: "workflow task",
			call: func(pool *TaskPool, task db.Task) error {
				_, err := pool.AddWorkflowTask(task, template, nil, "", 1, false)
				return err
			},
		},
		{
			name: "fenced workflow task",
			call: func(pool *TaskPool, task db.Task) error {
				_, err := pool.AddWorkflowTaskFenced(task, template, nil, "", 1, false, lease)
				return err
			},
		},
		{
			name: "workflow task deployment-window decision",
			call: func(pool *TaskPool, task db.Task) error {
				_, err := pool.AddWorkflowTaskWithDeploymentWindowDecision(task, template, nil, "", 1, false)
				return err
			},
		},
		{
			name: "fenced workflow task deployment-window decision",
			call: func(pool *TaskPool, task db.Task) error {
				_, err := pool.AddWorkflowTaskFencedWithDeploymentWindowDecision(task, template, nil, "", 1, false, lease)
				return err
			},
		},
		{
			name: "cross-project workflow task",
			call: func(pool *TaskPool, task db.Task) error {
				_, err := pool.AddCrossProjectWorkflowTaskFenced(task, db.CrossProjectTemplateProvenance{}, nil, "", 1, &lease)
				return err
			},
		},
		{
			name: "cross-project workflow task deployment-window decision",
			call: func(pool *TaskPool, task db.Task) error {
				_, err := pool.AddCrossProjectWorkflowTaskFencedWithDeploymentWindowDecision(task, db.CrossProjectTemplateProvenance{}, nil, "", 1, &lease)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuredErr := test.call(newPool(), task)
			require.Error(t, configuredErr)
			assert.Contains(t, configuredErr.Error(), "policy guardrail")

			communityTask := task
			communityTask.DeploymentWindowDecisionID = nil
			communityErr := test.call(&TaskPool{deploymentWindowAdmission: deploymentWindowAdmissionStub{}}, communityTask)
			require.Error(t, communityErr)
			assert.NotContains(t, communityErr.Error(), "policy guardrail", "nil policy admission must preserve the existing sink behavior")
		})
	}
}
