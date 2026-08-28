package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowJoinModesAndEmptyBranches(t *testing.T) {
	succeeded := workflowPlannerNode(1, db.WorkflowRunNodeSucceeded)
	failed := workflowPlannerNode(2, db.WorkflowRunNodeFailed)
	running := workflowPlannerNode(3, db.WorkflowRunNodeRunning)
	skipped := workflowPlannerNode(4, db.WorkflowRunNodeSkipped)
	nodes := map[int]db.WorkflowRunNode{1: succeeded, 2: failed, 3: running, 4: skipped}

	tests := []struct {
		name  string
		mode  db.WorkflowJoinMode
		edges []db.WorkflowEdge
		kind  workflowNodeDecisionKind
	}{
		{
			name: "all successful blocks after a selected failure", mode: db.WorkflowJoinAllSuccessful,
			edges: []db.WorkflowEdge{workflowPlannerEdge(t, 1, "true"), workflowPlannerEdge(t, 2, "true")},
			kind:  workflowNodeBlocked,
		},
		{
			name: "all complete accepts selected failures", mode: db.WorkflowJoinAllComplete,
			edges: []db.WorkflowEdge{workflowPlannerEdge(t, 1, "true"), workflowPlannerEdge(t, 2, "true")},
			kind:  workflowNodeReady,
		},
		{
			name: "any successful starts before every predecessor completes", mode: db.WorkflowJoinAnySuccessful,
			edges: []db.WorkflowEdge{workflowPlannerEdge(t, 1, "true"), workflowPlannerEdge(t, 3, "true")},
			kind:  workflowNodeReady,
		},
		{
			name: "any successful waits while a predecessor can still succeed", mode: db.WorkflowJoinAnySuccessful,
			edges: []db.WorkflowEdge{workflowPlannerEdge(t, 2, "true"), workflowPlannerEdge(t, 3, "true")},
			kind:  workflowNodeWait,
		},
		{
			name: "an empty selected branch is skipped deterministically", mode: db.WorkflowJoinAllComplete,
			edges: []db.WorkflowEdge{workflowPlannerEdge(t, 1, "false"), workflowPlannerEdge(t, 2, "false")},
			kind:  workflowNodeSkipped,
		},
		{
			name: "unselected failures do not block all successful", mode: db.WorkflowJoinAllSuccessful,
			edges: []db.WorkflowEdge{workflowPlannerEdge(t, 1, "true"), workflowPlannerEdge(t, 2, "false")},
			kind:  workflowNodeReady,
		},
		{
			name: "skipped branches do not count as selected predecessors", mode: db.WorkflowJoinAllSuccessful,
			edges: []db.WorkflowEdge{workflowPlannerEdge(t, 1, "true"), workflowPlannerEdge(t, 4, "true")},
			kind:  workflowNodeReady,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := evaluateWorkflowJoin(test.mode, test.edges, nodes)
			require.NoError(t, err)
			assert.Equal(t, test.kind, decision.kind)
		})
	}
}

func TestWorkflowPlannerEvaluatesImmutableSummaryResults(t *testing.T) {
	source := workflowPlannerNode(1, db.WorkflowRunNodeSucceeded)
	source.Result.Summary = &db.WorkflowNodeResultSummary{FailedHosts: 2}
	edge := workflowPlannerEdge(t, 1, "result.summary.available && result.summary.failed_hosts > 0")

	decision, err := evaluateWorkflowJoin(
		db.WorkflowJoinAllSuccessful,
		[]db.WorkflowEdge{edge},
		map[int]db.WorkflowRunNode{1: source},
	)
	require.NoError(t, err)
	assert.Equal(t, workflowNodeReady, decision.kind)
}

func TestWorkflowRunTerminalStatusPreservesDistinctOutcomeAndReason(t *testing.T) {
	tests := []struct {
		name   string
		nodes  []db.WorkflowRunNode
		status db.WorkflowRunStatus
		reason string
	}{
		{
			name: "failed", nodes: []db.WorkflowRunNode{
				{Status: db.WorkflowRunNodeFailed, Reason: "first task failed"},
				{Status: db.WorkflowRunNodeSkipped, Reason: "branch not selected"},
			}, status: db.WorkflowRunFailed, reason: "first task failed",
		},
		{
			name: "canceled", nodes: []db.WorkflowRunNode{
				{Status: db.WorkflowRunNodeCanceled, Reason: "stopped by actor"},
			}, status: db.WorkflowRunCanceled, reason: "stopped by actor",
		},
		{
			name: "blocked", nodes: []db.WorkflowRunNode{
				{Status: db.WorkflowRunNodeBlocked, Reason: "join failed"},
			}, status: db.WorkflowRunBlocked, reason: "join failed",
		},
		{
			name: "skips are successful", nodes: []db.WorkflowRunNode{
				{Status: db.WorkflowRunNodeSucceeded}, {Status: db.WorkflowRunNodeSkipped},
			}, status: db.WorkflowRunSucceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, reason, terminal := workflowRunTerminalStatus(db.WorkflowRun{Nodes: test.nodes})
			assert.True(t, terminal)
			assert.Equal(t, test.status, status)
			assert.Equal(t, test.reason, reason)
		})
	}
}

func workflowPlannerNode(id int, status db.WorkflowRunNodeStatus) db.WorkflowRunNode {
	return db.WorkflowRunNode{
		WorkflowNodeID: id,
		Status:         status,
		Result: db.WorkflowNodeResult{
			Status: status, Successful: status == db.WorkflowRunNodeSucceeded,
		},
	}
}

func workflowPlannerEdge(t *testing.T, sourceID int, expression string) db.WorkflowEdge {
	t.Helper()
	program, err := workflowDB.CompileWorkflowCondition(expression)
	require.NoError(t, err)
	return db.WorkflowEdge{ID: sourceID, SourceNodeID: sourceID, ConditionProgram: program}
}
