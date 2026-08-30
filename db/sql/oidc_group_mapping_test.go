package sql

import (
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOIDCGroupMappingApplyTracksOwnershipAndRejectsStaleRevision(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_800_000_000, 0).UTC()
	_, err := store.CreateUserWithoutPassword(db.User{
		Username: "local-admin", Name: "Local Admin", Email: "local-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "oidc-user", Name: "OIDC User", Email: "oidc-user@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "corp", ExternalUID: "subject-1",
	})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "OIDC project", Created: now})
	require.NoError(t, err)
	projectRole, err := store.CreateProjectRole(db.Role{
		ID: "oidc_runner", Name: "OIDC runner", ProjectID: &project.ID,
		Permissions: db.CanRunProjectTasks, Revision: 1,
	})
	require.NoError(t, err)
	globalRole, err := store.CreateGlobalRole(db.Role{
		ID: "oidc_auditor", Name: "OIDC auditor", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)

	projectID := project.ID
	projectMapping, err := store.SaveOIDCGroupMapping(db.OIDCGroupMapping{
		ID: "project-runners", ProviderID: "corp", ClaimValue: "engineering",
		TargetScope: "project", ProjectID: &projectID, RoleID: string(projectRole.ID), Enabled: true,
		Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	globalMapping, err := store.SaveOIDCGroupMapping(db.OIDCGroupMapping{
		ID: "global-auditors", ProviderID: "corp", ClaimValue: "auditors",
		TargetScope: "global", RoleID: string(globalRole.ID), Enabled: true,
		Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	revision, err := store.GetOIDCGroupMappingRevision("corp")
	require.NoError(t, err)
	assert.Equal(t, 3, revision)

	appliedAt := now.Add(time.Second)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "corp", UserID: user.ID, Source: "login", Status: "applied",
		Token: "preview-one", MappingRevision: revision, ClaimRevision: "claim-one",
		PreviewJSON: "{}", AdditionCount: 2, Created: now, AppliedAt: &appliedAt,
	}, []db.OIDCGroupAssignmentChange{
		{MappingID: projectMapping.ID, UserID: user.ID, TargetScope: "project", ProjectID: &projectID, RoleID: string(projectRole.ID)},
		{MappingID: globalMapping.ID, UserID: user.ID, TargetScope: "global", RoleID: string(globalRole.ID)},
	}, nil)
	require.NoError(t, err)

	assignments, err := store.GetOIDCGroupRoleAssignments("corp", user.ID)
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	for _, assignment := range assignments {
		assert.Equal(t, "oidc", assignment.OwnerKind)
		assert.Equal(t, "corp", assignment.ManagedByProviderID)
		assert.NotEmpty(t, assignment.ManagedByMappingID)
	}

	globalMapping.Enabled = false
	globalMapping.Updated = now.Add(2 * time.Second)
	_, err = store.SaveOIDCGroupMapping(globalMapping, globalMapping.Revision)
	require.NoError(t, err)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "corp", UserID: user.ID, Source: "login", Status: "applied",
		Token: "stale", MappingRevision: revision, ClaimRevision: "claim-two",
		PreviewJSON: "{}", Created: now,
	}, nil, nil)
	assert.ErrorIs(t, err, db.ErrOIDCGroupPreviewStale)
}

func TestOIDCGroupMappingApplyIsAtomicOnManualProjectMembershipCollision(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_800_000_100, 0).UTC()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "collision-user", Name: "Collision User", Email: "collision@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "corp", ExternalUID: "subject-2",
	})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Collision project", Created: now})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{
		ID: "mapped_role", Name: "Mapped role", ProjectID: &project.ID,
		Permissions: db.CanRunProjectTasks, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1,
	})
	require.NoError(t, err)
	projectID := project.ID
	mapping, err := store.SaveOIDCGroupMapping(db.OIDCGroupMapping{
		ID: "mapped", ProviderID: "corp", ClaimValue: "engineering", TargetScope: "project",
		ProjectID: &projectID, RoleID: string(role.ID), Enabled: true, Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	revision, err := store.GetOIDCGroupMappingRevision("corp")
	require.NoError(t, err)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "corp", UserID: user.ID, Source: "login", Status: "applied",
		Token: "collision", MappingRevision: revision, ClaimRevision: "claim",
		PreviewJSON: "{}", Created: now,
	}, []db.OIDCGroupAssignmentChange{{
		MappingID: mapping.ID, UserID: user.ID, TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID),
	}}, nil)
	assert.ErrorIs(t, err, db.ErrOIDCGroupMappingCollision)
	persisted, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectGuest, persisted.Role)
	assert.Nil(t, persisted.OIDCGroupManagedAssignmentID)
}

