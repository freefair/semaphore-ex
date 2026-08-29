package pro_interfaces

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectPermissionCatalogIsStableTypedAndIsolated(t *testing.T) {
	catalog := ProjectPermissionCatalog()
	require.Len(t, catalog, 5)
	assert.Equal(t, []PermissionID{
		PermissionRunProjectTasks,
		PermissionUpdateProject,
		PermissionViewProjectResources,
		PermissionManageProjectResources,
		PermissionManageProjectUsers,
	}, []PermissionID{
		catalog[0].ID, catalog[1].ID, catalog[2].ID, catalog[3].ID, catalog[4].ID,
	})

	seenPermissions := make(map[db.ProjectUserPermission]struct{}, len(catalog))
	for _, definition := range catalog {
		assert.NotEmpty(t, definition.Description)
		assert.Equal(t, PermissionScopeProject, definition.Scope)
		assert.NotNil(t, definition.CapabilityPrerequisites)
		_, duplicate := seenPermissions[definition.Permission]
		assert.False(t, duplicate)
		seenPermissions[definition.Permission] = struct{}{}
	}

	catalog[0].CapabilityPrerequisites = append(
		catalog[0].CapabilityPrerequisites,
		CapabilityProjectRoles,
	)
	assert.Empty(t, ProjectPermissionCatalog()[0].CapabilityPrerequisites)
}
