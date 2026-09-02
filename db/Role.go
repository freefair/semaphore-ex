package db

import (
	"errors"
	"strings"

	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

type ProjectRoleID string

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

// GlobalPermission is deliberately separate from ProjectUserPermission. The
// same numeric bit may therefore mean different things only when accompanied
// by its explicit scope.
type GlobalPermission int64

const (
	CanManageGlobalUsers GlobalPermission = 1 << iota
	CanManageGlobalRoles
	CanManageGlobalSystem
	CanReadGlobalAudit
	CanManageGlobalCredentialsMetadata
	CanManageGlobalCredentialsRotate
	CanManageGlobalCredentialsGrant
)

const AllGlobalPermissions = CanManageGlobalUsers | CanManageGlobalRoles |
	CanManageGlobalSystem | CanReadGlobalAudit | CanManageGlobalCredentialsMetadata |
	CanManageGlobalCredentialsRotate | CanManageGlobalCredentialsGrant

func (p GlobalPermission) Can(permission GlobalPermission) bool {
	return p&permission == permission
}

// TemplatePermission represents actions on one template. Project permissions
// are mapped to these actions explicitly by the permission evaluator.
type TemplatePermission int64

const (
	CanReadTemplate TemplatePermission = 1 << iota
	CanRunTemplate
	CanEditTemplate
	CanDeleteTemplate
)

func (p TemplatePermission) Can(permission TemplatePermission) bool {
	return p&permission == permission
}

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

// ValidateProjectRole validates the Enhanced project-scoped role contract.
// Slug remains an internal compatibility key; callers address roles by ID.
func ValidateProjectRole(role Role) error {
	if role.ProjectID == nil || *role.ProjectID <= 0 {
		return &common_errors.ValidationError{Message: "Project role requires a project"}
	}
	if strings.TrimSpace(string(role.ID)) == "" {
		return &common_errors.ValidationError{Message: "Project role ID cannot be empty"}
	}
	if strings.TrimSpace(role.Name) == "" {
		return &common_errors.ValidationError{Message: "Role name cannot be empty"}
	}
	if role.Revision <= 0 {
		return &common_errors.ValidationError{Message: "Project role revision must be positive"}
	}
	const knownPermissions = CanRunProjectTasks | CanUpdateProject | CanManageProjectResources |
		CanManageProjectUsers | CanViewProjectResources | CanViewWorkflows |
		CanEditWorkflows | CanStartWorkflows | CanStopWorkflows | CanAdministerWorkflows |
		CanListGrantedCredentials | CanConsumeGrantedCredentials | CanOverrideDeploymentWindow
	if role.Permissions&^knownPermissions != 0 {
		return &common_errors.ValidationError{Message: "Project role contains unknown permissions"}
	}
	if role.GlobalPermissions != 0 {
		return &common_errors.ValidationError{Message: "Project role cannot contain global permissions"}
	}
	return nil
}

// ValidateProjectRoleReference verifies only the durable reference shape.
// Whether a custom role still exists is intentionally evaluated from the
// current role store at authorization time.
func ValidateProjectRoleReference(reference ProjectRoleReference) error {
	if !IsValidProjectRoleReferenceSyntax(reference) {
		return &common_errors.ValidationError{Message: "Workflow role reference is invalid"}
	}
	return nil
}

// ValidateGlobalRole validates the enhanced system-scoped role fields while
// retaining the legacy project-permission column for upstream compatibility.
func ValidateGlobalRole(role Role) error {
	if role.ProjectID != nil {
		return &common_errors.ValidationError{Message: "Global role cannot belong to a project"}
	}
	if strings.TrimSpace(string(role.ID)) == "" {
		return &common_errors.ValidationError{Message: "Global role ID cannot be empty"}
	}
	if strings.TrimSpace(role.Name) == "" {
		return &common_errors.ValidationError{Message: "Role name cannot be empty"}
	}
	if role.Revision <= 0 {
		return &common_errors.ValidationError{Message: "Global role revision must be positive"}
	}
	const known = CanManageGlobalUsers | CanManageGlobalRoles |
		CanManageGlobalSystem | CanReadGlobalAudit | CanManageGlobalCredentialsMetadata |
		CanManageGlobalCredentialsRotate | CanManageGlobalCredentialsGrant
	if role.GlobalPermissions&^known != 0 {
		return &common_errors.ValidationError{Message: "Global role contains unknown permissions"}
	}
	return nil
}

type GlobalRoleAssignment struct {
	ID       int           `db:"id" json:"id"`
	UserID   int           `db:"user_id" json:"user_id"`
	RoleID   ProjectRoleID `db:"role_id" json:"role_id"`
	Revision int           `db:"revision" json:"revision"`

	RoleName          string           `db:"role_name" json:"role_name,omitempty"`
	GlobalPermissions GlobalPermission `db:"global_permissions" json:"global_permissions,omitempty"`
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

type TemplatePermissionContext struct {
	RoleID               string                `json:"role_id"`
	RoleName             string                `json:"role_name"`
	ProjectPermissions   ProjectUserPermission `json:"project_permissions"`
	EffectivePermissions TemplatePermission    `json:"effective_permissions"`
	Override             *TemplateRolePerm     `json:"override,omitempty"`
}

// ProjectPermissionsToTemplate is the explicit inheritance map between the
// broad project role and independent template actions.
func ProjectPermissionsToTemplate(permissions ProjectUserPermission) TemplatePermission {
	var result TemplatePermission
	if permissions.Can(CanViewProjectResources) {
		result |= CanReadTemplate
	}
	if permissions.Can(CanRunProjectTasks) {
		result |= CanRunTemplate
	}
	if permissions.Can(CanManageProjectResources) {
		result |= CanEditTemplate | CanDeleteTemplate
	}
	return result
}

func ApplyTemplatePermissionOverride(
	inherited TemplatePermission,
	override *TemplateRolePerm,
) TemplatePermission {
	if override == nil {
		return inherited
	}
	return (inherited | override.AllowedPermissions) &^ override.DeniedPermissions
}

// TemplatePermissionsToProject retains the legacy response mask while scoped
// middleware uses TemplatePermission for independent edit/delete decisions.
func TemplatePermissionsToProject(permissions TemplatePermission) ProjectUserPermission {
	var result ProjectUserPermission
	if permissions.Can(CanReadTemplate) {
		result |= CanViewProjectResources
	}
	if permissions.Can(CanRunTemplate) {
		result |= CanRunProjectTasks
	}
	if permissions.Can(CanEditTemplate) || permissions.Can(CanDeleteTemplate) {
		result |= CanManageProjectResources
	}
	return result
}

func ValidateTemplateRolePerm(permission TemplateRolePerm) error {
	if permission.ProjectID <= 0 || permission.TemplateID <= 0 {
		return &common_errors.ValidationError{Message: "Template role requires a project and template"}
	}
	if permission.RoleID == nil && strings.TrimSpace(permission.RoleSlug) == "" {
		return &common_errors.ValidationError{Message: "Template role requires a role"}
	}
	if permission.Revision <= 0 {
		return &common_errors.ValidationError{Message: "Template role revision must be positive"}
	}
	const known = CanReadTemplate | CanRunTemplate | CanEditTemplate | CanDeleteTemplate
	if permission.AllowedPermissions&^known != 0 || permission.DeniedPermissions&^known != 0 {
		return &common_errors.ValidationError{Message: "Template role contains unknown permissions"}
	}
	if permission.AllowedPermissions&permission.DeniedPermissions != 0 {
		return &common_errors.ValidationError{Message: "Template role cannot allow and deny the same permission"}
	}
	return nil
}
