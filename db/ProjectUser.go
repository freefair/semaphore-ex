package db

import (
	"errors"
)

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
	CanViewWorkflows
	CanEditWorkflows
	CanStartWorkflows
	CanStopWorkflows
	CanAdministerWorkflows
)

var rolePermissions = map[ProjectUserRole]ProjectUserPermission{
	ProjectOwner: CanRunProjectTasks | CanUpdateProject | CanManageProjectResources |
		CanManageProjectUsers | CanViewProjectResources | CanViewWorkflows |
		CanEditWorkflows | CanStartWorkflows | CanStopWorkflows | CanAdministerWorkflows,
	ProjectManager: CanRunProjectTasks | CanManageProjectResources | CanViewProjectResources |
		CanViewWorkflows | CanEditWorkflows | CanStartWorkflows | CanStopWorkflows |
		CanAdministerWorkflows,
	ProjectTaskRunner: CanRunProjectTasks | CanViewProjectResources |
		CanViewWorkflows | CanStartWorkflows | CanStopWorkflows,
	ProjectGuest: CanViewProjectResources | CanViewWorkflows,
}

func (r ProjectUserRole) IsValid() bool {
	_, ok := rolePermissions[r]
	return ok
}

type ProjectUser struct {
	ID                           int             `db:"id" json:"-"`
	ProjectID                    int             `db:"project_id" json:"project_id"`
	UserID                       int             `db:"user_id" json:"user_id"`
	Role                         ProjectUserRole `db:"role" json:"role"`
	RoleID                       *ProjectRoleID  `db:"role_id" json:"role_id,omitempty"`
	Revision                     int             `db:"revision" json:"revision"`
	LDAPGroupManagedAssignmentID *int            `db:"ldap_group_managed_assignment_id" json:"-"`
	OIDCGroupManagedAssignmentID *int            `db:"oidc_group_managed_assignment_id" json:"-"`
}

var ErrProjectWorkflowRoleIdentityUnavailable = errors.New("project workflow role identity is unavailable")

func (r ProjectUserRole) Can(permissions ProjectUserPermission) bool {
	return (rolePermissions[r] & permissions) == permissions
}

func (r ProjectUserRole) GetPermissions() ProjectUserPermission {
	return rolePermissions[r]
}
