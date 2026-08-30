package db

import (
	"errors"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

var (
	ErrProjectRoleRevisionConflict       = errors.New("project role revision conflict")
	ErrProjectMembershipRevisionConflict = errors.New("project membership revision conflict")
	ErrLastProjectAdministrator          = errors.New("project must retain an administrator")
	ErrProjectRoleAssigned               = errors.New("project role is assigned")
	ErrGlobalRoleRevisionConflict        = errors.New("global role revision conflict")
	ErrGlobalRoleAssignmentConflict      = errors.New("global role assignment revision conflict")
	ErrLastGlobalAdministrator           = errors.New("system must retain a global administrator")
	ErrGlobalRoleAssigned                = errors.New("global role is assigned")
	ErrTemplateRoleRevisionConflict      = errors.New("template role revision conflict")
	ErrLDAPGroupMappingRevisionConflict  = errors.New("LDAP group mapping revision conflict")
	ErrLDAPGroupPreviewStale             = errors.New("LDAP group preview is stale")
	ErrLDAPGroupMappingCollision         = errors.New("LDAP group mapping assignment collision")
	ErrOIDCGroupMappingRevisionConflict  = errors.New("OIDC group mapping revision conflict")
	ErrOIDCGroupPreviewStale             = errors.New("OIDC group mapping preview is stale")
	ErrOIDCGroupMappingCollision         = errors.New("OIDC group mapping assignment collision")
)

type Role struct {
	ID                ProjectRoleID         `db:"role_id" json:"id" backup:"-"`
	Slug              string                `db:"slug" json:"slug" backup:"-"`
	Name              string                `db:"name" json:"name"`
	Permissions       ProjectUserPermission `db:"permissions" json:"permissions"`
	GlobalPermissions GlobalPermission      `db:"global_permissions" json:"global_permissions" backup:"-"`
	ProjectID         *int                  `db:"project_id" json:"project_id"`
	Revision          int                   `db:"revision" json:"revision"`
}

func ValidateRole(role Role) error {
	if role.Name == "" {
		return &common_errors.ValidationError{Message: "Role name cannot be empty"}
	}
	if role.Slug == "" {
		return &common_errors.ValidationError{Message: "Role slug cannot be empty"}
	}
	// Built-in role slugs are reserved. Allowing a custom role to reuse one lets
	// it shadow the built-in role and escalate the permissions of its members.
	if ProjectUserRole(role.Slug).IsValid() {
		return &common_errors.ValidationError{Message: "Role slug is reserved and cannot be used: " + role.Slug}
	}
	return nil
}

type TemplateRolePerm struct {
	ID                 int                   `db:"id" json:"id"`
	RoleSlug           string                `db:"role_slug" json:"role_slug"`
	RoleID             *ProjectRoleID        `db:"role_id" json:"role_id,omitempty"`
	TemplateID         int                   `db:"template_id" json:"template_id"`
	ProjectID          int                   `db:"project_id" json:"project_id"`
	Permissions        ProjectUserPermission `db:"permissions" json:"permissions"`
	AllowedPermissions TemplatePermission    `db:"allowed_permissions" json:"allowed_permissions"`
	DeniedPermissions  TemplatePermission    `db:"denied_permissions" json:"denied_permissions"`
	Revision           int                   `db:"revision" json:"revision"`
}
