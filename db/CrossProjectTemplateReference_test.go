package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossProjectTemplateProvenanceCanonicalizesExactVersionSnapshot(t *testing.T) {
	snapshot, err := NewTemplateVersionSnapshot(Template{RepositoryID: 9, Name: "Deploy", Playbook: "deploy.yml", EnvironmentIDs: []int{8, 3}})
	require.NoError(t, err)
	fingerprint, err := TemplateVersionFingerprint(snapshot)
	require.NoError(t, err)
	provenance := CrossProjectTemplateProvenance{
		Reference:        CrossProjectTemplateReference{GrantID: 7, TemplateVersionNumber: 2, OwnerProjectID: 1, TemplateID: 4, TemplateVersionID: 5, ContentFingerprint: fingerprint, GrantRevision: 3},
		TemplateSnapshot: snapshot,
	}
	payload, err := provenance.CanonicalJSON()
	require.NoError(t, err)
	decoded, err := DecodeCrossProjectTemplateProvenance(payload)
	require.NoError(t, err)
	assert.Equal(t, provenance.Reference, decoded.Reference)
	assert.Equal(t, []int{8, 3}, decoded.TemplateSnapshot.Dependencies.EnvironmentIDs)
}

func TestCrossProjectTemplateReferenceRejectsClientDerivedProvenance(t *testing.T) {
	reference := CrossProjectTemplateReference{GrantID: 1, TemplateVersionNumber: 2}
	require.NoError(t, reference.ValidateInput())
	assert.Error(t, reference.ValidateNormalized())
}

func TestWorkflowTaskProvenanceReservesValidatedCrossProjectSeam(t *testing.T) {
	snapshot, err := NewTemplateVersionSnapshot(Template{RepositoryID: 9, Name: "Deploy", Playbook: "deploy.yml"})
	require.NoError(t, err)
	fingerprint, err := TemplateVersionFingerprint(snapshot)
	require.NoError(t, err)
	provenance := WorkflowTemplateProvenance{CrossProject: &CrossProjectTemplateProvenance{
		Reference:        CrossProjectTemplateReference{GrantID: 7, TemplateVersionNumber: 2, OwnerProjectID: 1, TemplateID: 4, TemplateVersionID: 5, ContentFingerprint: fingerprint, GrantRevision: 3},
		TemplateSnapshot: snapshot,
	}}
	require.NoError(t, provenance.Validate())
	task := Task{WorkflowTemplateProvenance: &provenance}
	assert.NotNil(t, task.WorkflowTemplateProvenance)
}
