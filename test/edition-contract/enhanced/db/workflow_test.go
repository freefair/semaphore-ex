package db

import (
	"testing"

	coreDB "github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowTemplateStoreStub struct {
	projectID int
	templates map[int]coreDB.Template
}

func (s workflowTemplateStoreStub) GetTemplate(projectID int, templateID int) (coreDB.Template, error) {
	if projectID != s.projectID {
		return coreDB.Template{}, coreDB.ErrNotFound
	}
	template, ok := s.templates[templateID]
	if !ok {
		return coreDB.Template{}, coreDB.ErrNotFound
	}
	return template, nil
}

func validWorkflow() coreDB.WorkflowTemplate {
	return coreDB.WorkflowTemplate{
		ProjectID:         7,
		Name:              "Deploy",
		DefinitionVersion: coreDB.WorkflowDefinitionVersion,
		Nodes: []coreDB.WorkflowNode{
			{ID: -1, Kind: coreDB.WorkflowNodeTaskKind, ConvergenceMode: coreDB.WorkflowConvergenceAll, TemplateID: 11, PositionX: 20, PositionY: 30},
			{ID: -2, Kind: coreDB.WorkflowNodeApprovalKind, ConvergenceMode: coreDB.WorkflowConvergenceAll, PositionX: 200, PositionY: 30},
		},
		Edges: []coreDB.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: coreDB.WorkflowEdgeOnSuccess},
		},
	}
}

func validWorkflowStore() workflowTemplateStoreStub {
	return workflowTemplateStoreStub{projectID: 7, templates: map[int]coreDB.Template{11: {ID: 11, ProjectID: 7}}}
}

func TestNormalizeWorkflowTemplateAppliesSchemaDefaults(t *testing.T) {
	workflow := validWorkflow()
	workflow.DefinitionVersion = 0
	workflow.Name = "  Deploy  "
	workflow.Nodes[0].Kind = ""
	workflow.Nodes[0].ConvergenceMode = ""
	workflow.Edges[0].ID = 0
	workflow.Edges[0].Condition = ""

	normalized := NormalizeWorkflowTemplate(workflow)

	assert.Equal(t, coreDB.WorkflowDefinitionVersion, normalized.DefinitionVersion)
	assert.Equal(t, "Deploy", normalized.Name)
	assert.Equal(t, coreDB.WorkflowNodeTaskKind, normalized.Nodes[0].Kind)
	assert.Equal(t, coreDB.WorkflowConvergenceAll, normalized.Nodes[0].ConvergenceMode)
	assert.Less(t, normalized.Edges[0].ID, 0)
	assert.Equal(t, coreDB.WorkflowEdgeOnSuccess, normalized.Edges[0].Condition)
}

func TestValidateWorkflowTemplateAcceptsValidGraph(t *testing.T) {
	result, err := ValidateWorkflowTemplate(validWorkflowStore(), validWorkflow())

	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Issues)
}

func TestValidateWorkflowTemplateReportsStableLocatedIssues(t *testing.T) {
	workflow := validWorkflow()
	workflow.Name = ""
	workflow.DefinitionVersion = 99
	workflow.Nodes[1].ID = -1
	workflow.Nodes[0].TemplateID = 99
	workflow.Edges = []coreDB.WorkflowEdge{
		{ID: -1, SourceNodeID: -1, DestinationNodeID: -1, Condition: "sometimes"},
		{ID: -1, SourceNodeID: -1, DestinationNodeID: -99, Condition: coreDB.WorkflowEdgeAlways},
	}

	result, err := ValidateWorkflowTemplate(validWorkflowStore(), workflow)

	require.NoError(t, err)
	require.False(t, result.Valid)
	codes := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		codes = append(codes, issue.Code)
		assert.NotEmpty(t, issue.Path)
	}
	assert.Contains(t, codes, "WORKFLOW_NAME_REQUIRED")
	assert.Contains(t, codes, "WORKFLOW_SCHEMA_UNSUPPORTED")
	assert.Contains(t, codes, "WORKFLOW_NODE_ID_DUPLICATE")
	assert.Contains(t, codes, "WORKFLOW_TEMPLATE_NOT_IN_PROJECT")
	assert.Contains(t, codes, "WORKFLOW_EDGE_ID_DUPLICATE")
	assert.Contains(t, codes, "WORKFLOW_EDGE_CONDITION_INVALID")
	assert.Contains(t, codes, "WORKFLOW_SELF_EDGE")
	assert.Contains(t, codes, "WORKFLOW_EDGE_DESTINATION_MISSING")
}

func TestValidateWorkflowTemplateRejectsCycleAndDisconnectedNode(t *testing.T) {
	workflow := validWorkflow()
	workflow.Nodes = append(workflow.Nodes,
		coreDB.WorkflowNode{ID: -3, Kind: coreDB.WorkflowNodeTaskKind, ConvergenceMode: coreDB.WorkflowConvergenceAll, TemplateID: 11},
		coreDB.WorkflowNode{ID: -4, Kind: coreDB.WorkflowNodeTaskKind, ConvergenceMode: coreDB.WorkflowConvergenceAll, TemplateID: 11},
	)
	workflow.Edges = []coreDB.WorkflowEdge{
		{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: coreDB.WorkflowEdgeAlways},
		{ID: -2, SourceNodeID: -2, DestinationNodeID: -1, Condition: coreDB.WorkflowEdgeAlways},
		{ID: -3, SourceNodeID: -3, DestinationNodeID: -4, Condition: coreDB.WorkflowEdgeAlways},
	}

	result, err := ValidateWorkflowTemplate(validWorkflowStore(), workflow)

	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.Contains(t, issueCodes(result), "WORKFLOW_CYCLE")
	assert.Contains(t, issueCodes(result), "WORKFLOW_DISCONNECTED")
}

func TestValidateWorkflowTemplateEnforcesSizeLimits(t *testing.T) {
	workflow := validWorkflow()
	workflow.Nodes = make([]coreDB.WorkflowNode, MaxWorkflowNodes+1)
	for index := range workflow.Nodes {
		workflow.Nodes[index] = coreDB.WorkflowNode{
			ID: index + 1, Kind: coreDB.WorkflowNodeTaskKind,
			ConvergenceMode: coreDB.WorkflowConvergenceAll, TemplateID: 11,
		}
	}
	workflow.Edges = make([]coreDB.WorkflowEdge, MaxWorkflowEdges+1)
	for index := range workflow.Edges {
		workflow.Edges[index] = coreDB.WorkflowEdge{
			ID: index + 1, SourceNodeID: 1, DestinationNodeID: 2,
			Condition: coreDB.WorkflowEdgeAlways,
		}
	}

	result, err := ValidateWorkflowTemplate(validWorkflowStore(), workflow)

	require.NoError(t, err)
	assert.Contains(t, issueCodes(result), "WORKFLOW_NODE_LIMIT_EXCEEDED")
	assert.Contains(t, issueCodes(result), "WORKFLOW_EDGE_LIMIT_EXCEEDED")
}

func issueCodes(result coreDB.WorkflowValidationResult) []string {
	codes := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		codes = append(codes, issue.Code)
	}
	return codes
}
