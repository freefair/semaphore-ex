package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleMappingCompatibilityIdentityIsStable(t *testing.T) {
	m := db.RoleMapping{Provider: "corp", Group: "engineering", ProjectID: 7, RoleID: "builtin:task_runner"}
	a, b := mappingID(m), mappingID(m)
	assert.Equal(t, a, b)
	assert.Equal(t, compatibilityID("corp", a), compatibilityID("corp", b))
	assert.Regexp(t, `^upstream-role-[0-9a-f]{32}$`, a)
}
func TestRoleMappingFacadeExcludesNativeOIDCMappings(t *testing.T) {
	_, ok := mappingFromOIDC(db.OIDCGroupMapping{ID: "native", TargetScope: "project"})
	assert.False(t, ok)
}

func TestRoleMappingStoreCRUDRevokesOnlyOwnedGrant(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	previous := util.Config
	t.Cleanup(func() { util.Config = previous })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"corp": {GroupsClaim: "groups"}}}
	project, err := store.CreateProject(db.Project{Name: "mapped roles", Created: time.Now().UTC()})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "mapped-custom", Slug: "mapped-custom", Name: "Mapped custom", ProjectID: &project.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "mapped-user", Name: "Mapped user", Email: "mapped@example.test", External: true})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{UserID: user.ID, Type: db.IdentityTypeOidc, Provider: "corp", ExternalUID: "mapped-subject"})
	require.NoError(t, err)
	adapter := NewRoleMappingStore(store)
	builtin, err := adapter.CreateRoleMapping(db.RoleMapping{Provider: "corp", Group: "owners", ProjectID: project.ID, RoleID: "builtin:owner"})
	require.NoError(t, err)
	created, err := adapter.CreateRoleMapping(db.RoleMapping{Provider: "corp", Group: "engineering", ProjectID: project.ID, RoleID: role.ID})
	require.NoError(t, err)
	mappings, err := adapter.GetRoleMappings()
	require.NoError(t, err)
	assert.Contains(t, mappings, builtin)
	assert.Contains(t, mappings, created)
	mappingID := mappingID(db.RoleMapping{Provider: "corp", Group: "engineering", ProjectID: project.ID, RoleID: role.ID})
	revision, err := store.GetOIDCGroupMappingRevision("corp")
	require.NoError(t, err)
	_, err = store.ApplyOIDCGroupReconciliation(db.OIDCGroupReconciliation{ProviderID: "corp", UserID: user.ID, Source: "login", Status: "applied", Token: "delete-owned", MappingRevision: revision, ClaimRevision: "claim", PreviewJSON: "{}", Created: time.Now().UTC()}, []db.OIDCGroupAssignmentChange{{MappingID: mappingID, UserID: user.ID, TargetScope: "project", ProjectID: &project.ID, RoleID: string(role.ID)}}, nil)
	require.NoError(t, err)
	deleted, _, err := adapter.DeleteRoleMapping(created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, deleted)
	assignments, err := store.GetOIDCGroupRoleAssignments("corp", user.ID)
	require.NoError(t, err)
	assert.Empty(t, assignments)
	remaining, err := adapter.GetRoleMappings()
	require.NoError(t, err)
	assert.Contains(t, remaining, builtin)
	assert.NotContains(t, remaining, created)
}
