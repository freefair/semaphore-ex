package db

import (
	"errors"
	"strings"
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
	// CanListGrantedCredentials permits viewing safe metadata for global
	// credentials explicitly granted to this project. It does not permit use.
	CanListGrantedCredentials
	// CanConsumeGrantedCredentials is reserved for Slice 061's runtime
	// resolver and is intentionally not implied by list/reference access.
	CanConsumeGrantedCredentials
	// CanOverrideDeploymentWindow is intentionally independent from ordinary
	// task-start permission. It authorizes an explicit, audited emergency
	// admission override only; it never makes scheduled or automatic starts
	// override-capable.
	CanOverrideDeploymentWindow
	// CanManagePolicyGuardrails permits ordinary project policy governance.
	CanManagePolicyGuardrails
	// CanRollbackPolicyGuardrails permits audited break-glass rollback only.
	CanRollbackPolicyGuardrails
)

var rolePermissions = map[ProjectUserRole]ProjectUserPermission{
	ProjectOwner: CanRunProjectTasks | CanUpdateProject | CanManageProjectResources |
		CanManageProjectUsers | CanViewProjectResources | CanViewWorkflows |
		CanEditWorkflows | CanStartWorkflows | CanStopWorkflows | CanAdministerWorkflows |
		CanListGrantedCredentials | CanConsumeGrantedCredentials | CanOverrideDeploymentWindow | CanManagePolicyGuardrails | CanRollbackPolicyGuardrails,
	ProjectManager: CanRunProjectTasks | CanManageProjectResources | CanViewProjectResources |
		CanViewWorkflows | CanEditWorkflows | CanStartWorkflows | CanStopWorkflows |
		CanAdministerWorkflows | CanListGrantedCredentials | CanConsumeGrantedCredentials |
		CanOverrideDeploymentWindow | CanManagePolicyGuardrails | CanRollbackPolicyGuardrails,
	ProjectTaskRunner: CanRunProjectTasks | CanViewProjectResources |
		CanViewWorkflows | CanStartWorkflows | CanStopWorkflows |
		CanListGrantedCredentials | CanConsumeGrantedCredentials,
	ProjectGuest: CanViewProjectResources | CanViewWorkflows,
}

// ProjectRoleReference is the stable role identity stored in workflow policy.
// It deliberately never uses a display name or legacy role slug.
type ProjectRoleReference string

const (
	BuiltinProjectRoleReferenceOwner      ProjectRoleReference = "builtin:owner"
	BuiltinProjectRoleReferenceManager    ProjectRoleReference = "builtin:manager"
	BuiltinProjectRoleReferenceTaskRunner ProjectRoleReference = "builtin:task_runner"
	BuiltinProjectRoleReferenceGuest      ProjectRoleReference = "builtin:guest"
)

const customProjectRoleReferencePrefix = "role:"

// ProjectRoleReferenceForBuiltInRole returns the stable workflow identity for
// one built-in project role.
func ProjectRoleReferenceForBuiltInRole(role ProjectUserRole) (ProjectRoleReference, bool) {
	switch role {
	case ProjectOwner:
		return BuiltinProjectRoleReferenceOwner, true
	case ProjectManager:
		return BuiltinProjectRoleReferenceManager, true
	case ProjectTaskRunner:
		return BuiltinProjectRoleReferenceTaskRunner, true
	case ProjectGuest:
		return BuiltinProjectRoleReferenceGuest, true
	default:
		return "", false
	}
}

// ProjectRoleReferenceForCustomRole returns the stable workflow identity for
// a project-scoped custom role.
func ProjectRoleReferenceForCustomRole(roleID ProjectRoleID) ProjectRoleReference {
	return ProjectRoleReference(customProjectRoleReferencePrefix + string(roleID))
}

// IsBuiltInProjectRoleReference reports whether the reference addresses a
// built-in role rather than a database role.
func IsBuiltInProjectRoleReference(reference ProjectRoleReference) bool {
	switch reference {
	case BuiltinProjectRoleReferenceOwner,
		BuiltinProjectRoleReferenceManager,
		BuiltinProjectRoleReferenceTaskRunner,
		BuiltinProjectRoleReferenceGuest:
		return true
	default:
		return false
	}
}

