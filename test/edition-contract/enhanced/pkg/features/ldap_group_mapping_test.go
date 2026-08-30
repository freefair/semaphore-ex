package features

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLDAPGroupMappingPreviewApplyStaleAndOutageLifecycle(t *testing.T) {
	store, service, client, now := newLDAPServiceTest(t)
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "mapping-admin", Name: "Mapping Admin", Email: "mapping-admin@example.test", Admin: true,
	}, Pwd: "local-password"})
	require.NoError(t, err)
	_, err = service.Configure(context.Background(), pro_interfaces.LDAPConfigureRequest{
		ActorID: admin.ID, ActorIsAdmin: true, Provider: testLDAPProviderInput(), Now: now,
	})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "mapped-user", Name: "Mapped User", Email: "mapped-user@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeLdap, Provider: "corp", ExternalUID: "entryuuid:user-1",
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
	projectMapping, err := service.SaveGroupMapping(context.Background(), pro_interfaces.LDAPGroupMappingRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ExpectedRevision: 0, Now: now,
		Mapping: pro_interfaces.LDAPGroupMapping{
			ID: "project-runners", ProviderID: "corp", Enabled: true,
			GroupExternalID: "entryuuid:10112233-4455-6677-8899-aabbccddeeff",
			Target: pro_interfaces.LDAPRoleTarget{
				Scope: pro_interfaces.LDAPRoleScopeProject, ProjectID: project.ID, RoleID: string(projectRole.ID),
			},
		},
	})
	require.NoError(t, err)
	_, err = service.SaveGroupMapping(context.Background(), pro_interfaces.LDAPGroupMappingRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ExpectedRevision: 0, Now: now,
		Mapping: pro_interfaces.LDAPGroupMapping{
			ID: "global-auditors", ProviderID: "corp", Enabled: true,
			GroupExternalID: "entryuuid:20112233-4455-6677-8899-aabbccddeeff",
			Target:          pro_interfaces.LDAPRoleTarget{Scope: pro_interfaces.LDAPRoleScopeGlobal, RoleID: string(globalRole.ID)},
		},
	})
	require.NoError(t, err)
	client.groupSnapshot = pro_interfaces.LDAPGroupDirectorySnapshot{
		Revision: "directory-one", CapturedAt: now,
		GroupExternalIDs: []string{
			"entryuuid:10112233-4455-6677-8899-aabbccddeeff",
			"entryuuid:20112233-4455-6677-8899-aabbccddeeff",
		},
		Users: []pro_interfaces.LDAPDirectoryUser{{
			ExternalID: "entryuuid:user-1",
			GroupExternalIDs: []string{
				"entryuuid:10112233-4455-6677-8899-aabbccddeeff",
				"entryuuid:20112233-4455-6677-8899-aabbccddeeff",
			},
		}},
	}
	actorID := admin.ID
	preview, err := service.PreviewGroupMappings(context.Background(), pro_interfaces.LDAPGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: true, ProviderID: "corp", Source: "manual", Now: now.Add(time.Second),
	})
	require.NoError(t, err)
	assert.Len(t, preview.Additions, 2)
	applyRequest := pro_interfaces.LDAPGroupApplyRequest{
		ActorID: admin.ID, ActorIsAdmin: true, ProviderID: "corp", Now: now.Add(2 * time.Second),
	}
	applyRequest.Token = preview.Token
	_, err = service.ApplyGroupPreview(context.Background(), applyRequest)
	require.NoError(t, err)

	client.groupSnapshot.Revision = "directory-two"
	client.groupSnapshot.Users[0].GroupExternalIDs = client.groupSnapshot.Users[0].GroupExternalIDs[1:]
	stalePreview, err := service.PreviewGroupMappings(context.Background(), pro_interfaces.LDAPGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: true, ProviderID: "corp", Source: "manual", Now: now.Add(3 * time.Second),
	})
	require.NoError(t, err)
	require.Len(t, stalePreview.Removals, 1)
	assert.Equal(t, projectMapping.ID, stalePreview.Removals[0].MappingID)
	client.groupSnapshot.Revision = "directory-three"
	applyRequest.Token = stalePreview.Token
	applyRequest.Now = now.Add(4 * time.Second)
	_, err = service.ApplyGroupPreview(context.Background(), applyRequest)
	assert.ErrorIs(t, err, pro_interfaces.ErrLDAPGroupPreviewStale)

	client.groupSnapshot.Revision = "directory-two"
	applied, err := service.ReconcileGroupMappings(context.Background(), pro_interfaces.LDAPGroupPreviewRequest{
		ProviderID: "corp", Source: "scheduled", Now: now.Add(5 * time.Second),
	})
	require.NoError(t, err)
	assert.Len(t, applied.Removals, 1)
	_, err = store.GetProjectUser(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	globalAssignments, err := store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	require.Len(t, globalAssignments, 1)

	client.groupErr = pro_interfaces.ErrLDAPProviderUnavailable
	_, err = service.ReconcileGroupMappings(context.Background(), pro_interfaces.LDAPGroupPreviewRequest{
		ProviderID: "corp", Source: "scheduled", Now: now.Add(6 * time.Second),
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrLDAPProviderUnavailable)
	globalAssignments, err = store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	require.Len(t, globalAssignments, 1)
	history, err := service.GroupReconciliationHistory(context.Background(), "corp", 20)
	require.NoError(t, err)
	assert.Contains(t, reconciliationStatuses(history), "stale")
}

func TestLDAPLoginRefreshesAuthenticatedUsersManagedAssignments(t *testing.T) {
	store, service, client, now := readyActiveLDAPServiceTest(t)
	globalRole, err := store.CreateGlobalRole(db.Role{
		ID: "login_auditor", Name: "Login auditor", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	_, err = service.SaveGroupMapping(context.Background(), pro_interfaces.LDAPGroupMappingRequest{
		ActorID: 1, ActorIsAdmin: true, Now: now,
		Mapping: pro_interfaces.LDAPGroupMapping{
			ID: "login-auditors", ProviderID: "corp", Enabled: true,
			GroupExternalID: "entryuuid:40112233-4455-6677-8899-aabbccddeeff",
			Target:          pro_interfaces.LDAPRoleTarget{Scope: pro_interfaces.LDAPRoleScopeGlobal, RoleID: string(globalRole.ID)},
		},
	})
	require.NoError(t, err)
	client.result.Identity = pro_interfaces.LDAPIdentity{
		ExternalID: "entryuuid:50112233-4455-6677-8899-aabbccddeeff",
		Username:   "login-user", Name: "Login User", Email: "login-user@example.test",
	}
	client.groupSnapshot = pro_interfaces.LDAPGroupDirectorySnapshot{
		Revision: "login-directory", CapturedAt: now,
		GroupExternalIDs: []string{"entryuuid:40112233-4455-6677-8899-aabbccddeeff"},
		Users: []pro_interfaces.LDAPDirectoryUser{{
			ExternalID:       client.result.Identity.ExternalID,
			GroupExternalIDs: []string{"entryuuid:40112233-4455-6677-8899-aabbccddeeff"},
		}},
	}
	user, err := service.Authenticate(context.Background(), pro_interfaces.LDAPAuthenticationRequest{
		ProviderID: "corp", Username: "login-user", Password: "password", Now: now.Add(time.Second),
	})
	require.NoError(t, err)
	assignments, err := store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	assert.Equal(t, globalRole.ID, assignments[0].RoleID)
}

func reconciliationStatuses(history []db.LDAPGroupReconciliation) []string {
	result := make([]string, 0, len(history))
	for _, item := range history {
		result = append(result, item.Status)
	}
	return result
}
