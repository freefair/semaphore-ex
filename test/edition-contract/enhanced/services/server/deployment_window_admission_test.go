package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deploymentWindowAdmissionCapture struct {
	request pro_interfaces.DeploymentWindowAdmissionRequest
}

func (s *deploymentWindowAdmissionCapture) Claim(request pro_interfaces.DeploymentWindowAdmissionRequest) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	s.request = request
	return pro_interfaces.DeploymentWindowAdmissionClaim{Decision: db.DeploymentWindowDecisionRecord{ID: 19, State: string(pro_interfaces.DeploymentWindowDecisionAllowed)}}, nil
}

func TestWorkflowStartAdmissionRequiresFinalRunPersistenceFence(t *testing.T) {
	capture := &deploymentWindowAdmissionCapture{}
	service := &workflowService{deploymentWindowAdmission: capture}
	workflow := db.WorkflowTemplate{ID: 8, ProjectID: 7}
	actor := &db.User{ID: 4}
	run := db.WorkflowRun{}

	err := service.claimWorkflowStartAdmission(&run, workflow, actor, "must-not-reach-unfenced-store")
	require.ErrorContains(t, err, "deployment window workflow admission is unavailable")
	assert.Empty(t, capture.request.DecisionKey, "no decision may be claimed before the final store fence is installed")
}

func TestWorkflowStartAdmissionMapsTriggerSourcesServerSide(t *testing.T) {
	cases := []struct {
		name    string
		trigger db.WorkflowTriggerType
		source  pro_interfaces.DeploymentWindowSource
		origin  pro_interfaces.DeploymentWindowOrigin
	}{
		{"schedule", db.WorkflowTriggerSchedule, pro_interfaces.DeploymentWindowSourceSchedule, pro_interfaces.DeploymentWindowOriginSchedule},
		{"api", db.WorkflowTriggerAPI, pro_interfaces.DeploymentWindowSourceAPI, pro_interfaces.DeploymentWindowOriginWorkflowTrigger},
		{"webhook", db.WorkflowTriggerWebhook, pro_interfaces.DeploymentWindowSourceWebhook, pro_interfaces.DeploymentWindowOriginWorkflowTrigger},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := &deploymentWindowAdmissionCapture{}
			service := &workflowService{deploymentWindowAdmission: capture, deploymentWindowRunFence: true}
			workflow := db.WorkflowTemplate{ID: 8, ProjectID: 7}
			actor := &db.User{ID: 4}
			run := db.WorkflowRun{}
			snapshot := db.WorkflowTriggerSnapshot{ID: 31, Revision: 2, InvocationID: 11, Type: tc.trigger}
			require.NoError(t, service.claimWorkflowStartAdmission(&run, workflow, actor, "caller-controlled", db.WorkflowRunInput{TriggerSnapshot: &snapshot}))
			assert.Equal(t, tc.source, capture.request.Source)
			assert.Equal(t, tc.origin, capture.request.Origin)
			assert.Equal(t, workflow.ID, *capture.request.WorkflowID)
			assert.Equal(t, actor.ID, *capture.request.ActorUserID)
			assert.NotContains(t, capture.request.DecisionKey, "caller-controlled")
			assert.Equal(t, 19, *run.DeploymentWindowDecisionID)
		})
	}
}

func TestWorkflowNodeAdmissionUsesConsumerScopeForCrossProjectNodes(t *testing.T) {
	for _, crossProject := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "cross-project"}[crossProject], func(t *testing.T) {
			capture := &deploymentWindowAdmissionCapture{}
			service := &workflowService{deploymentWindowAdmission: capture}
			runID, nodeID := 41, 71
			task := db.Task{TemplateID: 99, ProjectID: 7, WorkflowRunID: &runID, WorkflowNodeID: &nodeID}
			node := db.WorkflowRunNode{ID: 18, ProjectID: 7, WorkflowRunID: runID, WorkflowNodeID: nodeID, TemplateID: 99}
			if crossProject {
				node.CrossProjectTemplateProvenance = &db.CrossProjectTemplateProvenance{}
			}
			require.NoError(t, service.claimWorkflowNodeAdmission(&task, db.WorkflowRun{ID: runID, ProjectID: 7, WorkflowTemplateID: 13}, node, 4))
			assert.Equal(t, pro_interfaces.DeploymentWindowSourceWorkflowNode, capture.request.Source)
			assert.Equal(t, pro_interfaces.DeploymentWindowOriginWorkflowNode, capture.request.Origin)
			assert.Equal(t, 13, *capture.request.WorkflowID)
			if crossProject {
				assert.Nil(t, capture.request.TemplateID)
			} else {
				assert.Equal(t, 99, *capture.request.TemplateID)
			}
			assert.Equal(t, 19, *task.DeploymentWindowDecisionID)
		})
	}
}

func TestWorkflowAdmissionRejectsLegacyEnqueuerBeforeItCanIgnoreDecision(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()

	configurable, ok := fixture.service.(pro_interfaces.WorkflowDeploymentWindowAdmissionConfigurer)
	require.True(t, ok)
	configurable.ConfigureDeploymentWindowAdmission(
		NewDeploymentWindowAdmissionService(workflowSQL.NewDeploymentWindowStore(fixture.store.GetConnection())),
	)

	_, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "legacy-enqueuer-must-not-bypass")
	require.ErrorContains(t, err, "workflow deployment window task binding is unavailable")
	assert.Empty(t, fixture.enqueuer.inputTasks, "the historical enqueuer must not receive a decision it cannot consume")
	assert.Empty(t, fixture.enqueuer.tasks)
}
