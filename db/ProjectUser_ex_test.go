package db

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestBuiltInProjectRolePermissionsRemainDeterministic(t *testing.T) {
	expected := map[ProjectUserRole]ProjectUserPermission{
		ProjectOwner: CanRunProjectTasks | CanUpdateProject | CanManageProjectResources |
			CanManageProjectUsers | CanViewProjectResources,
		ProjectManager:    CanRunProjectTasks | CanManageProjectResources | CanViewProjectResources,
		ProjectTaskRunner: CanRunProjectTasks | CanViewProjectResources,
		ProjectGuest:      CanViewProjectResources,
	}

	permissions := BuiltInProjectRolePermissions()
	assert.Equal(t, expected, permissions)
	for role, permission := range expected {
		assert.Equal(t, permission, role.GetPermissions())
		assert.True(t, role.Can(CanViewProjectResources))
	}

	permissions[ProjectGuest] = 0
	assert.Equal(t, CanViewProjectResources, ProjectGuest.GetPermissions())
}
