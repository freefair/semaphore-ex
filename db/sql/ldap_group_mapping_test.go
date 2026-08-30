package sql

import (
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLDAPGroupMappingApplyTracksOwnershipAndRejectsStalePreview(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_780_000_000, 0).UTC()
	provider := readyLDAPProvider("corp", now, nil)
	provider.GroupSearchBaseDN = "ou=groups,dc=example,dc=test"
	provider.GroupUserFilter = "(objectClass=person)"
	provider.GroupFilter = "(objectClass=groupOfNames)"
	provider.GroupIdentityAttribute = "entryUUID"
	provider.GroupMemberAttribute = "member"
	provider.GroupMaxDepth = 4
	require.NoError(t, store.SaveLDAPProvider(provider))

	localAdmin, err := store.CreateUserWithoutPassword(db.User{
		Username: "local-admin", Name: "Local Admin", Email: "local-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	_ = localAdmin
	ldapUser, err := store.CreateUserWithoutPassword(db.User{
		Username: "mapped-user", Name: "Mapped User", Email: "mapped-user@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: ldapUser.ID, Type: db.IdentityTypeLdap, Provider: "corp", ExternalUID: "entryuuid:user-1",
	})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Mapped project", Created: now})
	require.NoError(t, err)
	projectRole, err := store.CreateProjectRole(db.Role{
		ID: "project_runner", Name: "Project runner", ProjectID: &project.ID,
		Permissions: db.CanRunProjectTasks, Revision: 1,
	})
	require.NoError(t, err)
	globalRole, err := store.CreateGlobalRole(db.Role{
		ID: "global_auditor", Name: "Global auditor", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)

	projectID := project.ID
	projectMapping, err := store.SaveLDAPGroupMapping(db.LDAPGroupMapping{
		ID: "project-runners", ProviderID: "corp",
		GroupExternalID: "entryuuid:10112233-4455-6677-8899-aabbccddeeff",
		TargetScope:     "project", ProjectID: &projectID, RoleID: string(projectRole.ID),
		Enabled: true, Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	globalMapping, err := store.SaveLDAPGroupMapping(db.LDAPGroupMapping{
		ID: "global-auditors", ProviderID: "corp",
		GroupExternalID: "entryuuid:20112233-4455-6677-8899-aabbccddeeff",
		TargetScope:     "global", RoleID: string(globalRole.ID), Enabled: true,
		Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, projectMapping.Revision)
	assert.Equal(t, 1, globalMapping.Revision)
	mappingRevision, err := store.GetLDAPGroupMappingRevision("corp")
	require.NoError(t, err)
	assert.Equal(t, 3, mappingRevision)

	preview := db.LDAPGroupReconciliation{
		ProviderID: "corp", Source: "manual", Status: "preview", Token: "preview-one",
		MappingRevision: mappingRevision, DirectoryRevision: "directory-one", PreviewJSON: "{}",
		AdditionCount: 2, Created: now,
	}
	_, err = store.SaveLDAPGroupReconciliation(preview)
	require.NoError(t, err)
	appliedAt := now.Add(time.Second)
	preview.Status = "applied"
	preview.AppliedAt = &appliedAt
	_, err = store.ApplyLDAPGroupPreview(preview, []db.LDAPGroupAssignmentChange{
		{MappingID: projectMapping.ID, UserID: ldapUser.ID, TargetScope: "project", ProjectID: &projectID, RoleID: string(projectRole.ID)},
		{MappingID: globalMapping.ID, UserID: ldapUser.ID, TargetScope: "global", RoleID: string(globalRole.ID)},
	}, nil)
	require.NoError(t, err)

	assignments, err := store.GetLDAPGroupRoleAssignments("corp")
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	for _, assignment := range assignments {
		assert.NotEmpty(t, assignment.ManagedByMappingID)
	}

	globalMapping.Enabled = false
	globalMapping.Updated = now.Add(2 * time.Second)
	_, err = store.SaveLDAPGroupMapping(globalMapping, globalMapping.Revision)
	require.NoError(t, err)
	_, err = store.ApplyLDAPGroupPreview(preview, nil, nil)
	assert.ErrorIs(t, err, db.ErrLDAPGroupPreviewStale)
}

func TestLDAPGroupMappingApplyIsAtomicOnProjectMembershipCollision(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_780_000_100, 0).UTC()
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", now, nil)))
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "collision-user", Name: "Collision User", Email: "collision@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeLdap, Provider: "corp", ExternalUID: "entryuuid:user-2",
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
	mapping, err := store.SaveLDAPGroupMapping(db.LDAPGroupMapping{
		ID: "mapped", ProviderID: "corp", GroupExternalID: "entryuuid:30112233-4455-6677-8899-aabbccddeeff",
		TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID), Enabled: true,
		Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	revision, err := store.GetLDAPGroupMappingRevision("corp")
	require.NoError(t, err)
	preview := db.LDAPGroupReconciliation{
		ProviderID: "corp", Source: "manual", Status: "preview", Token: "collision-preview",
		MappingRevision: revision, DirectoryRevision: "directory", PreviewJSON: "{}", Created: now,
	}
	_, err = store.SaveLDAPGroupReconciliation(preview)
	require.NoError(t, err)
	preview.Status = "applied"
	_, err = store.ApplyLDAPGroupPreview(preview, []db.LDAPGroupAssignmentChange{{
		MappingID: mapping.ID, UserID: user.ID, TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID),
	}}, nil)
	assert.ErrorIs(t, err, db.ErrLDAPGroupMappingCollision)
	persisted, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectGuest, persisted.Role)
	assignments, err := store.GetLDAPGroupRoleAssignments("corp")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	assert.Empty(t, assignments[0].ManagedByMappingID)
}

func TestLDAPGroupMappingRemovalDoesNotDeleteManuallyRecreatedMembership(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_780_000_150, 0).UTC()
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", now, nil)))
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "ownership-user", Name: "Ownership User", Email: "ownership@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeLdap, Provider: "corp", ExternalUID: "entryuuid:user-ownership",
	})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Ownership project", Created: now})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{
		ID: "ownership-role", Name: "Ownership role", ProjectID: &project.ID,
		Permissions: db.CanRunProjectTasks, Revision: 1,
	})
	require.NoError(t, err)
	projectID := project.ID
	mapping, err := store.SaveLDAPGroupMapping(db.LDAPGroupMapping{
		ID: "ownership-mapping", ProviderID: "corp", GroupExternalID: "entryuuid:40112233-4455-6677-8899-aabbccddeeff",
		TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID), Enabled: true,
		Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	revision, err := store.GetLDAPGroupMappingRevision("corp")
	require.NoError(t, err)
	preview := db.LDAPGroupReconciliation{
		ProviderID: "corp", Source: "manual", Status: "preview", Token: "ownership-add",
		MappingRevision: revision, DirectoryRevision: "directory", PreviewJSON: "{}", Created: now,
	}
	_, err = store.SaveLDAPGroupReconciliation(preview)
	require.NoError(t, err)
	preview.Status = "applied"
	change := db.LDAPGroupAssignmentChange{
		MappingID: mapping.ID, UserID: user.ID, TargetScope: "project", ProjectID: &projectID, RoleID: string(role.ID),
	}
	_, err = store.ApplyLDAPGroupPreview(preview, []db.LDAPGroupAssignmentChange{change}, nil)
	require.NoError(t, err)

	require.NoError(t, store.DeleteProjectUser(project.ID, user.ID))
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectNone, RoleID: &role.ID, Revision: 1,
	})
	require.NoError(t, err)
	replacement, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Nil(t, replacement.LDAPGroupManagedAssignmentID)

	removal := db.LDAPGroupReconciliation{
		ProviderID: "corp", Source: "manual", Status: "preview", Token: "ownership-remove",
		MappingRevision: revision, DirectoryRevision: "directory", PreviewJSON: "{}", Created: now,
	}
	_, err = store.SaveLDAPGroupReconciliation(removal)
	require.NoError(t, err)
	removal.Status = "applied"
	_, err = store.ApplyLDAPGroupPreview(removal, nil, []db.LDAPGroupAssignmentChange{change})
	assert.ErrorIs(t, err, db.ErrLDAPGroupMappingCollision)
	persisted, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, role.ID, *persisted.RoleID)
	assert.Nil(t, persisted.LDAPGroupManagedAssignmentID)
}

