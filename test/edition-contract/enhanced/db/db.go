// Package db implements the clean-room enhanced workflow domain contract.
package db

import (
	"errors"
	"fmt"
	"strings"

	coreDB "github.com/semaphoreui/semaphore/db"
)

const (
	MaxWorkflowNodes       = 200
	MaxWorkflowEdges       = 1000
	maxWorkflowNameLength  = 255
	maxWorkflowLabelLength = 255
)

func NormalizeWorkflowTemplate(workflow coreDB.WorkflowTemplate) coreDB.WorkflowTemplate {
	workflow.Name = strings.TrimSpace(workflow.Name)
	if workflow.DefinitionVersion == 0 {
		workflow.DefinitionVersion = coreDB.WorkflowDefinitionVersion
	}
	nextEdgeID := -1
	usedEdgeIDs := make(map[int]struct{}, len(workflow.Edges))
	for index := range workflow.Edges {
		edge := &workflow.Edges[index]
		if edge.Condition == "" {
			edge.Condition = coreDB.WorkflowEdgeOnSuccess
		}
		edge.Label = strings.TrimSpace(edge.Label)
		if edge.ID == 0 {
			for {
				if _, exists := usedEdgeIDs[nextEdgeID]; !exists {
					edge.ID = nextEdgeID
					nextEdgeID--
					break
				}
				nextEdgeID--
			}
		}
		usedEdgeIDs[edge.ID] = struct{}{}
	}
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.Kind == "" {
			node.Kind = coreDB.WorkflowNodeTaskKind
		}
		if node.ConvergenceMode == "" {
			node.ConvergenceMode = coreDB.WorkflowConvergenceAll
		}
		node.DisplayName = strings.TrimSpace(node.DisplayName)
	}
	return workflow
}

