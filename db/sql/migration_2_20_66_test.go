package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration_2_20_18_EX_1_3_SplitsAndRestoresEveryLegacyCustomRoleBit(t *testing.T) {
	target := "2.20.18-ex1.2"
	store := InitConfigCreateTestStoreAt(&target)
	t.Cleanup(store.Close)

	project, err := store.CreateProject(db.Project{Name: "resource permission migration"})
	require.NoError(t, err)
	legacyUnrelated := db.CanUpdateProject | db.CanManageProjectUsers |
		db.CanViewWorkflows | db.CanEditWorkflows | db.CanStartWorkflows | db.CanStopWorkflows |
		db.CanAdministerWorkflows | db.CanListGrantedCredentials | db.CanConsumeGrantedCredentials |
		db.CanOverrideDeploymentWindow | db.CanManagePolicyGuardrails | db.CanRollbackPolicyGuardrails |
		db.CanReadTaskGroups | db.CanCreateTaskGroups | db.CanUpdateTaskGroups | db.CanDeleteTaskGroups |
		db.CanShareTaskGroups | db.CanApproveWorkflows
	readProjectResourcePermissions := db.CanViewTemplates | db.CanViewAccessKeys |
		db.CanViewEnvironments | db.CanViewSchedules |
		db.CanViewIntegrations | db.CanViewViews

	type storedRole struct {
		name    string
		project bool
		mask    db.ProjectUserPermission
		role    db.Role
	}
	cases := make([]storedRole, 0, 32)
	legacyCombinations := []struct {
		name string
		mask db.ProjectUserPermission
	}{
		{name: "none", mask: 0},
		{name: "read", mask: db.CanReadProject},
		{name: "run", mask: db.CanRunProjectTasks},
		{name: "view", mask: db.CanViewProjectResources},
		{name: "manage", mask: db.CanManageProjectResources},
		{name: "read_run", mask: db.CanReadProject | db.CanRunProjectTasks},
		{name: "read_view", mask: db.CanReadProject | db.CanViewProjectResources},
		{name: "read_manage", mask: db.CanReadProject | db.CanManageProjectResources},
		{name: "run_view", mask: db.CanRunProjectTasks | db.CanViewProjectResources},
		{name: "run_manage", mask: db.CanRunProjectTasks | db.CanManageProjectResources},
		{name: "view_manage", mask: db.CanViewProjectResources | db.CanManageProjectResources},
		{name: "read_run_view", mask: db.CanReadProject | db.CanRunProjectTasks | db.CanViewProjectResources},
		{name: "read_run_manage", mask: db.CanReadProject | db.CanRunProjectTasks | db.CanManageProjectResources},
		{name: "read_view_manage", mask: db.CanReadProject | db.CanViewProjectResources | db.CanManageProjectResources},
		{name: "run_view_manage", mask: db.CanRunProjectTasks | db.CanViewProjectResources | db.CanManageProjectResources},
		{name: "all", mask: db.CanReadProject | db.CanRunProjectTasks | db.CanViewProjectResources | db.CanManageProjectResources},
	}
	for _, legacy := range legacyCombinations {
		cases = append(cases,
			storedRole{name: "project_" + legacy.name, project: true, mask: legacyUnrelated | legacy.mask},
			storedRole{name: "global_" + legacy.name, mask: legacyUnrelated | legacy.mask},
		)
	}
	for index := range cases {
		role := db.Role{ID: db.ProjectRoleID("legacy_resource_" + cases[index].name), Name: "Legacy " + cases[index].name, Permissions: cases[index].mask, Revision: 1}
		if cases[index].project {
			role.ProjectID = &project.ID
			cases[index].role, err = store.CreateProjectRole(role)
		} else {
			cases[index].role, err = store.CreateGlobalRole(role)
		}
		require.NoError(t, err)
		_, err = store.exec("update role set permissions=? where role_id=?", cases[index].mask, cases[index].role.ID)
		require.NoError(t, err)
	}

	migration := db.Migration{Version: "2.20.18-ex1.3"}
	require.NoError(t, store.ApplyMigration(migration))

	for _, testCase := range cases {
		t.Run(testCase.name+" upgrade", func(t *testing.T) {
			var migrated db.Role
			if testCase.project {
				migrated, err = store.GetProjectRoleByID(project.ID, testCase.role.ID)
			} else {
				migrated, err = store.GetGlobalRoleByID(testCase.role.ID)
			}
			require.NoError(t, err)
			assert.Equal(t, legacyUnrelated|testCase.mask&db.CanRunProjectTasks, migrated.Permissions&(legacyUnrelated|db.CanRunProjectTasks))
			assert.Zero(t, migrated.Permissions&(db.CanViewProjectResources|db.CanManageProjectResources))
			var expectedViews db.ProjectUserPermission
			if testCase.mask&db.CanReadProject != 0 {
				expectedViews |= readProjectResourcePermissions
			}
			if testCase.mask&db.CanViewProjectResources != 0 {
				expectedViews |= db.AllProjectResourceViewPermissions
			}
			assert.Equal(t, expectedViews, migrated.Permissions&db.AllProjectResourceViewPermissions)
			if testCase.mask&db.CanManageProjectResources != 0 {
				assert.Equal(t, db.AllProjectResourceMutationPermissions, migrated.Permissions&db.AllProjectResourceMutationPermissions)
			} else {
				assert.Zero(t, migrated.Permissions&db.AllProjectResourceMutationPermissions)
			}
		})
	}

	require.NoError(t, store.TryRollbackMigration(migration))
	for _, testCase := range cases {
		t.Run(testCase.name+" rollback", func(t *testing.T) {
			var restored db.Role
			if testCase.project {
				restored, err = store.GetProjectRoleByID(project.ID, testCase.role.ID)
			} else {
				restored, err = store.GetGlobalRoleByID(testCase.role.ID)
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.mask, restored.Permissions)
		})
	}
}
