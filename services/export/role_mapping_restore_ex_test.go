package export

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleMappingRestoreUsesRemappedCustomRoleIdentity(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{
		"corp": {GroupsClaim: "groups"},
	}}

	sourceProject, err := store.CreateProject(db.Project{Name: "role mapping source"})
	require.NoError(t, err)
	sourceRole, err := store.CreateProjectRole(db.Role{
		ID:          "source-custom-role",
		Slug:        "source-custom-role",
		Name:        "Source custom role",
		ProjectID:   &sourceProject.ID,
		Permissions: db.CanRunProjectTasks,
		Revision:    1,
	})
	require.NoError(t, err)

	sourceMapping, err := proFactory.NewRoleMappingStore(store).CreateRoleMapping(db.RoleMapping{
		Provider:  "corp",
		Group:     "engineering",
		ProjectID: sourceProject.ID,
		RoleID:    sourceRole.ID,
	})
	require.NoError(t, err)

	destinationProject, err := store.CreateProject(db.Project{Name: "role mapping destination"})
	require.NoError(t, err)
	assert.NotEqual(t, sourceProject.ID, destinationProject.ID)

	chain := InitProjectExporters(NewKeyMapper(), true, false)
	require.NoError(t, chain.mapKeys(Project, GlobalScope, NewKeyFromInt(sourceProject.ID), NewKeyFromInt(destinationProject.ID)))

	roleExporter := &RoleExporter{}
	require.NoError(t, roleExporter.restoreValue(EntityObject[db.Role]{
		value: sourceRole,
		scope: GlobalScope,
	}, store, chain))

	remappedRoleID, err := chain.getNewKey(Role, GlobalScope, sourceRole.GetDbKey())
	require.NoError(t, err)
	assert.NotEqual(t, sourceRole.ID, db.ProjectRoleID(remappedRoleID))
	destinationRole, err := store.GetProjectRoleByID(destinationProject.ID, db.ProjectRoleID(remappedRoleID))
	require.NoError(t, err)

	mappingExporter := &RoleMappingExporter{}
	require.NoError(t, mappingExporter.restoreValue(EntityObject[roleMapping]{
		value: roleMapping{
			ID:        sourceMapping.ID,
			Provider:  sourceMapping.Provider,
			Group:     sourceMapping.Group,
			ProjectID: sourceMapping.ProjectID,
			RoleID:    sourceMapping.RoleID,
		},
		scope: GlobalScope,
	}, store, chain))

	mappings, err := proFactory.NewRoleMappingStore(store).GetRoleMappings()
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	var restored db.RoleMapping
	for _, mapping := range mappings {
		if mapping.ProjectID == destinationProject.ID {
			restored = mapping
			break
		}
	}
	require.NotZero(t, restored.ID)
	assert.Equal(t, "corp", restored.Provider)
	assert.Equal(t, "engineering", restored.Group)
	assert.Equal(t, destinationProject.ID, restored.ProjectID)
	assert.Equal(t, destinationRole.ID, restored.RoleID)

	unchangedSourceRole, err := store.GetProjectRoleByID(sourceProject.ID, sourceRole.ID)
	require.NoError(t, err)
	assert.Equal(t, sourceRole.ID, unchangedSourceRole.ID)
	assert.Equal(t, sourceProject.ID, *unchangedSourceRole.ProjectID)
}
