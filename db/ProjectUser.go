package db

import "errors"

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
	// Task-group permissions are deliberately separate from generic resource
	// management: sharing a group changes which other projects can serialize
	// their executions with it.
	CanReadTaskGroups
	CanCreateTaskGroups
	CanUpdateTaskGroups
	CanDeleteTaskGroups
	CanShareTaskGroups
	CanReadProject
	CanApproveWorkflows
	// Resource permissions deliberately occupy new bits so the EX migration can
	// translate every persisted coarse grant without changing an existing bit.
	CanViewTemplates ProjectUserPermission = 1 << (iota + 0)
	CanCreateTemplates
	CanUpdateTemplates
	CanDeleteTemplates
	CanViewInventories
	CanCreateInventories
	CanUpdateInventories
	CanDeleteInventories
	CanViewRepositories
	CanCreateRepositories
	CanUpdateRepositories
	CanDeleteRepositories
	CanViewAccessKeys
	CanCreateAccessKeys
	CanUpdateAccessKeys
	CanDeleteAccessKeys
	CanUseAccessKeys
	CanViewEnvironments
	CanCreateEnvironments
	CanUpdateEnvironments
	CanDeleteEnvironments
	CanViewSchedules
	CanCreateSchedules
	CanUpdateSchedules
	CanDeleteSchedules
	CanViewIntegrations
	CanCreateIntegrations
	CanUpdateIntegrations
	CanDeleteIntegrations
	CanViewViews
	CanCreateViews
	CanUpdateViews
	CanDeleteViews
)

const (
	AllProjectResourceViewPermissions = CanViewTemplates | CanViewInventories |
		CanViewRepositories | CanViewAccessKeys | CanViewEnvironments |
		CanViewSchedules | CanViewIntegrations | CanViewViews
	AllProjectResourceMutationPermissions = CanCreateTemplates | CanUpdateTemplates | CanDeleteTemplates |
		CanCreateInventories | CanUpdateInventories | CanDeleteInventories |
		CanCreateRepositories | CanUpdateRepositories | CanDeleteRepositories |
		CanCreateAccessKeys | CanUpdateAccessKeys | CanDeleteAccessKeys | CanUseAccessKeys |
		CanCreateEnvironments | CanUpdateEnvironments | CanDeleteEnvironments |
		CanCreateSchedules | CanUpdateSchedules | CanDeleteSchedules |
		CanCreateIntegrations | CanUpdateIntegrations | CanDeleteIntegrations |
		CanCreateViews | CanUpdateViews | CanDeleteViews
	AllProjectResourcePermissions = AllProjectResourceViewPermissions | AllProjectResourceMutationPermissions

	CanRunWorkflows          = CanStartWorkflows
	WorkflowGrantPermissions = CanRunWorkflows | CanEditWorkflows | CanApproveWorkflows | CanReadProject
	TemplateGrantPermissions = CanRunProjectTasks | CanUpdateTemplates | CanReadProject
)

// Viewer scopes a list to the requesting member.
type Viewer struct {
	UserID int
	Admin  bool
}

var rolePermissions = map[ProjectUserRole]ProjectUserPermission{
	ProjectOwner: CanRunProjectTasks | CanUpdateProject | AllProjectResourcePermissions |
		CanManageProjectUsers | CanViewWorkflows |
		CanEditWorkflows | CanStartWorkflows | CanStopWorkflows | CanAdministerWorkflows |
		CanListGrantedCredentials | CanConsumeGrantedCredentials | CanOverrideDeploymentWindow | CanManagePolicyGuardrails | CanRollbackPolicyGuardrails |
		CanReadTaskGroups | CanCreateTaskGroups | CanUpdateTaskGroups | CanDeleteTaskGroups | CanShareTaskGroups | CanReadProject | CanApproveWorkflows,
	ProjectManager: CanRunProjectTasks | AllProjectResourcePermissions |
		CanViewWorkflows | CanEditWorkflows | CanStartWorkflows | CanStopWorkflows |
		CanAdministerWorkflows | CanListGrantedCredentials | CanConsumeGrantedCredentials |
		CanOverrideDeploymentWindow | CanManagePolicyGuardrails | CanRollbackPolicyGuardrails |
		CanReadTaskGroups | CanCreateTaskGroups | CanUpdateTaskGroups | CanDeleteTaskGroups | CanShareTaskGroups | CanReadProject | CanApproveWorkflows,
	ProjectTaskRunner: CanRunProjectTasks | AllProjectResourceViewPermissions |
		CanViewWorkflows | CanStartWorkflows | CanStopWorkflows |
		CanListGrantedCredentials | CanConsumeGrantedCredentials | CanReadTaskGroups | CanReadProject | CanApproveWorkflows,
	ProjectGuest: AllProjectResourceViewPermissions | CanViewWorkflows | CanReadTaskGroups | CanReadProject,
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
	return rolePermissions[r].Can(permissions)
}

func (r ProjectUserRole) GetPermissions() ProjectUserPermission {
	return rolePermissions[r]
}
