package db

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTemplateVersionSnapshotPreservesWorkingDirectory(t *testing.T) {
	directory := "deploy/ansible"
	source := Template{RepositoryID: 9, Name: "Deploy", Playbook: "deploy.yml", WorkingDirectory: &directory}
	snapshot, err := NewTemplateVersionSnapshot(source)
	require.NoError(t, err)
	fingerprint, err := TemplateVersionFingerprint(snapshot)
	require.NoError(t, err)
	directory = "edited/live"
	restored, err := snapshot.ReconstructTemplate(4, 12)
	require.NoError(t, err)
	require.NotNil(t, restored.WorkingDirectory)
	assert.Equal(t, "deploy/ansible", *restored.WorkingDirectory)
	changed, err := NewTemplateVersionSnapshot(source)
	require.NoError(t, err)
	changedFingerprint, err := TemplateVersionFingerprint(changed)
	require.NoError(t, err)
	assert.NotEqual(t, fingerprint, changedFingerprint)
	*restored.WorkingDirectory = "edited/restored"
	again, err := snapshot.ReconstructTemplate(4, 12)
	require.NoError(t, err)
	require.NotNil(t, again.WorkingDirectory)
	assert.Equal(t, "deploy/ansible", *again.WorkingDirectory)
}
