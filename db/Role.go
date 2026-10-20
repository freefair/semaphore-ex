package db

import (
	"errors"
	"regexp"
	"strings"

	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

var (
	ErrProjectRoleRevisionConflict       = errors.New("project role revision conflict")
	ErrProjectMembershipRevisionConflict = errors.New("project membership revision conflict")
	ErrProjectMembershipConflict         = errors.New("project membership already exists")
	ErrLastProjectAdministrator          = errors.New("project must retain an administrator")
	ErrProjectRoleAssigned               = errors.New("project role is assigned")
	ErrProjectRoleForceProtected         = errors.New("project role has protected directory references")
	ErrGlobalRoleRevisionConflict        = errors.New("global role revision conflict")
	ErrGlobalRoleAssignmentConflict      = errors.New("global role assignment revision conflict")
	ErrLastGlobalAdministrator           = errors.New("system must retain a global administrator")
	ErrGlobalRoleAssigned                = errors.New("global role is assigned")
	ErrGlobalRoleForceProtected          = errors.New("global role has protected project or directory references")
	ErrTemplateRoleRevisionConflict      = errors.New("template role revision conflict")
	ErrLDAPGroupMappingRevisionConflict  = errors.New("LDAP group mapping revision conflict")
	ErrLDAPGroupPreviewStale             = errors.New("LDAP group preview is stale")
	ErrLDAPGroupMappingCollision         = errors.New("LDAP group mapping assignment collision")
	ErrOIDCGroupMappingRevisionConflict  = errors.New("OIDC group mapping revision conflict")
	ErrOIDCGroupPreviewStale             = errors.New("OIDC group mapping preview is stale")
	ErrOIDCGroupMappingCollision         = errors.New("OIDC group mapping assignment collision")
)

// Role keeps its external immutable identity separate from its mutable slug.
// All EX API and policy references use ID.

// GlobalPermission is an instance-wide permission bit held by a global role. It is a separate type from
// ProjectUserPermission on purpose: the same numeric bit means different things in the two scopes.
type GlobalPermission int64

const (
	// CanManageGlobalUsers creates, edits and deletes users. Only an admin may grant the admin flag.
	CanManageGlobalUsers GlobalPermission = 1 << iota
	// CanManageGlobalRoles manages global roles and their assignments to users.
	CanManageGlobalRoles
	// CanManageGlobalSystem changes instance settings: options, apps, runners, cluster, active tasks.
	CanManageGlobalSystem
	// CanReadGlobalAudit reads the audit log.
	CanReadGlobalAudit
	CanManageGlobalCredentialsMetadata
	CanManageGlobalCredentialsRotate
	CanManageGlobalCredentialsGrant
	CanManageGlobalPolicyGuardrails
	CanRollbackGlobalPolicyGuardrails
)

const AllGlobalPermissions = CanManageGlobalUsers | CanManageGlobalRoles | CanManageGlobalSystem | CanReadGlobalAudit |
	CanManageGlobalCredentialsMetadata | CanManageGlobalCredentialsRotate | CanManageGlobalCredentialsGrant |
	CanManageGlobalPolicyGuardrails | CanRollbackGlobalPolicyGuardrails

func (p GlobalPermission) Can(permission GlobalPermission) bool {
	return p&permission == permission
}

// GlobalPermissionDefinition describes one global permission bit for the API and the UI.
type GlobalPermissionDefinition struct {
	ID          string           `json:"id"`
	Permission  GlobalPermission `json:"permission"`
	Description string           `json:"description"`
}

// GlobalPermissionCatalog lists the global permissions in a stable order.
func GlobalPermissionCatalog() []GlobalPermissionDefinition {
	return []GlobalPermissionDefinition{
		{ID: "global.users.manage", Permission: CanManageGlobalUsers, Description: "Manage users"},
		{ID: "global.roles.manage", Permission: CanManageGlobalRoles, Description: "Manage global roles"},
		{ID: "global.system.manage", Permission: CanManageGlobalSystem, Description: "Manage system settings"},
		{ID: "global.audit.read", Permission: CanReadGlobalAudit, Description: "Read the audit log"},
		{ID: "global.credentials.metadata.manage", Permission: CanManageGlobalCredentialsMetadata, Description: "Manage global credential metadata"},
		{ID: "global.credentials.rotate", Permission: CanManageGlobalCredentialsRotate, Description: "Rotate global credentials"},
		{ID: "global.credentials.grant", Permission: CanManageGlobalCredentialsGrant, Description: "Grant global credentials"},
		{ID: "global.policy_guardrails.manage", Permission: CanManageGlobalPolicyGuardrails, Description: "Manage policy guardrails"},
		{ID: "global.policy_guardrails.rollback", Permission: CanRollbackGlobalPolicyGuardrails, Description: "Rollback policy guardrails"},
	}
}

type Role struct {
	ID                ProjectRoleID         `db:"role_id" json:"id" backup:"-"`
	Slug              string                `db:"slug" json:"slug" backup:"-"`
	Name              string                `db:"name" json:"name"`
	Permissions       ProjectUserPermission `db:"permissions" json:"permissions"`
	GlobalPermissions GlobalPermission      `db:"global_permissions" json:"global_permissions" backup:"-"`
	ProjectID         *int                  `db:"project_id" json:"project_id"`
	Revision          int                   `db:"revision" json:"revision"`
	Builtin           bool                  `db:"builtin" json:"builtin" backup:"-"`
}

const KnownRolePermissions = CanRunProjectTasks | CanUpdateProject | CanManageProjectResources | CanManageProjectUsers

func ValidateRole(role Role, isNew bool) error {
	if strings.TrimSpace(role.Name) == "" {
		return &common_errors.ValidationError{Message: "Role name cannot be empty"}
	}
	// Built-in roles define the complete current project-permission catalog.
	// Keeping this derived from Owner avoids rejecting newer EX bits in role
	// imports while still rejecting values outside the typed permission space.
	if role.Permissions&^ProjectOwner.GetPermissions() != 0 {
		return &common_errors.ValidationError{Message: "Role permissions contain unknown bits"}
	}
	if !isNew {
		return nil
	}
	validSlug, _ := regexp.MatchString(`^[A-Za-z0-9_-]{1,100}$`, role.Slug)
	if !validSlug {
		return &common_errors.ValidationError{Message: "Role slug must be 1 to 100 characters: letters A-Z, digits, '_' and '-'"}
	}
	// Built-ins are virtual immutable references. A custom row with the same
	// slug would shadow that identity and could change effective permissions.
	if ProjectUserRole(strings.ToLower(role.Slug)).IsValid() {
		return &common_errors.ValidationError{Message: "Role slug is reserved and cannot be used: " + role.Slug}
	}
	if strings.EqualFold(role.Slug, "all") {
		return &common_errors.ValidationError{Message: "Role slug is reserved: " + role.Slug}
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

type RoleImpact struct {
	Members        int `json:"members"`
	Projects       int `json:"projects"`
	TemplateGrants int `json:"template_grants"`
	Mappings       int `json:"mappings"`
}

type RoleMember struct {
	ProjectID int
	UserID    int
	Username  string
	Roles     []Role
	// The member also holds roles through mappings of other roles, so losing every manual role keeps the membership.
	Mapped bool
}

type RoleAssignments struct {
	Members         []RoleMember
	Grants          []TemplateRolePerm
	GlobalMembers   int
	Mappings        []RoleMapping
	MappingCount    int
	MappingProjects []int
}

func (a RoleAssignments) Impact() RoleImpact {
	type member struct{ projectID, userID int }
	members := map[member]bool{}
	projects := map[int]bool{}
	for _, m := range a.Members {
		members[member{m.ProjectID, m.UserID}] = true
		projects[m.ProjectID] = true
	}
	for _, g := range a.Grants {
		projects[g.ProjectID] = true
	}

	for _, m := range a.Mappings {
		projects[m.ProjectID] = true
	}
	for _, projectID := range a.MappingProjects {
		if projectID > 0 {
			projects[projectID] = true
		}
	}
	return RoleImpact{Members: len(members) + a.GlobalMembers, Projects: len(projects), TemplateGrants: len(a.Grants), Mappings: len(a.Mappings) + a.MappingCount}
}
