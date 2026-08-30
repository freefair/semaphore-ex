package features

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staleOnceOIDCRepository struct {
	db.OIDCGroupMappingRepository
	stale bool
}

func (r *staleOnceOIDCRepository) ApplyOIDCGroupReconciliation(
	reconciliation db.OIDCGroupReconciliation,
	additions []db.OIDCGroupAssignmentChange,
	removals []db.OIDCGroupAssignmentChange,
) (db.OIDCGroupReconciliation, error) {
	if !r.stale {
		r.stale = true
		return db.OIDCGroupReconciliation{}, db.ErrOIDCGroupPreviewStale
	}
	return r.OIDCGroupMappingRepository.ApplyOIDCGroupReconciliation(reconciliation, additions, removals)
}

func TestOIDCGroupMappingLoginReconciliationLifecycle(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_800_100_000, 0).UTC()
	service := NewOIDCGroupMappingService(store)
	configuration := pro_interfaces.OIDCGroupClaimConfiguration{
		Path: "realm.groups", MissingClaimPolicy: pro_interfaces.OIDCMissingClaimPreserve,
	}
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "oidc-admin", Name: "OIDC Admin", Email: "admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "oidc-user", Name: "OIDC User", Email: "user@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "corp", ExternalUID: "subject-1",
	})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "OIDC mapped project", Created: now})
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

	projectMapping, err := service.SaveGroupMapping(context.Background(), pro_interfaces.OIDCGroupMappingRequest{
		ActorID: admin.ID, ActorIsAdmin: true, Configuration: configuration, Now: now,
		Mapping: pro_interfaces.OIDCGroupMapping{
			ID: "project-runners", ProviderID: "corp", ClaimValue: "engineering", Enabled: true,
			Target: pro_interfaces.OIDCRoleTarget{
				Scope: pro_interfaces.OIDCRoleScopeProject, ProjectID: project.ID, RoleID: string(projectRole.ID),
			},
		},
	})
	require.NoError(t, err)
	_, err = service.SaveGroupMapping(context.Background(), pro_interfaces.OIDCGroupMappingRequest{
		ActorID: admin.ID, ActorIsAdmin: true, Configuration: configuration, Now: now,
		Mapping: pro_interfaces.OIDCGroupMapping{
			ID: "global-auditors", ProviderID: "corp", ClaimValue: "auditors", Enabled: true,
			Target: pro_interfaces.OIDCRoleTarget{Scope: pro_interfaces.OIDCRoleScopeGlobal, RoleID: string(globalRole.ID)},
		},
	})
	require.NoError(t, err)

	arrayClaim, err := pro_interfaces.ParseOIDCGroupClaim(map[string]any{
		"realm":        map[string]any{"groups": []any{"engineering", "auditors", "unknown"}},
		"access_token": "must-never-be-persisted",
	}, configuration)
	require.NoError(t, err)
	preview, err := service.PreviewGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{
		ActorID: &admin.ID, ActorIsAdmin: true, ProviderID: "corp", Configuration: configuration,
		Claim: arrayClaim, UserID: user.ID, Source: "manual", Now: now.Add(time.Second),
	})
	require.NoError(t, err)
	assert.Len(t, preview.Additions, 2)
	assert.Equal(t, []string{"unknown"}, preview.UnknownValues)
	assert.NotContains(t, preview.Token, "must-never-be-persisted")

	applied, err := service.ReconcileGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{
		ProviderID: "corp", Configuration: configuration, Claim: arrayClaim,
		UserID: user.ID, Source: "login", Now: now.Add(2 * time.Second),
	})
	require.NoError(t, err)
	assert.Len(t, applied.Additions, 2)
	projectMembership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	require.NotNil(t, projectMembership.OIDCGroupManagedAssignmentID)
	globalAssignments, err := store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	require.Len(t, globalAssignments, 1)

	scalarClaim, err := pro_interfaces.ParseOIDCGroupClaim(map[string]any{
		"realm": map[string]any{"groups": "auditors"},
	}, configuration)
	require.NoError(t, err)
	applied, err = service.ReconcileGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{
		ProviderID: "corp", Configuration: configuration, Claim: scalarClaim,
		UserID: user.ID, Source: "login", Now: now.Add(3 * time.Second),
	})
	require.NoError(t, err)
	require.Len(t, applied.Removals, 1)
	assert.Equal(t, projectMapping.ID, applied.Removals[0].MappingID)
	_, err = store.GetProjectUser(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	missingPreserve, err := pro_interfaces.ParseOIDCGroupClaim(map[string]any{}, configuration)
	require.NoError(t, err)
	preserved, err := service.ReconcileGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{
		ProviderID: "corp", Configuration: configuration, Claim: missingPreserve,
		UserID: user.ID, Source: "login", Now: now.Add(4 * time.Second),
	})
	require.NoError(t, err)
	assert.True(t, preserved.Preserved)
	globalAssignments, err = store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	require.Len(t, globalAssignments, 1)

	clearConfiguration := configuration
	clearConfiguration.MissingClaimPolicy = pro_interfaces.OIDCMissingClaimClear
	missingClear, err := pro_interfaces.ParseOIDCGroupClaim(map[string]any{}, clearConfiguration)
	require.NoError(t, err)
	cleared, err := service.ReconcileGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{
		ProviderID: "corp", Configuration: clearConfiguration, Claim: missingClear,
		UserID: user.ID, Source: "login", Now: now.Add(5 * time.Second),
	})
	require.NoError(t, err)
	require.Len(t, cleared.Removals, 1)
	globalAssignments, err = store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	assert.Empty(t, globalAssignments)

	history, err := service.GroupReconciliationHistory(context.Background(), "corp", 20)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	for _, item := range history {
		assert.NotContains(t, item.PreviewJSON, "access_token")
		assert.NotContains(t, item.PreviewJSON, "must-never-be-persisted")
	}
}

