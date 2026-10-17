package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoryRoleRemovalReprojectsSurvivingRole(t *testing.T) {
	for _, test := range []struct {
		name              string
		removedProvider   string
		survivingProvider string
	}{
		{name: "ldap removal keeps manual role", removedProvider: "ldap", survivingProvider: "manual"},
		{name: "ldap removal keeps oidc role and provenance", removedProvider: "ldap", survivingProvider: "oidc"},
		{name: "oidc removal keeps manual role", removedProvider: "oidc", survivingProvider: "manual"},
		{name: "oidc removal keeps ldap role and provenance", removedProvider: "oidc", survivingProvider: "ldap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			now := time.Unix(1_900_400_000, 0).UTC()
			project, user := workflowRoleIdentityProjectUser(t, store, test.name)
			ldapRole, err := store.CreateProjectRole(db.Role{ID: "directory-reprojection-ldap", Name: "LDAP role", ProjectID: &project.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
			require.NoError(t, err)
			oidcRole, err := store.CreateProjectRole(db.Role{ID: "directory-reprojection-oidc", Name: "OIDC role", ProjectID: &project.ID, Permissions: db.CanStartWorkflows, Revision: 1})
			require.NoError(t, err)

			if test.removedProvider == "ldap" {
				setupLDAPWorkflowRoleIdentity(t, store, project, user, ldapRole, now)
			} else {
				setupOIDCWorkflowRoleIdentity(t, store, project, user, oidcRole, now)
			}
			switch test.survivingProvider {
			case "ldap":
				setupLDAPWorkflowRoleIdentity(t, store, project, user, ldapRole, now.Add(time.Minute))
			case "oidc":
				setupOIDCWorkflowRoleIdentity(t, store, project, user, oidcRole, now.Add(time.Minute))
			default:
				_, err = store.Sql().Exec("insert into project__user_role (project_id, user_id, role_id) values (?, ?, ?)", project.ID, user.ID, db.BuiltinProjectRoleReferenceManager)
				require.NoError(t, err)
			}

			before, err := store.GetProjectUser(project.ID, user.ID)
			require.NoError(t, err)
			if test.removedProvider == "ldap" {
				removeLDAPDirectoryRoleForTest(t, store, project.ID, user.ID, ldapRole.ID)
			} else {
				removeOIDCDirectoryRoleForTest(t, store, project.ID, user.ID, oidcRole.ID)
			}

			projection, err := store.GetProjectUser(project.ID, user.ID)
			require.NoError(t, err)
			assert.Equal(t, before.Revision+1, projection.Revision)

			type relation struct {
				RoleID db.ProjectRoleID `db:"role_id"`
				LDAP   *int             `db:"ldap_group_managed_assignment_id"`
				OIDC   *int             `db:"oidc_group_managed_assignment_id"`
			}
			var remaining relation
			err = store.Sql().SelectOne(&remaining, "select role_id, ldap_group_managed_assignment_id, oidc_group_managed_assignment_id from project__user_role where project_id=? and user_id=?", project.ID, user.ID)
			require.NoError(t, err)
			assert.Equal(t, remaining.RoleID, projectedRoleReference(projection))
			assert.Equal(t, remaining.LDAP, projection.LDAPGroupManagedAssignmentID)
			assert.Equal(t, remaining.OIDC, projection.OIDCGroupManagedAssignmentID)
			if test.survivingProvider == "manual" {
				assert.Equal(t, db.ProjectManager, projection.Role)
				assert.Nil(t, projection.RoleID)
			} else if test.survivingProvider == "ldap" {
				require.NotNil(t, projection.RoleID)
				assert.Equal(t, ldapRole.ID, *projection.RoleID)
			} else {
				require.NotNil(t, projection.RoleID)
				assert.Equal(t, oidcRole.ID, *projection.RoleID)
			}

			identities, err := store.ResolveProjectWorkflowRoleIdentities(project.ID, user.ID)
			require.NoError(t, err)
			require.Len(t, identities, 1, "the revoked relation must not remain effective")
			if test.survivingProvider == "manual" {
				assert.Equal(t, db.BuiltinProjectRoleReferenceManager, identities[0].Reference)
				assert.Equal(t, db.ProjectWorkflowRoleOriginBuiltIn, identities[0].Origin)
			} else if test.survivingProvider == "ldap" {
				assert.Equal(t, db.ProjectRoleReferenceForCustomRole(ldapRole.ID), identities[0].Reference)
				assert.Equal(t, db.ProjectWorkflowRoleOriginLDAP, identities[0].Origin)
			} else {
				assert.Equal(t, db.ProjectRoleReferenceForCustomRole(oidcRole.ID), identities[0].Reference)
				assert.Equal(t, db.ProjectWorkflowRoleOriginOIDC, identities[0].Origin)
			}
		})
	}
}

func TestDirectoryRoleRemovalBumpsRevisionForSecondaryRoleWithoutChangingProjection(t *testing.T) {
	for _, test := range []struct {
		name               string
		projectionProvider string
	}{
		{name: "ldap secondary to oidc projection", projectionProvider: "oidc"},
		{name: "oidc secondary to ldap projection", projectionProvider: "ldap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			now := time.Unix(1_900_500_000, 0).UTC()
			project, user := workflowRoleIdentityProjectUser(t, store, test.name)
			ldapRole, err := store.CreateProjectRole(db.Role{ID: "directory-secondary-ldap", Name: "LDAP secondary", ProjectID: &project.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
			require.NoError(t, err)
			oidcRole, err := store.CreateProjectRole(db.Role{ID: "directory-secondary-oidc", Name: "OIDC secondary", ProjectID: &project.ID, Permissions: db.CanStartWorkflows, Revision: 1})
			require.NoError(t, err)

			if test.projectionProvider == "oidc" {
				setupOIDCWorkflowRoleIdentity(t, store, project, user, oidcRole, now)
				setupLDAPWorkflowRoleIdentity(t, store, project, user, ldapRole, now.Add(time.Minute))
			} else {
				setupLDAPWorkflowRoleIdentity(t, store, project, user, ldapRole, now)
				setupOIDCWorkflowRoleIdentity(t, store, project, user, oidcRole, now.Add(time.Minute))
			}

			before, err := store.GetProjectUser(project.ID, user.ID)
			require.NoError(t, err)
			if test.projectionProvider == "oidc" {
				removeLDAPDirectoryRoleForTest(t, store, project.ID, user.ID, ldapRole.ID)
			} else {
				removeOIDCDirectoryRoleForTest(t, store, project.ID, user.ID, oidcRole.ID)
			}

			after, err := store.GetProjectUser(project.ID, user.ID)
			require.NoError(t, err)
			assert.Equal(t, before.Revision+1, after.Revision)
			assert.Equal(t, before.Role, after.Role)
			assert.Equal(t, before.RoleID, after.RoleID)
			assert.Equal(t, before.LDAPGroupManagedAssignmentID, after.LDAPGroupManagedAssignmentID)
			assert.Equal(t, before.OIDCGroupManagedAssignmentID, after.OIDCGroupManagedAssignmentID)

			remaining, err := store.Sql().SelectInt(store.PrepareQuery("select count(*) from project__user_role where project_id=? and user_id=?"), project.ID, user.ID)
			require.NoError(t, err)
			assert.Equal(t, int64(1), remaining)
		})
	}
}

func TestProjectRoleAssignmentProtectedAdministratorExcludesOnlyRemovedRelation(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "protected-administrator")
	adminOne, err := store.CreateProjectRole(db.Role{ID: "directory-admin-one", Name: "Directory admin one", ProjectID: &project.ID, Permissions: db.CanManageProjectUsers, Revision: 1})
	require.NoError(t, err)
	adminTwo, err := store.CreateProjectRole(db.Role{ID: "directory-admin-two", Name: "Directory admin two", ProjectID: &project.ID, Permissions: db.CanManageProjectUsers, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Role: db.ProjectManager, Revision: 1})
	require.NoError(t, err)
	_, err = store.Sql().Exec("insert into project__user_role (project_id, user_id, role_id) values (?, ?, ?)", project.ID, user.ID, adminOne.ID)
	require.NoError(t, err)

	protected, err := store.projectRoleAssignmentProtectedAdministrator(project.ID, user.ID, adminOne.ID, adminOne.Permissions)
	require.NoError(t, err)
	assert.True(t, protected, "a manager without CanManageProjectUsers cannot replace the sole directory administrator")

	_, err = store.Sql().Exec("insert into project__user_role (project_id, user_id, role_id) values (?, ?, ?)", project.ID, user.ID, adminTwo.ID)
	require.NoError(t, err)
	for _, role := range []db.Role{adminOne, adminTwo} {
		protected, err = store.projectRoleAssignmentProtectedAdministrator(project.ID, user.ID, role.ID, role.Permissions)
		require.NoError(t, err)
		assert.False(t, protected, "the member retains its other administrator relation")
	}
}

func projectedRoleReference(member db.ProjectUser) db.ProjectRoleID {
	if member.RoleID != nil {
		return *member.RoleID
	}
	return db.ProjectRoleID("builtin:" + string(member.Role))
}

func removeLDAPDirectoryRoleForTest(t *testing.T, store *SqlDb, projectID, userID int, roleID db.ProjectRoleID) {
	t.Helper()
	tx, err := store.Sql().Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, removeLDAPManagedAssignmentTx(tx, store, "corp", db.LDAPGroupAssignmentChange{MappingID: "workflow-approvers", UserID: userID, TargetScope: "project", ProjectID: &projectID, RoleID: string(roleID)}))
	require.NoError(t, tx.Commit())
}

func removeOIDCDirectoryRoleForTest(t *testing.T, store *SqlDb, projectID, userID int, roleID db.ProjectRoleID) {
	t.Helper()
	tx, err := store.Sql().Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, removeOIDCManagedAssignmentTx(tx, store, "corp", db.OIDCGroupAssignmentChange{MappingID: "workflow-approvers", UserID: userID, TargetScope: "project", ProjectID: &projectID, RoleID: string(roleID)}))
	require.NoError(t, tx.Commit())
}
