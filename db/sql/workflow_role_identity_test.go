package sql

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveProjectWorkflowRoleIdentityUsesOnlyCurrentProjectMembership(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "manual")

	_, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1,
	})
	require.NoError(t, err)
	identity, err := store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.BuiltinProjectRoleReferenceGuest, identity.Reference)
	assert.Equal(t, db.ProjectGuest.GetPermissions(), identity.Permissions)
	assert.Equal(t, 1, identity.Revision)
	assert.Equal(t, db.ProjectWorkflowRoleOriginBuiltIn, identity.Origin)
	require.NoError(t, identity.Validate())

	require.NoError(t, store.DeleteProjectUser(project.ID, user.ID))
	_, err = store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrProjectWorkflowRoleIdentityUnavailable)
}

func TestResolveProjectWorkflowRoleIdentityUsesCustomRoleIDNotSlug(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "custom")
	role, err := store.CreateProjectRole(db.Role{
		ID: "role_deployer", Slug: "human-readable-but-not-authoritative", Name: "Deployer",
		ProjectID: &project.ID, Permissions: db.CanStartWorkflows, Revision: 7,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectNone, RoleID: &role.ID, Revision: 1,
	})
	require.NoError(t, err)

	identity, err := store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectRoleReferenceForCustomRole(role.ID), identity.Reference)
	assert.Equal(t, db.CanStartWorkflows, identity.Permissions)
	assert.Equal(t, 7, identity.Revision)
	assert.Equal(t, db.ProjectWorkflowRoleOriginManual, identity.Origin)

	_, err = store.Sql().Exec("delete from `role` where project_id=? and role_id=?", project.ID, role.ID)
	require.NoError(t, err)
	_, err = store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrProjectWorkflowRoleIdentityUnavailable)
}

func TestResolveProjectWorkflowRoleIdentityRejectsGlobalRoleAndConflictingOwnership(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "global")
	globalRole, err := store.CreateGlobalRole(db.Role{
		ID: "role_global", Name: "Global", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{UserID: user.ID, RoleID: globalRole.ID, Revision: 1})
	require.NoError(t, err)
	_, err = store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrProjectWorkflowRoleIdentityUnavailable)

	role, err := store.CreateProjectRole(db.Role{
		ID: "role_conflict", Name: "Conflict", ProjectID: &project.ID, Permissions: db.CanViewWorkflows, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		"update project__user set ldap_group_managed_assignment_id=?, oidc_group_managed_assignment_id=? where project_id=? and user_id=?",
		1, 2, project.ID, user.ID,
	)
	require.NoError(t, err)
	_, err = store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrProjectWorkflowRoleIdentityUnavailable)
}

func TestResolveProjectWorkflowRoleIdentityPreservesLDAPAndOIDCProvenance(t *testing.T) {
	for _, test := range []struct {
		name   string
		origin db.ProjectWorkflowRoleOrigin
		setup  func(*testing.T, *SqlDb, db.Project, db.User, db.Role, time.Time)
		value  string
	}{
		{name: "ldap", origin: db.ProjectWorkflowRoleOriginLDAP, setup: setupLDAPWorkflowRoleIdentity, value: "directory-revision"},
		{name: "oidc", origin: db.ProjectWorkflowRoleOriginOIDC, setup: setupOIDCWorkflowRoleIdentity, value: "claim-revision"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			now := time.Unix(1_900_000_000, 0).UTC()
			project, user := workflowRoleIdentityProjectUser(t, store, test.name)
			role, err := store.CreateProjectRole(db.Role{
				ID: db.ProjectRoleID("role_" + test.name), Name: test.name, ProjectID: &project.ID,
				Permissions: db.CanStartWorkflows, Revision: 3,
			})
			require.NoError(t, err)
			test.setup(t, store, project, user, role, now)

			identity, err := store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
			require.NoError(t, err)
			assert.Equal(t, db.ProjectRoleReferenceForCustomRole(role.ID), identity.Reference)
			assert.Equal(t, test.origin, identity.Origin)
			assert.Equal(t, "corp", identity.DirectoryProviderID)
			assert.Equal(t, "workflow-approvers", identity.DirectoryMappingID)
			assert.Positive(t, identity.DirectoryMappingRevision)
			hash := sha256.Sum256([]byte(test.value))
			assert.Equal(t, hex.EncodeToString(hash[:]), identity.DirectoryRevisionFingerprint)
			require.NoError(t, identity.Validate())
		})
	}
}

