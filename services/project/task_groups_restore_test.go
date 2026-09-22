package project

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupRestoreRejectsSourceTaskGroupReferences(t *testing.T) {
	backup := BackupFormat{Templates: []BackupTemplate{{Template: db.Template{TaskGroups: db.TaskGroupBindings{42}}}}}

	err := backup.rejectTaskGroupsForRestore()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "task group memberships")
	assert.Equal(t, db.TaskGroupBindings{42}, backup.Templates[0].TaskGroups)
}