func ValidateWorkflowTemplate(store coreDB.WorkflowTemplateValidationStore, workflow coreDB.WorkflowTemplate) (coreDB.WorkflowValidationResult, error) {
	workflow = NormalizeWorkflowTemplate(workflow)
	issues := make([]coreDB.WorkflowValidationIssue, 0)
	add := func(code, message, path string, nodeID, edgeID *int) {
		issues = append(issues, coreDB.WorkflowValidationIssue{
			Code: code, Message: message, Path: path, NodeID: nodeID, EdgeID: edgeID,
		})
	}
	if workflow.Name == "" {
		add("WORKFLOW_NAME_REQUIRED", "Workflow name is required.", "name", nil, nil)
	} else if len(workflow.Name) > maxWorkflowNameLength {
		add("WORKFLOW_NAME_TOO_LONG", "Workflow name is too long.", "name", nil, nil)
	}
	if workflow.DefinitionVersion != coreDB.WorkflowDefinitionVersion {
		add("WORKFLOW_SCHEMA_UNSUPPORTED", "Workflow definition version is not supported.", "definition_version", nil, nil)
	}
	if len(workflow.Nodes) == 0 {
		add("WORKFLOW_NODES_REQUIRED", "At least one workflow node is required.", "nodes", nil, nil)
	}
	if len(workflow.Nodes) > MaxWorkflowNodes {
		add("WORKFLOW_NODE_LIMIT_EXCEEDED", fmt.Sprintf("A workflow can contain at most %d nodes.", MaxWorkflowNodes), "nodes", nil, nil)
	}
	if len(workflow.Edges) > MaxWorkflowEdges {
		add("WORKFLOW_EDGE_LIMIT_EXCEEDED", fmt.Sprintf("A workflow can contain at most %d edges.", MaxWorkflowEdges), "edges", nil, nil)
	}

	nodes := make(map[int]coreDB.WorkflowNode, len(workflow.Nodes))
	executable := make(map[int]struct{}, len(workflow.Nodes))
	for index, node := range workflow.Nodes {
		id := node.ID
		path := fmt.Sprintf("nodes[%d]", index)
		if id == 0 {
			add("WORKFLOW_NODE_ID_REQUIRED", "Workflow node ID must not be zero.", path+".id", &id, nil)
		} else if _, exists := nodes[id]; exists {
			add("WORKFLOW_NODE_ID_DUPLICATE", "Workflow node ID must be unique.", path+".id", &id, nil)
		}
		nodes[id] = node
		if err := node.Kind.Validate(); err != nil {
			add("WORKFLOW_NODE_KIND_INVALID", "Workflow node kind is invalid.", path+".kind", &id, nil)
		}
		if node.EffectiveKind() != coreDB.WorkflowNodeNoteKind {
			executable[id] = struct{}{}
			if err := node.ConvergenceMode.Validate(); err != nil {
				add("WORKFLOW_CONVERGENCE_INVALID", "Workflow convergence mode is invalid.", path+".convergence_mode", &id, nil)
			}
		}
		if len(node.DisplayName) > maxWorkflowLabelLength {
			add("WORKFLOW_NODE_DISPLAY_NAME_TOO_LONG", "Workflow node display name is too long.", path+".display_name", &id, nil)
		}
		switch node.EffectiveKind() {
		case coreDB.WorkflowNodeTaskKind:
			if node.TemplateID <= 0 {
				add("WORKFLOW_TEMPLATE_REQUIRED", "Task nodes require a template.", path+".template_id", &id, nil)
			} else if store != nil {
				_, err := store.GetTemplate(workflow.ProjectID, node.TemplateID)
				if errors.Is(err, coreDB.ErrNotFound) {
					add("WORKFLOW_TEMPLATE_NOT_IN_PROJECT", "Template is not available in this project.", path+".template_id", &id, nil)
				} else if err != nil {
					return coreDB.WorkflowValidationResult{}, fmt.Errorf("validate workflow template reference: %w", err)
				}
			}
		case coreDB.WorkflowNodeApprovalKind:
			if node.TemplateID != 0 {
				add("WORKFLOW_APPROVAL_TEMPLATE_FORBIDDEN", "Approval nodes cannot reference a template.", path+".template_id", &id, nil)
			}
			if node.ApprovalTimeout != nil && *node.ApprovalTimeout <= 0 {
				add("WORKFLOW_APPROVAL_TIMEOUT_INVALID", "Approval timeout must be positive.", path+".approval_timeout", &id, nil)
			}
		case coreDB.WorkflowNodeNoteKind:
			if node.TemplateID != 0 {
				add("WORKFLOW_NOTE_TEMPLATE_FORBIDDEN", "Note nodes cannot reference a template.", path+".template_id", &id, nil)
			}
		}
	}

	edgeIDs := make(map[int]struct{}, len(workflow.Edges))
	adjacency := make(map[int][]int, len(executable))
	undirected := make(map[int][]int, len(executable))
	incoming := make(map[int]int, len(executable))
	for index, edge := range workflow.Edges {
		id := edge.ID
		path := fmt.Sprintf("edges[%d]", index)
		if id == 0 {
			add("WORKFLOW_EDGE_ID_REQUIRED", "Workflow edge ID must not be zero.", path+".id", nil, &id)
		} else if _, exists := edgeIDs[id]; exists {
			add("WORKFLOW_EDGE_ID_DUPLICATE", "Workflow edge ID must be unique.", path+".id", nil, &id)
		}
		edgeIDs[id] = struct{}{}
		if err := edge.Condition.Validate(); err != nil {
			add("WORKFLOW_EDGE_CONDITION_INVALID", "Workflow edge condition is invalid.", path+".condition", nil, &id)
		}
		if len(edge.Label) > maxWorkflowLabelLength {
			add("WORKFLOW_EDGE_LABEL_TOO_LONG", "Workflow edge label is too long.", path+".label", nil, &id)
		}
		_, sourceExists := nodes[edge.SourceNodeID]
		_, destinationExists := nodes[edge.DestinationNodeID]
		if !sourceExists {
			add("WORKFLOW_EDGE_SOURCE_MISSING", "Workflow edge source does not exist.", path+".source_node_id", nil, &id)
		}
		if !destinationExists {
			add("WORKFLOW_EDGE_DESTINATION_MISSING", "Workflow edge destination does not exist.", path+".destination_node_id", nil, &id)
		}
		if edge.SourceNodeID == edge.DestinationNodeID {
			add("WORKFLOW_SELF_EDGE", "A workflow node cannot connect to itself.", path, nil, &id)
		}
		_, sourceExecutable := executable[edge.SourceNodeID]
		_, destinationExecutable := executable[edge.DestinationNodeID]
		if sourceExists && destinationExists && (!sourceExecutable || !destinationExecutable) {
			add("WORKFLOW_NOTE_EDGE_FORBIDDEN", "Note nodes cannot be connected.", path, nil, &id)
		}
		if sourceExecutable && destinationExecutable && edge.SourceNodeID != edge.DestinationNodeID {
			adjacency[edge.SourceNodeID] = append(adjacency[edge.SourceNodeID], edge.DestinationNodeID)
			undirected[edge.SourceNodeID] = append(undirected[edge.SourceNodeID], edge.DestinationNodeID)
			undirected[edge.DestinationNodeID] = append(undirected[edge.DestinationNodeID], edge.SourceNodeID)
			incoming[edge.DestinationNodeID]++
		}
	}

	if len(executable) > 0 {
		roots := 0
		var first int
		for id := range executable {
			if first == 0 {
				first = id
			}
			if incoming[id] == 0 {
				roots++
			}
		}
		if roots != 1 {
			add("WORKFLOW_ROOT_COUNT_INVALID", "Workflow must have exactly one starting node.", "nodes", nil, nil)
		}
		if hasCycle(adjacency, executable) {
			add("WORKFLOW_CYCLE", "Workflow graph must not contain a cycle.", "edges", nil, nil)
		}
		seen := map[int]struct{}{first: {}}
		stack := []int{first}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, next := range undirected[current] {
				if _, exists := seen[next]; exists {
					continue
				}
				seen[next] = struct{}{}
				stack = append(stack, next)
			}
		}
		if len(seen) != len(executable) {
			add("WORKFLOW_DISCONNECTED", "Workflow graph contains disconnected nodes.", "nodes", nil, nil)
		}
	}

	return coreDB.WorkflowValidationResult{Valid: len(issues) == 0, Issues: issues}, nil
}

