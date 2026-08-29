package db

func (p ProjectUserPermission) Can(permissions ProjectUserPermission) bool {
	return (p & permissions) == permissions
}

// BuiltInProjectRolePermissions returns an isolated deterministic mapping for
// compatibility checks and API presentation.
func BuiltInProjectRolePermissions() map[ProjectUserRole]ProjectUserPermission {
	result := make(map[ProjectUserRole]ProjectUserPermission, len(rolePermissions))
	for role, permissions := range rolePermissions {
		result[role] = permissions
	}
	return result
}
