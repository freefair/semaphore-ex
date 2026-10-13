package pro_interfaces

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWorkflowInputMappingsFingerprintPreservesLegacyAndDetectsChanges(t *testing.T) {
	workflow := db.WorkflowTemplate{Name: "Deploy", Nodes: []db.WorkflowNode{{ID: 1, TemplateID: 11}, {ID: 2, TemplateID: 12}}, Edges: []db.WorkflowEdge{{ID: 8, SourceNodeID: 1, DestinationNodeID: 2, Condition: db.WorkflowEdgeOnSuccess}}}
	legacy, err := WorkflowDefinitionFingerprint(workflow)
	require.NoError(t, err)
	assert.Equal(t, "sha256:703ef8c5fded3fb0babda13a7164f87d218fcc9ad68689018c7ac59d50ff5cd1", legacy)
	workflow.Edges[0].InputMode = db.WorkflowEdgeInputByName
	same, err := WorkflowDefinitionFingerprint(workflow)
	require.NoError(t, err)
	assert.Equal(t, legacy, same)
	workflow.Edges[0].InputMode = db.WorkflowEdgeInputExplicit
	disabled, err := WorkflowDefinitionFingerprint(workflow)
	require.NoError(t, err)
	assert.NotEqual(t, legacy, disabled)
	workflow.Edges[0].InputMappings = []db.WorkflowInputMapping{{Var: "tag", Key: "image-tag"}, {Var: "zone", Key: "region"}}
	before := workflow
	before.Edges = append([]db.WorkflowEdge(nil), workflow.Edges...)
	one, err := WorkflowDefinitionFingerprint(workflow)
	require.NoError(t, err)
	assert.NotEqual(t, disabled, one)
	workflow.Edges[0].InputMappings = []db.WorkflowInputMapping{{Var: "zone", Key: "region"}, {Var: "tag", Key: "image-tag"}}
	reordered, err := WorkflowDefinitionFingerprint(workflow)
	require.NoError(t, err)
	assert.Equal(t, one, reordered)
	workflow.Edges[0].InputMappings[1].Key = "different"
	after, err := WorkflowDefinitionFingerprint(workflow)
	require.NoError(t, err)
	assert.NotEqual(t, one, after)
	diff, err := DiffWorkflowDefinitions(before, workflow)
	require.NoError(t, err)
	assert.Contains(t, diff.ChangedSections(), "edges")
}
