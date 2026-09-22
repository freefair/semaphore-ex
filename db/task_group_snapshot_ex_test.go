package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskGroupsSurviveImmutableTemplateVersion(t *testing.T) {
	template := Template{ID: 12, ProjectID: 4, RepositoryID: 1, Name: "apply", Playbook: "main.tf",
		TaskGroups: TaskGroupBindings{23, 41}}
	snapshot, err := NewTemplateVersionSnapshot(template)
	require.NoError(t, err)
	template.TaskGroups[0] = 99
	restored, err := snapshot.ReconstructTemplate(4, 12)
	require.NoError(t, err)
	assert.Equal(t, TaskGroupBindings{23, 41}, restored.TaskGroups)
	restored.TaskGroups[0] = 11
	assert.Equal(t, 23, snapshot.Execution.TaskGroups[0])
}
