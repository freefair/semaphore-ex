package db

type ProjectUserRole string

const (
	ProjectOwner      ProjectUserRole = "owner"
	ProjectManager    ProjectUserRole = "manager"
	ProjectTaskRunner ProjectUserRole = "task_runner"
	ProjectGuest      ProjectUserRole = "guest"
	ProjectNone       ProjectUserRole = ""
)

type ProjectUserPermission int64

const (
	CanRunProjectTasks ProjectUserPermission = 1 << iota
	CanUpdateProject
	CanManageProjectResources
	CanManageProjectUsers
	CanViewProjectResources
)

var rolePermissions = map[ProjectUserRole]ProjectUserPermission{
	ProjectOwner: CanRunProjectTasks | CanUpdateProject | CanManageProjectResources |
		CanManageProjectUsers | CanViewProjectResources,
	ProjectManager:    CanRunProjectTasks | CanManageProjectResources | CanViewProjectResources,
	ProjectTaskRunner: CanRunProjectTasks | CanViewProjectResources,
	ProjectGuest:      CanViewProjectResources,
}

func (r ProjectUserRole) IsValid() bool {
	_, ok := rolePermissions[r]
	return ok
}

type ProjectUser struct {
	ID        int             `db:"id" json:"-"`
	ProjectID int             `db:"project_id" json:"project_id"`
	UserID    int             `db:"user_id" json:"user_id"`
	Role      ProjectUserRole `db:"role" json:"role"`
	RoleID    *ProjectRoleID  `db:"role_id" json:"role_id,omitempty"`
	Revision  int             `db:"revision" json:"revision"`
}

func (r ProjectUserRole) Can(permissions ProjectUserPermission) bool {
	return (rolePermissions[r] & permissions) == permissions
}

func (p ProjectUserPermission) Can(permissions ProjectUserPermission) bool {
	return (p & permissions) == permissions
}

func (r ProjectUserRole) GetPermissions() ProjectUserPermission {
	return rolePermissions[r]
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
