package pro_interfaces

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowDefinitionFingerprintIgnoresPersistenceIdentity(t *testing.T) {
	left := db.WorkflowTemplate{
		ID: 11, ProjectID: 7, Revision: 4, Name: "Deploy", MaxParallelTasks: 2,
		Nodes: []db.WorkflowNode{{ID: 101, Kind: db.WorkflowNodeTaskKind, TemplateID: 31, DisplayName: "Build"}},
	}
	right := left
	right.ID = 99
	right.ProjectID = 8
	right.Revision = 12
	right.Nodes = []db.WorkflowNode{{ID: 501, Kind: db.WorkflowNodeTaskKind, TemplateID: 31, DisplayName: "Build"}}

	leftFingerprint, err := WorkflowDefinitionFingerprint(left)
	require.NoError(t, err)
	rightFingerprint, err := WorkflowDefinitionFingerprint(right)
	require.NoError(t, err)
	assert.Equal(t, leftFingerprint, rightFingerprint)
}

func TestWorkflowDefinitionDiffReportsStructuralSections(t *testing.T) {
	before := db.WorkflowTemplate{
		Name: "Deploy", MaxParallelTasks: 2,
		AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}},
		Nodes:        []db.WorkflowNode{{ID: 1, Kind: db.WorkflowNodeTaskKind, TemplateID: 31, DisplayName: "Build"}},
	}
	after := before
	after.Name = "Release"
	after.MaxParallelTasks = 4
	after.AccessPolicy.ViewRoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager}
	after.Nodes = append(after.Nodes, db.WorkflowNode{ID: 2, Kind: db.WorkflowNodeApprovalKind, DisplayName: "Approve"})

	diff, err := DiffWorkflowDefinitions(before, after)
	require.NoError(t, err)
	assert.Contains(t, diff.ChangedSections(), "metadata")
	assert.Contains(t, diff.ChangedSections(), "nodes")
	assert.Contains(t, diff.ChangedSections(), "permissions")
}

func TestWorkflowDefinitionFingerprintSortsRoleSetsAndEdges(t *testing.T) {
	left := db.WorkflowTemplate{
		Name: "Deploy",
		AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{
			db.BuiltinProjectRoleReferenceManager, db.BuiltinProjectRoleReferenceOwner,
		}},
		Nodes: []db.WorkflowNode{{ID: 10, TemplateID: 1}, {ID: 20, TemplateID: 2}},
		Edges: []db.WorkflowEdge{
			{ID: 2, SourceNodeID: 20, DestinationNodeID: 10, Condition: db.WorkflowEdgeAlways},
			{ID: 1, SourceNodeID: 10, DestinationNodeID: 20, Condition: db.WorkflowEdgeOnSuccess},
		},
	}
	right := left
	right.AccessPolicy.ViewRoleIDs = []db.ProjectRoleReference{
		db.BuiltinProjectRoleReferenceOwner, db.BuiltinProjectRoleReferenceManager,
	}
	right.Edges = []db.WorkflowEdge{left.Edges[1], left.Edges[0]}

	leftFingerprint, err := WorkflowDefinitionFingerprint(left)
	require.NoError(t, err)
	rightFingerprint, err := WorkflowDefinitionFingerprint(right)
	require.NoError(t, err)
	assert.Equal(t, leftFingerprint, rightFingerprint)
}

func TestWorkflowDefinitionDiffReportsNormalizedCrossProjectReference(t *testing.T) {
	snapshot, err := db.NewTemplateVersionSnapshot(db.Template{RepositoryID: 8, Name: "Deploy", Playbook: "deploy.yml"})
	require.NoError(t, err)
	fingerprint, err := db.TemplateVersionFingerprint(snapshot)
	require.NoError(t, err)
	before := db.WorkflowTemplate{Nodes: []db.WorkflowNode{{ID: 1, Kind: db.WorkflowNodeTaskKind, TemplateID: 4, CrossProjectTemplateReference: &db.CrossProjectTemplateReference{
		GrantID: 9, GrantRevision: 2, OwnerProjectID: 3, TemplateID: 4, TemplateVersionID: 5, TemplateVersionNumber: 1, ContentFingerprint: fingerprint,
	}}}}
	after := before
	after.Nodes = append([]db.WorkflowNode(nil), before.Nodes...)
	after.Nodes[0].CrossProjectTemplateReference = &db.CrossProjectTemplateReference{
		GrantID: 9, GrantRevision: 2, OwnerProjectID: 3, TemplateID: 4, TemplateVersionID: 6, TemplateVersionNumber: 2, ContentFingerprint: fingerprint,
	}
	diff, err := DiffWorkflowDefinitions(before, after)
	require.NoError(t, err)
	assert.Contains(t, diff.ChangedSections(), "references")
}
