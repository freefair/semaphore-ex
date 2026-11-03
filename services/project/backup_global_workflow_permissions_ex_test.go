package project

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEXWorkflowBackupResolvesOnlyExactGlobalRoleIdentity(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	global, err := store.CreateGlobalRole(db.Role{ID: "global-exact", Slug: "shared", Name: "Shared", Revision: 1})
	require.NoError(t, err)
	b := BackupDB{store: store, version: 4}
	got, err := b.workflowGrantRole(BackupWorkflowRole{RoleID: global.ID, Slug: global.Slug, IsGlobal: true})
	require.NoError(t, err)
	assert.Equal(t, global.ID, got.ID)
	_, err = b.workflowGrantRole(BackupWorkflowRole{RoleID: "other-instance", Slug: global.Slug, IsGlobal: true})
	assert.ErrorIs(t, err, db.ErrNotFound, "a shared slug must not redirect the grant to another role")
	builtin, err := b.workflowGrantRole(BackupWorkflowRole{RoleID: "builtin:guest", Slug: "guest", IsGlobal: true})
	require.NoError(t, err)
	assert.Equal(t, db.ProjectRoleID("builtin:guest"), builtin.ID)
}
