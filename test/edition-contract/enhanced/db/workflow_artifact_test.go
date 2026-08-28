package db

import (
	"testing"

	coreDB "github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareWorkflowTemplateDefaultsAndValidatesArtifactGraph(t *testing.T) {
	workflow := validWorkflow()
	workflow.Nodes[1].Kind = coreDB.WorkflowNodeTaskKind
	workflow.Nodes[1].TemplateID = 11
	workflow.Nodes[0].ArtifactOutputs = []coreDB.WorkflowArtifactDeclaration{{
		Name: "release", Schema: coreDB.WorkflowArtifactSchema{Type: coreDB.WorkflowArtifactString},
	}}
	workflow.Nodes[1].ArtifactInputs = []coreDB.WorkflowArtifactReference{{
		Name: "release_input", SourceNodeID: -1, Output: "release", Required: true,
	}}

	prepared, result, err := PrepareWorkflowTemplate(validWorkflowStore(), workflow)
	require.NoError(t, err)
	require.True(t, result.Valid, result.Issues)
	require.Len(t, prepared.Nodes[0].ArtifactOutputs, 1)
	assert.Equal(t, coreDB.DefaultWorkflowArtifactMaxBytes, prepared.Nodes[0].ArtifactOutputs[0].MaxBytes)
	assert.NotEmpty(t, prepared.Nodes[0].ArtifactOutputsJSON)
	assert.NotEmpty(t, prepared.Nodes[1].ArtifactInputsJSON)
}

func TestValidateWorkflowTemplateRejectsUnreachableArtifactInput(t *testing.T) {
	workflow := validWorkflow()
	workflow.Nodes[1].Kind = coreDB.WorkflowNodeTaskKind
	workflow.Nodes[1].TemplateID = 11
	workflow.Nodes[0].ArtifactOutputs = []coreDB.WorkflowArtifactDeclaration{{
		Name: "release", Schema: coreDB.WorkflowArtifactSchema{Type: coreDB.WorkflowArtifactString}, MaxBytes: 64,
	}}
	workflow.Nodes[1].ArtifactInputs = []coreDB.WorkflowArtifactReference{{
		Name: "release_input", SourceNodeID: -1, Output: "release", Required: true,
	}}
	workflow.Edges = nil

	result, err := ValidateWorkflowTemplate(validWorkflowStore(), workflow)
	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.Contains(t, issueCodes(result), "WORKFLOW_ARTIFACT_SOURCE_UNREACHABLE")
}
