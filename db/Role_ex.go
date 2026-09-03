package db

import (
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"strings"
)

type ProjectRoleID string

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
	// CanManageGlobalPolicyGuardrails permits global policy governance.
	CanManageGlobalPolicyGuardrails
	// CanRollbackGlobalPolicyGuardrails permits global break-glass rollback.
	CanRollbackGlobalPolicyGuardrails
)

const AllGlobalPermissions = CanManageGlobalUsers | CanManageGlobalRoles |
	CanManageGlobalSystem | CanReadGlobalAudit | CanManageGlobalCredentialsMetadata |
	CanManageGlobalCredentialsRotate | CanManageGlobalCredentialsGrant | CanManageGlobalPolicyGuardrails | CanRollbackGlobalPolicyGuardrails

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
		CanListGrantedCredentials | CanConsumeGrantedCredentials | CanOverrideDeploymentWindow | CanManagePolicyGuardrails | CanRollbackPolicyGuardrails
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
		CanManageGlobalCredentialsRotate | CanManageGlobalCredentialsGrant | CanManageGlobalPolicyGuardrails | CanRollbackGlobalPolicyGuardrails
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