func TestMigration22031AddsAndRollsBackLDAPGroupMappingTables(t *testing.T) {
	legacy := "2.20.30"
	store := InitConfigCreateTestStoreAt(&legacy)
	t.Cleanup(store.Close)
	now := time.Unix(1_780_000_200, 0).UTC()
	_, err := store.exec(
		`insert into ldap_provider
		 (id, display_name, state, server_url, tls_mode, trust_mode, ca_pem, bind_dn,
		  encrypted_bind_password, search_base_dn, user_filter, identity_attribute,
		  username_attribute, name_attribute, email_attribute, readiness_status,
		  readiness_code, config_version, created, updated)
		 values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		"legacy", "Legacy", "disabled", "ldaps://ldap.example.test:636", "ldaps", "system", "",
		"cn=bind,dc=example,dc=test", "encrypted", "ou=users,dc=example,dc=test",
		"(uid={{username}})", "entryUUID", "uid", "cn", "mail", "untested", "legacy", now, now)
	require.NoError(t, err)
	require.NoError(t, store.ApplyMigration(db.Migration{Version: "2.20.31"}))
	revision, err := store.GetLDAPGroupMappingRevision("legacy")
	require.NoError(t, err)
	assert.Equal(t, 1, revision)
	assert.Contains(t, sqliteColumnNames(t, store, "project__user"), "ldap_group_managed_assignment_id")
	require.NoError(t, store.TryRollbackMigration(db.Migration{Version: "2.20.31"}))
	applied, err := store.IsMigrationApplied(db.Migration{Version: "2.20.31"})
	require.NoError(t, err)
	assert.False(t, applied)
	assert.NotContains(t, sqliteColumnNames(t, store, "project__user"), "ldap_group_managed_assignment_id")
}

func TestMigration22031UsesMySQLCompatibleRollbackIndexSyntax(t *testing.T) {
	rollback := strings.ToLower(strings.Join(getVersionSQL("mysql", "v2.20.31.err.sql", true), ";"))
	assert.Contains(t, rollback, "drop index `project__user__ldap_group_managed_assignment` on `project__user`")
}
