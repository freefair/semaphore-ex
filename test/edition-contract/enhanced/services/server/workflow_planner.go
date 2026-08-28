package server

import (
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
)

type workflowNodeDecisionKind string

const (
	workflowNodeWait    workflowNodeDecisionKind = "wait"
	workflowNodeReady   workflowNodeDecisionKind = "ready"
	workflowNodeSkipped workflowNodeDecisionKind = "skipped"
	workflowNodeBlocked workflowNodeDecisionKind = "blocked"
)

type workflowNodeDecision struct {
	node   db.WorkflowRunNode
	kind   workflowNodeDecisionKind
	reason string
}

func planWorkflowNodes(run db.WorkflowRun) ([]workflowNodeDecision, int, error) {
	nodes := make(map[int]db.WorkflowRunNode, len(run.Nodes))
	active := 0
	for _, node := range run.Nodes {
		nodes[node.WorkflowNodeID] = node
		if node.Status == db.WorkflowRunNodeQueued || node.Status == db.WorkflowRunNodeRunning {
			active++
		}
	}
	incoming := make(map[int][]db.WorkflowEdge, len(run.Nodes))
	for _, edge := range run.DefinitionSnapshot.Edges {
		incoming[edge.DestinationNodeID] = append(incoming[edge.DestinationNodeID], edge)
	}
	decisions := make([]workflowNodeDecision, 0, len(run.Nodes))
	for _, node := range run.Nodes {
		if node.Status != db.WorkflowRunNodePending {
			continue
		}
		edges := incoming[node.WorkflowNodeID]
		if len(edges) == 0 {
			decisions = append(decisions, workflowNodeDecision{node: node, kind: workflowNodeReady})
			continue
		}
		definitionNode, err := workflowDefinitionNode(run.DefinitionSnapshot, node.WorkflowNodeID)
		if err != nil {
			return nil, active, err
		}
		decision, err := evaluateWorkflowJoin(definitionNode.EffectiveJoinMode(), edges, nodes)
		if err != nil {
			return nil, active, err
		}
		decision.node = node
		decisions = append(decisions, decision)
	}
	return decisions, active, nil
}

func evaluateWorkflowJoin(
	mode db.WorkflowJoinMode,
	edges []db.WorkflowEdge,
	nodes map[int]db.WorkflowRunNode,
) (workflowNodeDecision, error) {
	allComplete := true
	selected := 0
	selectedSuccessful := 0
	for _, edge := range edges {
		source, ok := nodes[edge.SourceNodeID]
		if !ok {
			return workflowNodeDecision{}, fmt.Errorf("workflow predecessor %d is missing", edge.SourceNodeID)
		}
		if !source.Status.IsFinished() {
			allComplete = false
			continue
		}
		// A skipped node represents a branch that was never selected. It is
		// complete for readiness, but must not become a selected join input
		// through a downstream `always` edge.
		if source.Status == db.WorkflowRunNodeSkipped {
			continue
		}
		matched, err := workflowDB.EvaluateWorkflowCondition(edge.ConditionProgram, source.Result)
		if err != nil {
			return workflowNodeDecision{}, fmt.Errorf("evaluate workflow edge %d: %w", edge.ID, err)
		}
		if !matched {
			continue
		}
		selected++
		if source.Status == db.WorkflowRunNodeSucceeded {
			selectedSuccessful++
		}
	}
	if mode == db.WorkflowJoinAnySuccessful && selectedSuccessful > 0 {
		return workflowNodeDecision{kind: workflowNodeReady}, nil
	}
	if !allComplete {
		return workflowNodeDecision{kind: workflowNodeWait}, nil
	}
	if selected == 0 {
		return workflowNodeDecision{kind: workflowNodeSkipped, reason: "Skipped because no incoming condition matched."}, nil
	}
	switch mode {
	case db.WorkflowJoinAllComplete:
		return workflowNodeDecision{kind: workflowNodeReady}, nil
	case db.WorkflowJoinAllSuccessful:
		if selectedSuccessful == selected {
			return workflowNodeDecision{kind: workflowNodeReady}, nil
		}
		return workflowNodeDecision{kind: workflowNodeBlocked, reason: "Blocked because a selected predecessor did not succeed."}, nil
	case db.WorkflowJoinAnySuccessful:
		return workflowNodeDecision{kind: workflowNodeBlocked, reason: "Blocked because no selected predecessor succeeded."}, nil
	default:
		return workflowNodeDecision{}, fmt.Errorf("workflow join mode %q is invalid", mode)
	}
}

func workflowRunTerminalStatus(run db.WorkflowRun) (db.WorkflowRunStatus, string, bool) {
	hasFailed := false
	hasCanceled := false
	hasBlocked := false
	failedReason := ""
	canceledReason := ""
	blockedReason := ""
	for _, node := range run.Nodes {
		if !node.Status.IsFinished() {
			return "", "", false
		}
		switch node.Status {
		case db.WorkflowRunNodeFailed:
			hasFailed = true
			if failedReason == "" {
				failedReason = node.Reason
			}
		case db.WorkflowRunNodeCanceled, db.WorkflowRunNodeStopped:
			hasCanceled = true
			if canceledReason == "" {
				canceledReason = node.Reason
			}
		case db.WorkflowRunNodeBlocked:
			hasBlocked = true
			if blockedReason == "" {
				blockedReason = node.Reason
			}
		}
	}
	if hasFailed {
		if failedReason == "" {
			failedReason = "One or more workflow nodes failed."
		}
		return db.WorkflowRunFailed, failedReason, true
	}
	if hasCanceled {
		if canceledReason == "" {
			canceledReason = "One or more workflow nodes were canceled."
		}
		return db.WorkflowRunCanceled, canceledReason, true
	}
	if hasBlocked {
		if blockedReason == "" {
			blockedReason = "One or more workflow nodes were blocked."
		}
		return db.WorkflowRunBlocked, blockedReason, true
	}
	return db.WorkflowRunSucceeded, "", true
}