func TestResolveProjectWorkflowRoleIdentitySupportsDirectoryManagedBuiltInRoles(t *testing.T) {
	for _, test := range []struct {
		name   string
		origin db.ProjectWorkflowRoleOrigin
		setup  func(*testing.T, *SqlDb, db.Project, db.User, time.Time)
		value  string
	}{
		{name: "ldap", origin: db.ProjectWorkflowRoleOriginLDAP, setup: setupLDAPBuiltInWorkflowRoleIdentity, value: "directory-built-in"},
		{name: "oidc", origin: db.ProjectWorkflowRoleOriginOIDC, setup: setupOIDCBuiltInWorkflowRoleIdentity, value: "claim-built-in"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			now := time.Unix(1_900_100_000, 0).UTC()
			project, user := workflowRoleIdentityProjectUser(t, store, "builtin-"+test.name)
			test.setup(t, store, project, user, now)

			identity, err := store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
			require.NoError(t, err)
			assert.Equal(t, db.BuiltinProjectRoleReferenceManager, identity.Reference)
			assert.Equal(t, db.ProjectManager.GetPermissions(), identity.Permissions)
			assert.Equal(t, db.ProjectWorkflowRoleOrigin(test.origin), identity.Origin)
			assert.Equal(t, "corp", identity.DirectoryProviderID)
			assert.Equal(t, "builtin-managers", identity.DirectoryMappingID)
			assert.Equal(t, 4, identity.DirectoryMappingRevision)
			hash := sha256.Sum256([]byte(test.value))
			assert.Equal(t, hex.EncodeToString(hash[:]), identity.DirectoryRevisionFingerprint)
			require.NoError(t, identity.Validate())
		})
	}
}

func workflowRoleIdentityProjectUser(t *testing.T, store *SqlDb, suffix string) (db.Project, db.User) {
	t.Helper()
	project, err := store.CreateProject(db.Project{Name: "workflow identity " + suffix})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "workflow-identity-" + suffix, Name: "Workflow Identity " + suffix,
		Email: "workflow-identity-" + suffix + "@example.test",
	})
	require.NoError(t, err)
	return project, user
}

func setupLDAPWorkflowRoleIdentity(t *testing.T, store *SqlDb, project db.Project, user db.User, role db.Role, now time.Time) {
	t.Helper()
	provider := readyLDAPProvider("corp", now, nil)
	require.NoError(t, store.SaveLDAPProvider(provider))
	projectID := project.ID
	mapping, err := store.SaveLDAPGroupMapping(db.LDAPGroupMapping{
		ID: "workflow-approvers", ProviderID: "corp",
		GroupExternalID: "entryuuid:10112233-4455-6677-8899-aabbccddeeff",
		TargetScope:     "project", ProjectID: &projectID, RoleID: string(role.ID),
		Enabled: true, Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	mappingRevision, err := store.GetLDAPGroupMappingRevision("corp")
	require.NoError(t, err)
	preview := db.LDAPGroupReconciliation{
		ProviderID: "corp", Source: "manual", Status: "preview", Token: "workflow-identity",
		MappingRevision: mappingRevision, DirectoryRevision: "directory-revision", PreviewJSON: "{}", Created: now,
	}
	_, err = store.SaveLDAPGroupReconciliation(preview)
	require.NoError(t, err)
	appliedAt := now.Add(time.Second)
	preview.Status = "applied"
	preview.AppliedAt = &appliedAt
	_, err = store.ApplyLDAPGroupPreview(preview, []db.LDAPGroupAssignmentChange{{
		MappingID: mapping.ID, UserID: user.ID, TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID),
	}}, nil)
	require.NoError(t, err)
}

func setupOIDCWorkflowRoleIdentity(t *testing.T, store *SqlDb, project db.Project, user db.User, role db.Role, now time.Time) {
	t.Helper()
	projectID := project.ID
	mapping, err := store.SaveOIDCGroupMapping(db.OIDCGroupMapping{
		ID: "workflow-approvers", ProviderID: "corp", ClaimValue: "approvers",
		TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID),
		Enabled: true, Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	mappingRevision, err := store.GetOIDCGroupMappingRevision("corp")
	require.NoError(t, err)
	appliedAt := now.Add(time.Second)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "corp", UserID: user.ID, Source: "login", Status: "applied", Token: "workflow-identity",
		MappingRevision: mappingRevision, ClaimRevision: "claim-revision", PreviewJSON: "{}", Created: now, AppliedAt: &appliedAt,
	}, []db.OIDCGroupAssignmentChange{{
		MappingID: mapping.ID, UserID: user.ID, TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID),
	}}, nil)
	require.NoError(t, err)
}

