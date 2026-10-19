package server

import (
	"context"
	"errors"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturedOIDCReconciler struct {
	pro_interfaces.OIDCGroupMappingService
	request pro_interfaces.OIDCGroupPreviewRequest
}

func (s *capturedOIDCReconciler) ReconcileGroupMappings(_ context.Context, request pro_interfaces.OIDCGroupPreviewRequest) (pro_interfaces.OIDCGroupPreview, error) {
	s.request = request
	return pro_interfaces.OIDCGroupPreview{Additions: []pro_interfaces.OIDCGroupAssignmentChange{{
		MappingID: "upstream-role-a", UserID: request.UserID,
		Target: pro_interfaces.OIDCRoleTarget{Scope: pro_interfaces.OIDCRoleScopeProject, ProjectID: 9, RoleID: "builtin:task_runner"},
	}}}, nil
}

func (*capturedOIDCReconciler) GroupMappings(context.Context, string) ([]pro_interfaces.OIDCGroupMapping, error) {
	return []pro_interfaces.OIDCGroupMapping{{ID: "upstream-role-a", ClaimValue: "engineering"}}, nil
}

func TestRoleSyncServiceUsesConfiguredGroupsClaimAlias(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"corp": {GroupsClaim: "realm.groups"}}}
	reconciler := &capturedOIDCReconciler{}
	added, removed, err := NewRoleSyncServiceWithOIDC(reconciler).SyncLoginRoles(17, "CORP", map[string]any{"realm": map[string]any{"groups": []any{"engineering"}}})
	require.NoError(t, err)
	assert.Empty(t, removed)
	require.Len(t, added, 1)
	assert.Equal(t, db.ProjectRoleID("builtin:task_runner"), added[0].RoleID)
	assert.True(t, reconciler.request.Claim.Trusted)
	assert.Equal(t, []string{"engineering"}, reconciler.request.Claim.Values)
}

func TestRoleSyncServiceRejectsMalformedGroupsWithoutReconciling(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"corp": {GroupClaimPath: "groups"}}}
	reconciler := &capturedOIDCReconciler{}
	_, _, err := NewRoleSyncServiceWithOIDC(reconciler).SyncLoginRoles(17, "corp", map[string]any{"groups": []any{"engineering", 9}})
	require.Error(t, err)
	assert.ErrorIs(t, err, pro_interfaces.ErrGroupsClaim)
	assert.Empty(t, reconciler.request.ProviderID)
}

func TestRoleSyncServiceRejectsConflictingClaimAliases(t *testing.T) {
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"corp": {GroupClaimPath: "groups", GroupsClaim: "realm.groups"}}}
	_, _, err := NewRoleSyncServiceWithOIDC(&capturedOIDCReconciler{}).SyncLoginRoles(17, "corp", map[string]any{"groups": "engineering"})
	assert.Error(t, err)
	assert.False(t, errors.Is(err, pro_interfaces.ErrGroupsClaim))
}

func TestRoleSyncServiceStoreAdapterAddsAndRemovesMappedRole(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"corp": {GroupsClaim: "groups", GroupClaimMissingPolicy: "preserve"}}}
	project, err := store.CreateProject(db.Project{Name: "sync"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "sync-role", Slug: "sync-role", Name: "Sync role", ProjectID: &project.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "sync-user", Name: "Sync", Email: "sync@example.test", External: true})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "corp", ExternalUID: "sync-sub"})
	require.NoError(t, err)
	mappings := proFactory.NewRoleMappingStore(store)
	_, err = mappings.CreateRoleMapping(db.RoleMapping{Provider: "corp", Group: "engineering", ProjectID: project.ID, RoleID: role.ID})
	require.NoError(t, err)
	service := NewRoleSyncService(mappings)
	added, removed, err := service.SyncLoginRoles(user.ID, "corp", map[string]any{"groups": []any{"engineering"}})
	require.NoError(t, err)
	require.Len(t, added, 1)
	assert.Empty(t, removed)
	assert.Equal(t, "corp", added[0].Provider)
	assert.Equal(t, "engineering", added[0].Group)
	added, removed, err = service.SyncLoginRoles(user.ID, "corp", map[string]any{"groups": []any{}})
	require.NoError(t, err)
	assert.Empty(t, added)
	require.Len(t, removed, 1)
	_, _, err = service.SyncLoginRoles(user.ID, "corp", map[string]any{"groups": []any{"engineering", 3}})
	assert.ErrorIs(t, err, pro_interfaces.ErrGroupsClaim)
	added, removed, err = service.SyncLoginRoles(user.ID, "corp", map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.Empty(t, removed)
	added, removed, err = service.SyncLoginRoles(user.ID, "", map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.Empty(t, removed)
}