func hasCycle(adjacency map[int][]int, nodes map[int]struct{}) bool {
	state := make(map[int]uint8, len(nodes))
	var visit func(int) bool
	visit = func(node int) bool {
		if state[node] == 1 {
			return true
		}
		if state[node] == 2 {
			return false
		}
		state[node] = 1
		for _, next := range adjacency[node] {
			if visit(next) {
				return true
			}
		}
		state[node] = 2
		return false
	}
	for node := range nodes {
		if visit(node) {
			return true
		}
	}
	return false
}

func WorkflowConditionMatches(status coreDB.WorkflowRunStatus, condition coreDB.WorkflowEdgeCondition) bool {
	return false
}

func WorkflowRootNode(workflow coreDB.WorkflowTemplate) (coreDB.WorkflowNode, error) {
	incoming := make(map[int]struct{}, len(workflow.Edges))
	for _, edge := range workflow.Edges {
		incoming[edge.DestinationNodeID] = struct{}{}
	}
	var root *coreDB.WorkflowNode
	for index := range workflow.Nodes {
		node := workflow.Nodes[index]
		if node.EffectiveKind() == coreDB.WorkflowNodeNoteKind {
			continue
		}
		if _, exists := incoming[node.ID]; exists {
			continue
		}
		if root != nil {
			return coreDB.WorkflowNode{}, errors.New("workflow has multiple root nodes")
		}
		root = &node
	}
	if root == nil {
		return coreDB.WorkflowNode{}, errors.New("workflow has no root node")
	}
	return *root, nil
}