// IsValidProjectRoleReferenceSyntax validates the stable serialized identity.
// Custom-role existence is intentionally a separate live lookup so a deleted
// role fails closed at authorization time.
func IsValidProjectRoleReferenceSyntax(reference ProjectRoleReference) bool {
	if IsBuiltInProjectRoleReference(reference) {
		return true
	}
	value := strings.TrimPrefix(string(reference), customProjectRoleReferencePrefix)
	if value == string(reference) || value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// KnownProjectRoleReferences adds every built-in role to the supplied custom
// role references. Authorization callers must provide this live set so a
// deleted custom role cannot accidentally remain eligible from a snapshot.
func KnownProjectRoleReferences(customRoleIDs []ProjectRoleID) map[ProjectRoleReference]bool {
	known := map[ProjectRoleReference]bool{
		BuiltinProjectRoleReferenceOwner:      true,
		BuiltinProjectRoleReferenceManager:    true,
		BuiltinProjectRoleReferenceTaskRunner: true,
		BuiltinProjectRoleReferenceGuest:      true,
	}
	for _, roleID := range customRoleIDs {
		if strings.TrimSpace(string(roleID)) != "" {
			known[ProjectRoleReferenceForCustomRole(roleID)] = true
		}
	}
	return known
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

// ProjectWorkflowRoleOrigin records the current source of a project role.
// It is narrower than authentication identity: a user may have several
// external identities but exactly one effective project membership.
type ProjectWorkflowRoleOrigin string

const (
	ProjectWorkflowRoleOriginBuiltIn ProjectWorkflowRoleOrigin = "built_in"
	ProjectWorkflowRoleOriginManual  ProjectWorkflowRoleOrigin = "manual"
	ProjectWorkflowRoleOriginLDAP    ProjectWorkflowRoleOrigin = "ldap"
	ProjectWorkflowRoleOriginOIDC    ProjectWorkflowRoleOrigin = "oidc"
)

var ErrProjectWorkflowRoleIdentityUnavailable = errors.New("project workflow role identity is unavailable")

// ProjectWorkflowRoleIdentity is the current, fail-closed authorization
// identity. DirectoryRevisionFingerprint is a SHA-256 fingerprint only; raw
// LDAP/OIDC revisions and claims never leave the repository boundary.
type ProjectWorkflowRoleIdentity struct {
	Reference                    ProjectRoleReference      `json:"reference"`
	Permissions                  ProjectUserPermission     `json:"permissions"`
	Revision                     int                       `json:"revision"`
	Origin                       ProjectWorkflowRoleOrigin `json:"origin"`
	DirectoryProviderID          string                    `json:"directory_provider_id,omitempty"`
	DirectoryMappingID           string                    `json:"directory_mapping_id,omitempty"`
	DirectoryMappingRevision     int                       `json:"directory_mapping_revision,omitempty"`
	DirectoryRevisionFingerprint string                    `json:"directory_revision_fingerprint,omitempty"`
}

// Validate prevents malformed or stale provenance from becoming an
// authorization grant. Custom-role existence is checked by the SQL resolver.
func (identity ProjectWorkflowRoleIdentity) Validate() error {
	if identity.Revision < 1 || ValidateProjectRoleReference(identity.Reference) != nil {
		return ErrProjectWorkflowRoleIdentityUnavailable
	}
	switch identity.Origin {
	case ProjectWorkflowRoleOriginBuiltIn:
		if !IsBuiltInProjectRoleReference(identity.Reference) ||
			identity.DirectoryProviderID != "" || identity.DirectoryMappingID != "" ||
			identity.DirectoryMappingRevision != 0 || identity.DirectoryRevisionFingerprint != "" {
			return ErrProjectWorkflowRoleIdentityUnavailable
		}
	case ProjectWorkflowRoleOriginManual:
		if IsBuiltInProjectRoleReference(identity.Reference) ||
			identity.DirectoryProviderID != "" || identity.DirectoryMappingID != "" ||
			identity.DirectoryMappingRevision != 0 || identity.DirectoryRevisionFingerprint != "" {
			return ErrProjectWorkflowRoleIdentityUnavailable
		}
	case ProjectWorkflowRoleOriginLDAP, ProjectWorkflowRoleOriginOIDC:
		if identity.DirectoryProviderID == "" || identity.DirectoryMappingID == "" ||
			identity.DirectoryMappingRevision < 1 ||
			!isCanonicalSHA256Fingerprint(identity.DirectoryRevisionFingerprint) {
			return ErrProjectWorkflowRoleIdentityUnavailable
		}
	default:
		return ErrProjectWorkflowRoleIdentityUnavailable
	}
	return nil
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