func TestOIDCGroupAssignmentProjectionIdentifiesLDAPAndForeignOIDCOwners(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_800_000_200, 0).UTC()
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("ldap-corp", now, nil)))
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "multi-id-user", Name: "Multi Identity", Email: "multi@example.test", External: true,
	})
	require.NoError(t, err)
	for _, identity := range []db.UserExternalIdentity{
		{UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "provider-a", ExternalUID: "subject-a"},
		{UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "provider-b", ExternalUID: "subject-b"},
		{UserID: user.ID, Type: db.IdentityTypeLdap, Provider: "ldap-corp", ExternalUID: "entryuuid:user-3"},
	} {
		_, err = store.CreateExternalIdentity(identity)
		require.NoError(t, err)
	}
	globalRole, err := store.CreateGlobalRole(db.Role{
		ID: "shared_auditor", Name: "Shared auditor", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	mapping, err := store.SaveOIDCGroupMapping(db.OIDCGroupMapping{
		ID: "shared", ProviderID: "provider-b", ClaimValue: "auditors", TargetScope: "global",
		RoleID: string(globalRole.ID), Enabled: true, Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	revision, err := store.GetOIDCGroupMappingRevision("provider-b")
	require.NoError(t, err)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{
		ProviderID: "provider-b", UserID: user.ID, Source: "login", Status: "applied",
		Token: "foreign-owner", MappingRevision: revision, ClaimRevision: "claim",
		PreviewJSON: "{}", Created: now,
	}, []db.OIDCGroupAssignmentChange{{
		MappingID: mapping.ID, UserID: user.ID, TargetScope: "global", RoleID: string(globalRole.ID),
	}}, nil)
	require.NoError(t, err)

	assignments, err := store.GetOIDCGroupRoleAssignments("provider-a", user.ID)
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	assert.Equal(t, "oidc", assignments[0].OwnerKind)
	assert.Equal(t, "provider-b", assignments[0].ManagedByProviderID)

	ldapRole, err := store.CreateGlobalRole(db.Role{
		ID: "ldap_auditor", Name: "LDAP auditor", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	ldapMapping, err := store.SaveLDAPGroupMapping(db.LDAPGroupMapping{
		ID: "ldap-shared", ProviderID: "ldap-corp",
		GroupExternalID: "entryuuid:50112233-4455-6677-8899-aabbccddeeff",
		TargetScope:     "global", RoleID: string(ldapRole.ID), Enabled: true,
		Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	ldapRevision, err := store.GetLDAPGroupMappingRevision("ldap-corp")
	require.NoError(t, err)
	_, err = store.SaveLDAPGroupReconciliation(db.LDAPGroupReconciliation{
		ProviderID: "ldap-corp", Source: "manual", Status: "preview", Token: "ldap-preview",
		MappingRevision: ldapRevision, DirectoryRevision: "directory", PreviewJSON: "{}", Created: now,
	})
	require.NoError(t, err)
	_, err = store.ApplyLDAPGroupPreview(db.LDAPGroupReconciliation{
		ProviderID: "ldap-corp", Source: "manual", Status: "applied", Token: "ldap-preview",
		MappingRevision: ldapRevision, DirectoryRevision: "directory", PreviewJSON: "{}", Created: now,
	}, []db.LDAPGroupAssignmentChange{{
		MappingID: ldapMapping.ID, UserID: user.ID, TargetScope: "global", RoleID: string(ldapRole.ID),
	}}, nil)
	require.NoError(t, err)
	assignments, err = store.GetOIDCGroupRoleAssignments("provider-a", user.ID)
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	owners := map[string]string{}
	for _, assignment := range assignments {
		owners[assignment.RoleID] = assignment.OwnerKind
	}
	assert.Equal(t, "oidc", owners[string(globalRole.ID)])
	assert.Equal(t, "ldap", owners[string(ldapRole.ID)])
}

func TestMigration22032AddsAndRollsBackOIDCGroupMappingTables(t *testing.T) {
	legacy := "2.20.31"
	store := InitConfigCreateTestStoreAt(&legacy)
	t.Cleanup(store.Close)
	require.NoError(t, store.ApplyMigration(db.Migration{Version: "2.20.32"}))
	revision, err := store.GetOIDCGroupMappingRevision("corp")
	require.NoError(t, err)
	assert.Equal(t, 1, revision)
	assert.Contains(t, sqliteColumnNames(t, store, "project__user"), "oidc_group_managed_assignment_id")
	require.NoError(t, store.TryRollbackMigration(db.Migration{Version: "2.20.32"}))
	applied, err := store.IsMigrationApplied(db.Migration{Version: "2.20.32"})
	require.NoError(t, err)
	assert.False(t, applied)
	assert.NotContains(t, sqliteColumnNames(t, store, "project__user"), "oidc_group_managed_assignment_id")
}

func TestMigration22032UsesMySQLCompatibleRollbackIndexSyntax(t *testing.T) {
	rollback := strings.ToLower(strings.Join(getVersionSQL("mysql", "v2.20.32.err.sql", true), ";"))
	assert.Contains(t, rollback, "drop index `project__user__oidc_group_managed_assignment` on `project__user`")
}
