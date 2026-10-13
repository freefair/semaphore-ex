package server

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWorkflowPublicOutputPassThroughRespectsFrozenConditionsAndTaskIdentity(t *testing.T) {
	for _, kind := range []db.WorkflowNodeKind{db.WorkflowNodeApprovalKind, db.WorkflowNodeDelayKind} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newWorkflowServiceFixture(t)
			defer fixture.store.Close()
			run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "pass-through-"+string(kind))
			require.NoError(t, err)
			root := run.Nodes[0]
			require.NotNil(t, root.TaskID)
			task := finishWorkflowTask(t, fixture.store, root, task_logger.TaskSuccessStatus, "")
			document := `{"values":{"release":"safe"}}`
			require.NoError(t, fixture.store.UpdateTaskArtifacts(fixture.projectID, task.ID, &document))
			root.Status = db.WorkflowRunNodeSucceeded
			root.Result = db.WorkflowNodeResult{Status: db.WorkflowRunNodeSucceeded, Successful: true}
			passID := 100001
			pass := db.WorkflowRunNode{WorkflowNodeID: passID, Status: db.WorkflowRunNodeSucceeded}
			run.Nodes = []db.WorkflowRunNode{root, pass}
			run.DefinitionSnapshot.Nodes = []db.WorkflowNode{
				{ID: root.WorkflowNodeID, Kind: db.WorkflowNodeTaskKind}, {ID: passID, Kind: kind},
			}
			yes, err := workflowDB.CompileWorkflowCondition("true")
			require.NoError(t, err)
			no, err := workflowDB.CompileWorkflowCondition("false")
			require.NoError(t, err)
			run.DefinitionSnapshot.Edges = []db.WorkflowEdge{{ID: 1, SourceNodeID: root.WorkflowNodeID, DestinationNodeID: passID, ConditionProgram: yes}}
			service := fixture.service.(*workflowService)
			values, err := service.workflowEdgeSourceValues(run, passID, map[int]bool{})
			require.NoError(t, err)
			assert.Equal(t, json.RawMessage(`"safe"`), values["release"])
			run.DefinitionSnapshot.Edges[0].ConditionProgram = no
			values, err = service.workflowEdgeSourceValues(run, passID, map[int]bool{})
			require.NoError(t, err)
			assert.Empty(t, values, "non-selected edge must not contribute")
			run.DefinitionSnapshot.Edges[0].ConditionProgram = yes
			wrongID := task.ID + 10000
			run.Nodes[0].TaskID = &wrongID
			values, err = service.workflowEdgeSourceValues(run, passID, map[int]bool{})
			require.NoError(t, err)
			assert.Empty(t, values, "source must match frozen run-node task binding")
		})
	}
}
