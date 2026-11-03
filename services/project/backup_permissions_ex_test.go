package project

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEXBackupPermissionSchemaRoundTripPreservesDeniesAndWorkflowGrants(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{TmpPath: t.TempDir()}
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createBackupFixture(t, store)
	role, err := store.CreateProjectRole(db.Role{ID: "roundtrip-role", Name: "Scoped runner", ProjectID: &project.ID, Permissions: db.CanStartWorkflows, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: project.ID, TemplateID: template.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate | db.CanRunTemplate, DeniedPermissions: db.CanEditTemplate})
	require.NoError(t, err)
	workflows := proFactory.NewWorkflowStore(store)
	workflow, err := workflows.CreateWorkflowTemplate(db.WorkflowTemplate{ProjectID: project.ID, Name: "Deploy", DefinitionVersion: db.WorkflowDefinitionVersion, Nodes: []db.WorkflowNode{{ID: -1, Kind: db.WorkflowNodeTaskKind, TemplateID: template.ID, ConvergenceMode: db.WorkflowConvergenceAll}}})
	require.NoError(t, err)
	_, err = store.CreateWorkflowRole(db.WorkflowRolePerm{ProjectID: project.ID, WorkflowID: workflow.ID, RoleID: role.ID, Permissions: db.CanReadProject | db.CanStartWorkflows})
	require.NoError(t, err)
	backup, err := GetBackup(project.ID, store, workflows)
	require.NoError(t, err)
	encoded, err := backup.Marshal()
	require.NoError(t, err)
	var restoredFile BackupFormat
	require.NoError(t, restoredFile.Unmarshal(encoded))
	assert.Equal(t, "semaphore-ex", restoredFile.PermissionSchema)
	restoredFile.Meta.Name = "Copied permissions"
	restored, err := restoredFile.Restore(restoreUser(t, store), store, workflows)
	require.NoError(t, err)
	roles, err := store.GetProjectRoles(restored.ID)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.NotEqual(t, role.ID, roles[0].ID)
	assert.Equal(t, db.CanStartWorkflows, roles[0].Permissions, "marked EX backups must not gain project read or approval")
	templates, err := store.GetTemplates(restored.ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	grants, err := store.GetTemplateRoles(restored.ID, templates[0].ID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, db.CanReadTemplate|db.CanRunTemplate, grants[0].AllowedPermissions)
	assert.Equal(t, db.CanEditTemplate, grants[0].DeniedPermissions)
	copies, err := workflows.GetWorkflowTemplates(restored.ID, nil, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, copies, 1)
	workflowGrants, err := store.GetWorkflowRoles(restored.ID, copies[0].ID)
	require.NoError(t, err)
	require.Len(t, workflowGrants, 1)
	assert.Equal(t, roles[0].ID, workflowGrants[0].RoleID)
	assert.Equal(t, db.CanReadProject|db.CanStartWorkflows, workflowGrants[0].Permissions)
}

func TestEXPermissionSchemaRequiresCurrentBackupVersion(t *testing.T) {
	v2, v3, v4 := 2, 3, 4
	tests := []struct {
		name    string
		schema  string
		version *int
		valid   bool
	}{
		{"legacy EX without version", "", nil, true},
		{"marked EX without version", "semaphore-ex", nil, false},
		{"marked EX version 2", "semaphore-ex", &v2, false},
		{"marked EX version 3", "semaphore-ex", &v3, false},
		{"marked EX version 4", "semaphore-ex", &v4, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&BackupFormat{PermissionSchema: tt.schema, Version: tt.version}).Verify()
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