func setupLDAPBuiltInWorkflowRoleIdentity(t *testing.T, store *SqlDb, project db.Project, user db.User, now time.Time) {
	t.Helper()
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", now, nil)))
	_, err := store.Sql().Exec(
		`insert into ldap_group_mapping_state (provider_id, revision) values (?, ?)
		 on conflict(provider_id) do update set revision=excluded.revision`, "corp", 5,
	)
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		`insert into ldap_group_mapping
		 (id, provider_id, group_external_id, target_scope, project_id, role_id, enabled, revision, created, updated)
		 values (?, ?, ?, 'project', ?, 'manager', true, ?, ?, ?)`,
		"builtin-managers", "corp", "entryuuid:10112233-4455-6677-8899-aabbccddeeff", project.ID, 4, now, now,
	)
	require.NoError(t, err)
	appliedAt := now.Add(time.Second)
	_, err = store.Sql().Exec(
		`insert into ldap_group_reconciliation
		 (provider_id, source, status, token, mapping_revision, directory_revision, preview_json,
		  addition_count, removal_count, unresolved_count, collision_count, protected_admin_count,
		  error_code, actor_id, created, applied_at)
		 values (?, 'manual', 'applied', 'builtin-manager', ?, ?, '{}', 0, 0, 0, 0, 0, '', null, ?, ?)`,
		"corp", 5, "directory-built-in", now, appliedAt,
	)
	require.NoError(t, err)
	result, err := store.Sql().Exec(
		`insert into ldap_group_managed_assignment
		 (provider_id, mapping_id, user_id, target_scope, project_id, role_id, global_assignment_id, created)
		 values (?, ?, ?, 'project', ?, 'manager', null, ?)`,
		"corp", "builtin-managers", user.ID, project.ID, now,
	)
	require.NoError(t, err)
	ledgerID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		`insert into project__user
		 (project_id, user_id, role, role_id, revision, ldap_group_managed_assignment_id)
		 values (?, ?, 'manager', null, 1, ?)`, project.ID, user.ID, ledgerID,
	)
	require.NoError(t, err)
}

func setupOIDCBuiltInWorkflowRoleIdentity(t *testing.T, store *SqlDb, project db.Project, user db.User, now time.Time) {
	t.Helper()
	_, err := store.Sql().Exec("insert into oidc_group_mapping_state (provider_id, revision) values (?, ?)", "corp", 5)
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		`insert into oidc_group_mapping
		 (id, provider_id, claim_value, target_scope, project_id, role_id, enabled, revision, created, updated)
		 values (?, ?, 'managers', 'project', ?, 'manager', true, ?, ?, ?)`,
		"builtin-managers", "corp", project.ID, 4, now, now,
	)
	require.NoError(t, err)
	appliedAt := now.Add(time.Second)
	_, err = store.Sql().Exec(
		`insert into oidc_group_reconciliation
		 (provider_id, user_id, source, status, token, mapping_revision, claim_revision, preview_json,
		  addition_count, removal_count, unknown_count, collision_count, protected_admin_count,
		  error_code, actor_id, created, applied_at)
		 values (?, ?, 'login', 'applied', 'builtin-manager', ?, ?, '{}', 0, 0, 0, 0, 0, '', null, ?, ?)`,
		"corp", user.ID, 5, "claim-built-in", now, appliedAt,
	)
	require.NoError(t, err)
	result, err := store.Sql().Exec(
		`insert into oidc_group_managed_assignment
		 (provider_id, mapping_id, user_id, target_scope, project_id, role_id, global_assignment_id, created)
		 values (?, ?, ?, 'project', ?, 'manager', null, ?)`,
		"corp", "builtin-managers", user.ID, project.ID, now,
	)
	require.NoError(t, err)
	ledgerID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		`insert into project__user
		 (project_id, user_id, role, role_id, revision, oidc_group_managed_assignment_id)
		 values (?, ?, 'manager', null, 1, ?)`, project.ID, user.ID, ledgerID,
	)
	require.NoError(t, err)
}

func TestResolveProjectWorkflowRoleIdentityRejectsMalformedDirectoryLedger(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := workflowRoleIdentityProjectUser(t, store, "malformed")
	role, err := store.CreateProjectRole(db.Role{
		ID: "role_malformed", Name: "Malformed", ProjectID: &project.ID, Permissions: db.CanStartWorkflows, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		"update project__user set ldap_group_managed_assignment_id=? where project_id=? and user_id=?", 999, project.ID, user.ID,
	)
	require.NoError(t, err)
	_, err = store.ResolveProjectWorkflowRoleIdentity(project.ID, user.ID)
	assert.True(t, errors.Is(err, db.ErrProjectWorkflowRoleIdentityUnavailable))
}