func TestOIDCGroupMappingReconciliationRetriesOneStaleRevision(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_800_100_100, 0).UTC()
	repository := &staleOnceOIDCRepository{OIDCGroupMappingRepository: store}
	service := NewOIDCGroupMappingService(repository)
	configuration := pro_interfaces.OIDCGroupClaimConfiguration{Path: "groups"}
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "retry-user", Name: "Retry User", Email: "retry@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "corp", ExternalUID: "subject-retry",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "retry_auditor", Name: "Retry auditor", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	_, err = service.SaveGroupMapping(context.Background(), pro_interfaces.OIDCGroupMappingRequest{
		ActorID: 1, ActorIsAdmin: true, Configuration: configuration, Now: now,
		Mapping: pro_interfaces.OIDCGroupMapping{
			ID: "retry", ProviderID: "corp", ClaimValue: "auditors", Enabled: true,
			Target: pro_interfaces.OIDCRoleTarget{Scope: pro_interfaces.OIDCRoleScopeGlobal, RoleID: string(role.ID)},
		},
	})
	require.NoError(t, err)
	claim, err := pro_interfaces.ParseOIDCGroupClaim(map[string]any{"groups": "auditors"}, configuration)
	require.NoError(t, err)
	preview, err := service.ReconcileGroupMappings(context.Background(), pro_interfaces.OIDCGroupPreviewRequest{
		ProviderID: "corp", Configuration: configuration, Claim: claim,
		UserID: user.ID, Source: "login", Now: now.Add(time.Second),
	})
	require.NoError(t, err)
	assert.True(t, repository.stale)
	assert.Len(t, preview.Additions, 1)
	assignments, err := store.GetGlobalRoleAssignments(user.ID)
	require.NoError(t, err)
	require.Len(t, assignments, 1)
}

func TestOIDCGroupMappingRejectsUnauthorizedAndUnknownRoleTargets(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	service := NewOIDCGroupMappingService(store)
	request := pro_interfaces.OIDCGroupMappingRequest{
		ActorID: 7, Configuration: pro_interfaces.OIDCGroupClaimConfiguration{Path: "groups"},
		Now: time.Unix(1_800_100_200, 0).UTC(),
		Mapping: pro_interfaces.OIDCGroupMapping{
			ID: "invalid", ProviderID: "corp", ClaimValue: "engineering", Enabled: true,
			Target: pro_interfaces.OIDCRoleTarget{Scope: pro_interfaces.OIDCRoleScopeGlobal, RoleID: "missing"},
		},
	}
	_, err := service.SaveGroupMapping(context.Background(), request)
	assert.ErrorIs(t, err, pro_interfaces.ErrOIDCGroupMappingForbidden)
	request.ActorIsAdmin = true
	_, err = service.SaveGroupMapping(context.Background(), request)
	assert.Contains(t, err.Error(), "role target does not exist")
}
